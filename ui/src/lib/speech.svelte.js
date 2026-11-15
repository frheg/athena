// Reads answers aloud in the browser. Core makes the audio a piece at a time (whole sentences), so playing starts
// after the first piece; the next one loads while the current one plays. Over Bluetooth, this plays wherever the
// phone or laptop sends its sound.
import { t } from './i18n.js'
import { toast } from './state.svelte.js'

function stored() {
  try {
    return localStorage.getItem('athena.readAloud') === '1'
  } catch {
    return false
  }
}

// id: the message being read; auto: read every new answer aloud (per device)
export const player = $state({ id: null, part: 0, loading: false, auto: stored() })

let audio = null
let next = null
let run = 0

const url = (id, part) => `api/messages/${id}/speech?part=${part}`

export function stop() {
  run++
  for (const el of [audio, next]) {
    if (el) {
      el.onended = el.onerror = el.onplaying = null
      el.pause()
      el.removeAttribute('src')
    }
  }
  audio = next = null
  player.id = null
  player.loading = false
}

export function speak(id) {
  stop()
  const mine = ++run
  player.id = id
  player.loading = true
  const play = (part, el) => {
    if (mine !== run) return
    audio = el || new Audio(url(id, part))
    player.part = part
    // the next piece loads while this one plays; a 404 there means this is the last one
    let last = false
    next = new Audio(url(id, part + 1))
    next.preload = 'auto'
    next.onerror = () => (last = true)
    const cur = audio
    cur.onplaying = () => (player.loading = false)
    cur.onended = () => {
      if (mine !== run) return
      if (last) stop()
      else play(part + 1, next)
    }
    cur.onerror = () => {
      if (mine !== run) return
      if (part === 0) toast(t('Could not read this aloud: the speech service did not answer.'))
      stop()
    }
    cur.play().catch((e) => {
      if (mine !== run) return
      // browsers only play sound after you have touched the page
      if (e.name === 'NotAllowedError') toast(t('The browser blocked sound. Press play on the answer to hear it.'))
      stop()
    })
  }
  play(0)
}

export function toggleAuto() {
  player.auto = !player.auto
  try {
    localStorage.setItem('athena.readAloud', player.auto ? '1' : '0')
  } catch {
    // private window: the choice lasts until the page closes
  }
  if (!player.auto) stop()
}
