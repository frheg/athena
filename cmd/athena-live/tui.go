package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// What the avatar shows, from what live mode is doing.
type mood int

const (
	asleep    mood = iota // waiting for the wake word
	listening             // awake: listening without the wake word
	hearing               // someone is speaking
	thinking              // working on an answer
	speaking
	asking // waiting for a yes or no
)

var moodText = map[mood]string{
	asleep:    `waiting for "Athena"`,
	listening: "listening",
	hearing:   "hearing you",
	thinking:  "thinking",
	speaking:  "speaking",
	asking:    "waiting for yes or no",
}

// line is one line of the conversation on screen.
type line struct {
	who  string // "you", "athena", or "" for a note
	text string
}

// view is what the screen shows; render draws it.
type view struct {
	mood    mood
	detail  string    // a short note next to the mood (a tool in use, an error)
	level   float64   // microphone level, dBFS
	mouth   float64   // 0 closed to 1 wide open, while speaking
	happy   time.Time // until when the eyes smile (a thumbs up was seen)
	muted   bool
	lines   []line
	camera  []string // the ASCII picture, nil when there is none
	seen    string   // what the camera sees, in words
	started time.Time
}

const (
	reset  = "\x1b[0m"
	dim    = "\x1b[38;5;245m"
	sage   = "\x1b[38;5;151m"
	amber  = "\x1b[38;5;222m"
	orange = "\x1b[38;5;215m"
	bold   = "\x1b[1m"
	inv    = "\x1b[7m"
)

// owl draws the avatar for the moment t: its eyes blink every few seconds, look around while it thinks, smile after a
// thumbs up; its ear tufts stand up while it listens; its beak opens with its voice.
func owl(v *view, t time.Time) []string {
	since := t.Sub(v.started).Seconds()
	pupil := 1 // 0 left, 1 middle, 2 right
	eye := "O"
	switch {
	case t.Before(v.happy):
		eye = "^"
	case math.Mod(since, 4.3) < 0.15: // a blink
		eye = "-"
	case v.mood == thinking:
		pupil = int(since/0.6) % 4
		if pupil == 3 {
			pupil = 1
		}
	case v.mood == hearing:
		eye = "@"
	case v.mood == asleep && math.Mod(since, 9) > 7:
		eye = "-" // dozes now and then while it waits
	}
	inner := []byte("   ")
	inner[pupil] = eye[0]
	if eye == "^" || eye == "-" {
		inner = []byte{eye[0], eye[0], eye[0]}
	}
	e := string(inner)
	tufts := `  /\              /\  `
	if v.mood == asleep {
		tufts = `  __              __  `
	}
	beakTop, beakBot := `\/`, `  `
	if v.mood == speaking {
		switch {
		case v.mouth > 0.45:
			beakTop, beakBot = `/\`, `\/`
		case v.mood == speaking && v.mouth > 0.15:
			beakTop, beakBot = `\/`, `\/`
		}
	}
	bob := ""
	if v.mood == asleep && math.Mod(since, 3) < 1.5 { // breathes while it waits
		bob = " "
	}
	c := func(color, s string) string { return color + s + sage }
	rows := []string{
		tufts,
		` /  \____________/  \ `,
		`|    ___      ___    |`,
		`|   /   \    /   \   |`,
		`|  | ` + c(amber, e) + ` |  | ` + c(amber, e) + ` |  |`,
		`|   \___/    \___/   |`,
		`|         ` + c(orange, beakTop) + `         |`,
		`|         ` + c(orange, beakBot) + `         |`,
		` \   ^ ^ ^ ^ ^ ^ ^  / `,
		`  \   ^ ^ ^ ^ ^ ^  /  `,
		`   \______________/   `,
		`      ||      ||      `,
	}
	for i := range rows {
		rows[i] = bob + sage + rows[i] + reset
	}
	return rows
}

// width counts what a terminal shows of s: characters, without color codes.
func width(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			esc = true
		case esc && r == 'm':
			esc = false
		case !esc:
			n++
		}
	}
	return n
}

// fit cuts or pads s to exactly w shown characters.
func fit(s string, w int) string {
	if n := width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	var b strings.Builder
	n, esc := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			esc = true
		case esc:
			if r == 'm' {
				esc = false
			}
		default:
			if n == w {
				continue
			}
			n++
		}
		b.WriteRune(r)
	}
	return b.String() + reset
}

// wrap breaks text into lines of at most w characters, at spaces where it can.
func wrap(text string, w int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		cur := ""
		for _, word := range strings.Fields(para) {
			for utf8.RuneCountInString(word) > w {
				r := []rune(word)
				if cur != "" {
					out = append(out, cur)
					cur = ""
				}
				out, word = append(out, string(r[:w])), string(r[w:])
			}
			switch {
			case cur == "":
				cur = word
			case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(word) <= w:
				cur += " " + word
			default:
				out = append(out, cur)
				cur = word
			}
		}
		out = append(out, cur)
	}
	return out
}

// meter draws the microphone level as a bar of n cells (-60 dB empty, -10 dB full).
func meter(db float64, n int) string {
	f := math.Min(1, math.Max(0, (db+60)/50))
	k := int(f * float64(n))
	return strings.Repeat("|", k) + dim + strings.Repeat(".", n-k) + reset
}

// render draws the whole screen, w x h characters.
func render(v *view, t time.Time, w, h int) string {
	var b strings.Builder
	b.WriteString("\x1b[H")
	put := func(s string) { b.WriteString(fit(s, w) + "\r\n") }

	state := moodText[v.mood]
	if v.muted {
		state = "microphone muted"
	}
	head := fmt.Sprintf(" %sATHENA%s  live   %s%s%s", bold, reset, sage, state, reset)
	if v.detail != "" {
		head += dim + "  " + v.detail + reset
	}
	clock := t.Format("15:04")
	put(inv + fit(head, w-len(clock)-1) + inv + clock + " " + reset)
	put("")

	o := owl(v, t)
	camW := 0
	if w >= 90 {
		camW = min(56, w-40)
	}
	camH := camW * 9 / 32
	var cam []string
	if camW > 0 {
		cam = v.camera
		if cam != nil && len(cam) > camH {
			cam = cam[:camH]
		}
	}
	rows := max(len(o)+2, camH+2)
	for i := 0; i < rows; i++ {
		left := ""
		if i < len(o) {
			left = "      " + o[i]
		}
		if i == len(o)+1 {
			left = "      " + sage + "mic " + reset + meter(v.level, 16)
		}
		right := ""
		switch {
		case camW == 0:
		case i == 0:
			right = dim + "camera" + reset
		case i-1 < len(cam):
			right = dim + cam[i-1] + reset
		case i-1 == len(cam) && cam == nil:
			right = dim + "(camera off: camview -web)" + reset
		}
		if camW > 0 {
			put(fit(left, w-camW-2) + right)
		} else {
			put(left)
		}
	}
	if v.seen != "" {
		put("      " + dim + "seen: " + reset + v.seen)
	} else {
		put("")
	}
	put(dim + strings.Repeat("-", w) + reset)

	// the conversation: as many of the newest lines as fit
	room := h - strings.Count(b.String(), "\r\n") - 1
	var conv []string
	for i := len(v.lines) - 1; i >= 0 && len(conv) < room; i-- {
		l := v.lines[i]
		label, color := "         ", dim
		switch l.who {
		case "you":
			label, color = "  you  > ", amber
		case "athena":
			label, color = " athena > ", sage
		}
		parts := wrap(l.text, max(10, w-len(label)-1))
		for j := len(parts) - 1; j >= 0 && len(conv) < room; j-- {
			lab := strings.Repeat(" ", len(label))
			if j == 0 {
				lab = color + label + reset
			}
			conv = append(conv, lab+parts[j])
		}
	}
	for i := 0; i < room; i++ {
		if j := room - 1 - i; j < len(conv) {
			put(conv[j])
		} else {
			put("")
		}
	}
	// the last row ends without a line break, or the terminal would scroll
	b.WriteString(fit(dim+" space talk   s stop speaking   m mute mic   q quit"+reset, w))
	return b.String()
}
