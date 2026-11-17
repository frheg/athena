// What live mode makes of the words it hears: the wake word, yes or no, stop, and speech recognition's mistakes.
// The same rules as the athena-live command (cmd/athena-live/words.go).

const word = /[\p{L}\p{N}]+/gu

export const words = (text) => text.match(word) || []

// findWake looks for the wake word among the first words of what was heard (after "hei", "ok" and such) and returns
// what was said after it, or null. Speech recognition spells a name it does not know in many ways ("Atena", "Adena",
// "Ethina", "A tina"), so near misses count; two words are also tried together.
export function findWake(text, wake) {
  wake = wake.toLowerCase()
  const found = [...text.matchAll(word)].slice(0, 6)
  for (let i = 0; i < found.length && i < 4; i++) {
    const one = found[i][0].toLowerCase()
    let end = found[i].index + found[i][0].length
    let ok = near(one, wake)
    if (!ok && i + 1 < found.length) {
      const two = one + found[i + 1][0].toLowerCase()
      if (near(two, wake)) {
        ok = true
        end = found[i + 1].index + found[i + 1][0].length
      }
    }
    if (ok) return text.slice(end).replace(/^[\s,.:;!?\-–]+/, '')
  }
  return null
}

// one or two letters off, or three for a word that starts with a or e and ends with a, the way the name sounds
export function near(w, wake) {
  const n = [...w].length
  const d = distance(w, wake)
  if (d <= 2 && n >= 4) return true
  return d <= 3 && n >= 5 && n <= 7 && 'aeæä'.includes([...w][0]) && w.endsWith('a')
}

// the Levenshtein distance between two words, in letters
export function distance(a, b) {
  const ra = [...a]
  const rb = [...b]
  let prev = Array.from({ length: rb.length + 1 }, (_, j) => j)
  let cur = new Array(rb.length + 1)
  for (let i = 1; i <= ra.length; i++) {
    cur[0] = i
    for (let j = 1; j <= rb.length; j++) {
      cur[j] = Math.min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (ra[i - 1] === rb[j - 1] ? 0 : 1))
    }
    ;[prev, cur] = [cur, prev]
  }
  return prev[rb.length]
}

// phrases speech recognition makes up from silence and noise (subtitle credits it learned from video)
const madeUp = ['teksting av', 'undertekster av', 'takk for at du så på', 'takk for at du så', 'thank you for watching',
  'thanks for watching', 'subtitles by', 'amara.org', 'norsk tekst', 'tekst:']

// heardNothing tells text that is not speech: empty, only sounds in brackets ("[Music]"), or a made-up credit
export function heardNothing(text) {
  const t = text.replace(/\[[^\]]*\]|\([^)]*\)|\*[^*]*\*/g, '').trim()
  if (!words(t).length) return true
  const l = t.toLowerCase()
  return madeUp.some((m) => l.includes(m))
}

const yesWords = new Set(['ja', 'jo', 'jepp', 'yes', 'yeah', 'yep', 'ok', 'okei', 'greit', 'gjør', 'kjør', 'sure', 'go'])
const noWords = new Set(['nei', 'no', 'nope', 'ikke', 'stopp', 'stop', 'avbryt', 'cancel', 'don', 'dont'])
const stopWords = new Set(['stopp', 'stop', 'stille', 'shh', 'hysj', 'quiet', 'enough', 'nok'])

// yesNo reads an answer to an approval: true for yes, false for no, null for neither
export function yesNo(text) {
  for (const w of words(text.toLowerCase()).slice(0, 4)) {
    if (noWords.has(w)) return false
    if (yesWords.has(w)) return true
  }
  return null
}

// isStop tells a request to stop talking ("stopp", "stop", "shh")
export function isStop(text) {
  const ws = words(text.toLowerCase())
  return ws.length > 0 && ws.length <= 3 && stopWords.has(ws[0])
}

const noHints = new Set(['og', 'ikke', 'jeg', 'det', 'er', 'på', 'hva', 'hvor', 'du', 'deg', 'kan', 'skal', 'vil', 'hei', 'takk',
  'ja', 'nei', 'meg', 'hvordan', 'blir'])
const enHints = new Set(['the', 'and', 'is', 'you', 'what', 'how', 'can', 'will', 'please', 'thanks', 'yes', 'my', 'me', 'it'])

// norwegian guesses the language of what was said, for the few things live mode says itself
export function norwegian(text) {
  const l = text.toLowerCase()
  let no = (l.match(/[æøå]/g) || []).length
  let en = 0
  for (const w of words(l)) {
    if (noHints.has(w)) no++
    else if (enHints.has(w)) en++
  }
  return no >= en
}

// sameWords tells whether most of the words heard (at least half, and at least two) are among the words said
export function sameWords(heard, said) {
  const h = words(heard.toLowerCase())
  if (h.length < 2) return false
  const s = new Set(words(said.toLowerCase()))
  return h.filter((w) => s.has(w)).length * 2 >= h.length
}

// Sentences cuts streaming text into whole sentences to speak. A sentence ends at . ! ? or : followed by a space,
// or at a line break; short ones wait for the next, except the first, so speech starts early.
export class Sentences {
  buf = ''
  spoken = 0

  add(text) {
    this.buf += text
    const all = this.buf
    const out = []
    let start = 0
    for (let i = 0; i < all.length; i++) {
      const c = all[i]
      let end = -1
      if (c === '\n') end = i
      else if ('.!?:'.includes(c) && i + 1 < all.length && (all[i + 1] === ' ' || all[i + 1] === '\n')) end = i + 1
      if (end < 0) continue
      const piece = all.slice(start, end).trim()
      if (!piece) {
        start = end
        continue
      }
      if (piece.length < 12 && this.spoken + out.length > 0) continue // too short alone: let it grow
      out.push(piece)
      start = end
    }
    this.buf = all.slice(start)
    this.spoken += out.length
    return out
  }

  flush() {
    const rest = this.buf.trim()
    this.reset()
    return rest
  }

  reset() {
    this.buf = ''
    this.spoken = 0
  }
}
