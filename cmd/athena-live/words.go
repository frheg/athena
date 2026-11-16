package main

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var word = regexp.MustCompile(`[\p{L}\p{N}]+`)

// findWake looks for the wake word among the first words of what was heard (after "hei", "ok" and such) and returns
// what was said after it. Speech recognition spells a name it does not know in many ways ("Atena", "Adena",
// "Ethina", "A tina"), so near misses count: one or two letters off, or three for a word that starts with a or e and
// ends with a, the way the name sounds. Two words are also tried together.
func findWake(text, wake string) (string, bool) {
	wake = strings.ToLower(wake)
	idx := word.FindAllStringIndex(text, 6)
	for i := 0; i < len(idx) && i < 4; i++ {
		one := strings.ToLower(text[idx[i][0]:idx[i][1]])
		end := idx[i][1]
		ok := near(one, wake)
		if !ok && i+1 < len(idx) {
			two := one + strings.ToLower(text[idx[i+1][0]:idx[i+1][1]])
			if near(two, wake) {
				ok, end = true, idx[i+1][1]
			}
		}
		if ok {
			return strings.TrimLeft(text[end:], " ,.:;!?-–"), true
		}
	}
	return "", false
}

func near(w, wake string) bool {
	d := distance(w, wake)
	if d <= 2 && utf8.RuneCountInString(w) >= 4 {
		return true
	}
	n := utf8.RuneCountInString(w)
	return d <= 3 && n >= 5 && n <= 7 && strings.ContainsRune("aeæä", []rune(w)[0]) && strings.HasSuffix(w, "a")
}

// distance is the Levenshtein distance between two words, in letters.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// Phrases speech recognition makes up from silence and noise (subtitle credits it learned from video).
var madeUp = []string{"teksting av", "undertekster av", "takk for at du så på", "takk for at du så", "thank you for watching",
	"thanks for watching", "subtitles by", "amara.org", "norsk tekst", "tekst:"}

// heardNothing tells text that is not speech: empty, only sounds in brackets ("[Music]", "(musikk)"), or a made-up
// subtitle credit.
func heardNothing(text string) bool {
	t := strings.TrimSpace(regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|\*[^*]*\*`).ReplaceAllString(text, ""))
	if len(word.FindAllString(t, -1)) == 0 {
		return true
	}
	l := strings.ToLower(t)
	for _, m := range madeUp {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

var (
	yesWords  = map[string]bool{"ja": true, "jo": true, "jepp": true, "yes": true, "yeah": true, "yep": true, "ok": true, "okei": true, "greit": true, "gjør": true, "kjør": true, "sure": true, "go": true}
	noWords   = map[string]bool{"nei": true, "no": true, "nope": true, "ikke": true, "stopp": true, "stop": true, "avbryt": true, "cancel": true, "don't": true, "dont": true}
	stopWords = map[string]bool{"stopp": true, "stop": true, "stille": true, "shh": true, "hysj": true, "quiet": true, "enough": true, "nok": true}
)

// yesNo reads an answer to an approval: true for yes, false for no, ok false when it is neither.
func yesNo(text string) (yes, ok bool) {
	for _, w := range word.FindAllString(strings.ToLower(text), 4) {
		if noWords[w] {
			return false, true
		}
		if yesWords[w] {
			return true, true
		}
	}
	return false, false
}

// isStop tells a request to stop talking ("stopp", "stop", "shh").
func isStop(text string) bool {
	ws := word.FindAllString(strings.ToLower(text), -1)
	return len(ws) > 0 && len(ws) <= 3 && stopWords[ws[0]]
}

// norwegian guesses the language of what was said, for the few things live mode says itself.
func norwegian(text string) bool {
	l := strings.ToLower(text)
	no, en := strings.Count(l, "æ")+strings.Count(l, "ø")+strings.Count(l, "å"), 0
	for _, w := range word.FindAllString(l, -1) {
		switch w {
		case "og", "ikke", "jeg", "det", "er", "på", "hva", "hvor", "du", "deg", "kan", "skal", "vil", "hei", "takk", "ja", "nei", "meg", "hvordan", "blir":
			no++
		case "the", "and", "is", "you", "what", "how", "can", "will", "please", "thanks", "yes", "my", "me", "it":
			en++
		}
	}
	return no >= en
}
