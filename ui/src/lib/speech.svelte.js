// Reads answers aloud: in the browser (this device), or on a speaker connected to Athena, such as one next to a
// Bluetooth speaker (athena-speaker). In the browser, core makes the audio a piece at a time (whole sentences), so
// playing starts after the first piece and the next one loads while one plays.
import { t } from './i18n.js'
import { toast } from './state.svelte.js'
import { get, post } from './api.js'

function stored(key, def) {
  try {
    return localStorage.getItem(key) ?? def
  } catch {
    return def
  }
}
function keep(key, value) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // private window: the choice lasts until the page closes
  }
}

// id: the message being read; auto: read every new answer aloud; target: '' = this device, else a speaker's name.
// auto and target are per device.
export const player = $state({ id: null, part: 0, loading: false, auto: stored('athena.readAloud', '0') === '1',
  target: stored('athena.speaker', ''), speakers: [] })

let audio = null
let next = null
let run = 0
let watch = null

const url = (id, part) => `api/messages/${id}/speech?part=${part}`

export async function refreshSpeakers() {
  try {
    player.speakers = (await get('/speakers')).speakers
  } catch {
    player.speakers = []
  }
  return player.speakers
}

export function setTarget(name) {
  stop()
  player.target = name
  keep('athena.speaker', name)
}

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
  clearInterval(watch)
  if (player.id && player.target) post('/speakers/' + player.target + '/stop').catch(() => {})
  player.id = null
  player.loading = false
}

export function speak(id) {
  stop()
  if (player.target) return speakOn(id, player.target)
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

// speakOn sends the answer to a speaker, then follows what it reports until it is done.
async function speakOn(id, name) {
  const mine = ++run
  player.id = id
  player.loading = true
  try {
    await post('/messages/' + id + '/speak', { to: name })
  } catch (e) {
    if (mine === run) {
      toast(t('Could not play on {s}: {e}', { s: name, e: e.message }))
      player.id = null
      player.loading = false
    }
    return
  }
  let started = false
  watch = setInterval(async () => {
    if (mine !== run) return clearInterval(watch)
    const sp = (await refreshSpeakers()).find((s) => s.name === name)
    if (sp?.playing === id) {
      started = true
      player.loading = false
    } else if (started || !sp) {
      clearInterval(watch)
      if (mine === run) {
        player.id = null
        player.loading = false
      }
    }
  }, 1000)
}

export function toggleAuto() {
  player.auto = !player.auto
  keep('athena.readAloud', player.auto ? '1' : '0')
  if (!player.auto) stop()
}

// volume (0 to 1) and mute of a speaker; the page shows the change at once, the speaker confirms it in its next report
export async function setVolume(name, volume) {
  const sp = player.speakers.find((s) => s.name === name)
  if (sp) sp.volume = volume
  try {
    await post('/speakers/' + name + '/volume', { volume })
  } catch (e) {
    toast(t('Could not change the volume on {s}: {e}', { s: name, e: e.message }))
  }
}

export async function setMuted(name, muted) {
  const sp = player.speakers.find((s) => s.name === name)
  if (sp) sp.muted = muted
  try {
    await post('/speakers/' + name + '/volume', { muted })
  } catch (e) {
    toast(t('Could not change the volume on {s}: {e}', { s: name, e: e.message }))
  }
}
