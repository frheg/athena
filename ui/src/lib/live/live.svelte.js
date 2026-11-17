// Live mode in the browser: talk with Athena out loud. It listens for its name, answers with its voice, and sees you
// through the camera (pose and gestures, worked out by the pose service). The same behaviour as the athena-live command:
// say "Athena, ..." to ask; for 15 seconds after an answer no name is needed. While it speaks, "Athena" or "stopp"
// (or an open palm) stops it. When Athena asks to do something, say yes or no (or thumbs up or down).
//
// Speech without the wake word, outside a conversation, is written down by Athena's speech service and dropped
// right here; it is not kept. Everything said to Athena is a normal message in the conversation.
import { post, stream, ApiError } from '../api.js'
import { t } from '../i18n.js'
import { Mic, VAD, wav, envelope, level, RATE } from './audio.js'
import { Camera } from './camera.js'
import { findWake, heardNothing, yesNo, isStop, norwegian, sameWords, Sentences } from './words.js'

const WAKE = 'Athena'
const FOLLOW = 15000 // ms after an answer it listens without the wake word
const ECHO_TAIL = 600 // ms after playing the microphone can still hear it (speaker delay, the room)

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
    // private window
  }
}

// mood: asleep, listening, hearing, thinking, speaking, asking
export const live = $state({
  on: false, starting: false, session: '', mood: 'asleep', level: -90, mouth: 0, happy: false,
  muted: false, cameraAvailable: false, cameraOn: stored('athena.liveCamera', '1') === '1', cameraReady: false, preview: stored('athena.livePreview', '1') === '1',
  seen: '', heard: '', other: '', note: '', approval: null, turn: '',
  mic: stored('athena.liveMic', ''), micLabel: '', mics: [], // the microphone chosen ('' = the browser's default), and those there are
  soundBlocked: false, // the browser holds sound back until the page is touched
})

let ctx = null
let mic = null
let vad = null
let camera = null
let voice = null
let hooks = {}
let timer = null
let closeTurn = null
let awakeUntil = 0
let happyUntil = 0
let hearing = false
let transcribing = 0
let saidNorwegian = true
const sent = new Sentences()

export const getCamera = () => camera

// Voice speaks sentences in order: the next one's audio is made while the current one plays, so there is no pause
// between them. stop drops everything at once.
class Voice {
  queue = []
  ready = []
  gen = 0
  fetching = false
  playing = null
  env = null
  since = 0
  said = [] // {text, from, to}: the last minute of what it said; to is 0 while it plays
  done = []

  say(text) {
    this.queue.push(text)
    this.pump()
  }

  async pump() {
    if (this.fetching) return
    while (this.queue.length) {
      const text = this.queue.shift()
      const gen = this.gen
      this.fetching = true
      try {
        const res = await fetch('api/speech', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ text }) })
        if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || res.statusText)
        const buf = await ctx.decodeAudioData(await res.arrayBuffer())
        if (gen === this.gen) {
          this.ready.push({ buf, text, gen })
          this.play()
        }
      } catch (e) {
        console.warn('live: could not speak', e)
        if (gen === this.gen) live.note = t('Could not speak: {e}', { e: e.message })
      } finally {
        this.fetching = false
      }
    }
    this.finished()
  }

  play() {
    if (this.playing || !this.ready.length || !ctx) return
    const c = this.ready.shift()
    if (c.gen !== this.gen) return this.play()
    const src = ctx.createBufferSource()
    src.buffer = c.buf
    src.connect(ctx.destination)
    const rec = { text: c.text, from: performance.now(), to: 0 }
    this.said.push(rec)
    this.said = this.said.filter((s) => !s.to || performance.now() - s.to < 60000)
    this.playing = src
    this.env = envelope(c.buf)
    this.since = rec.from
    src.onended = () => {
      if (this.playing !== src) return
      this.playing = null
      rec.to = performance.now() + ECHO_TAIL
      this.play()
      this.finished()
    }
    src.start()
  }

  stop() {
    this.gen++
    this.queue = []
    this.ready = []
    const src = this.playing
    this.playing = null
    if (src) {
      src.onended = null
      try {
        src.stop()
      } catch {
        // already stopped
      }
      const last = this.said[this.said.length - 1]
      if (last && !last.to) last.to = performance.now() + ECHO_TAIL
    }
    this.finished()
  }

  busy() {
    return this.queue.length > 0 || this.fetching || this.ready.length > 0 || !!this.playing
  }

  mouth() {
    if (!this.playing || !this.env) return 0
    return this.env[Math.floor((performance.now() - this.since) / 50)] || 0
  }

  whenDone(f) {
    if (!this.busy()) f()
    else this.done.push(f)
  }

  finished() {
    if (this.busy()) return
    const fs = this.done
    this.done = []
    for (const f of fs) f()
  }

  // echo tells whether speech heard from..to overlaps what it said itself, and what it said then
  echo(from, to) {
    let overlaps = false
    let spoken = ''
    for (const s of this.said) {
      const end = s.to || performance.now() + ECHO_TAIL
      if (s.from < to && end > from) {
        overlaps = true
        spoken += ' ' + s.text
      }
    }
    return { overlaps, spoken }
  }
}

// start begins live mode in a conversation. hooks.onturn(turnId) is called when a spoken message starts an answer,
// so the page can show it.
export async function start(session, h = {}) {
  if (live.on || live.starting) return
  if (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia) {
    throw new Error(t('Live mode needs the microphone, which browsers only allow on https:// pages. Open Athena over HTTPS.'))
  }
  live.starting = true
  hooks = h
  try {
    ctx = new AudioContext()
    ctx.resume().catch(() => {}) // not awaited: a browser that holds sound back never settles it until the page is touched
    voice = new Voice()
    await openMic(live.mic)
    watchSound()
    live.session = session
    live.on = true
    live.note = t('Say "{w}, ..." to talk. For 15 seconds after an answer, no name is needed.', { w: WAKE })
    timer = setInterval(tick, 1000 / 15)
    live.cameraAvailable = h.camera !== false
    if (live.cameraOn && live.cameraAvailable) startCamera()
  } catch (e) {
    stop()
    throw e
  } finally {
    live.starting = false
  }
}

export function stop() {
  clearInterval(timer)
  voice?.stop()
  if (closeTurn) closeTurn()
  closeTurn = null
  mic?.stop()
  camera?.stop()
  ctx?.close().catch(() => {})
  ctx = mic = vad = camera = voice = null
  sent.reset()
  Object.assign(live, { on: false, session: '', mood: 'asleep', level: -90, mouth: 0, seen: '', heard: '', other: '', note: '', approval: null, turn: '', cameraReady: false, soundBlocked: false })
}

// openMic opens a microphone (a fresh VAD, so it learns the new one's background); one that is gone falls back to
// the default
async function openMic(id) {
  mic?.stop()
  vad = new VAD()
  mic = new Mic(ctx, frame)
  let got
  try {
    got = await mic.start(id)
  } catch (e) {
    if (!id || (e.name !== 'OverconstrainedError' && e.name !== 'NotFoundError')) throw e
    mic = new Mic(ctx, frame)
    got = await mic.start('')
  }
  live.micLabel = got.label
  // the names of the microphones are only shown once one may be used
  const devices = await navigator.mediaDevices.enumerateDevices().catch(() => [])
  live.mics = devices.filter((d) => d.kind === 'audioinput' && d.deviceId).map((d) => ({ id: d.deviceId, label: d.label || t('Microphone') }))
}

export async function setMic(id) {
  live.mic = id
  keep('athena.liveMic', id)
  if (!live.on) return
  try {
    await openMic(id)
  } catch (e) {
    live.note = t('Could not open that microphone: {e}', { e: e.message })
  }
}

// watchSound notices a browser that holds sound back (no microphone, no voice) and starts it on the next touch
function watchSound() {
  const check = () => {
    if (!ctx) return
    live.soundBlocked = ctx.state !== 'running'
    if (live.soundBlocked) {
      const go = () => ctx?.resume().then(check).catch(() => {})
      document.addEventListener('pointerdown', go, { once: true })
      document.addEventListener('keydown', go, { once: true })
    }
  }
  setTimeout(check, 500)
  ctx.onstatechange = check
}

async function startCamera() {
  const cam = new Camera({
    ongesture: gesture,
    onerror: (e) => (live.note = t('The camera analysis failed: {e}', { e })),
  })
  try {
    await cam.start()
    if (!live.on) return cam.stop()
    camera = cam
    live.cameraReady = true
  } catch (e) {
    cam.stop()
    live.note = t('No camera: {e}', { e: e.message })
  }
}

export function setCamera(on) {
  live.cameraOn = on
  keep('athena.liveCamera', on ? '1' : '0')
  if (!live.on) return
  if (on && !camera && live.cameraAvailable) startCamera()
  if (!on && camera) {
    camera.stop()
    camera = null
    live.cameraReady = false
    live.seen = ''
  }
}

export function setPreview(on) {
  live.preview = on
  keep('athena.livePreview', on ? '1' : '0')
}

export function setMuted(on) {
  live.muted = on
}

// talk listens without the wake word for a while, like after an answer
export function talk() {
  awakeUntil = performance.now() + FOLLOW
}

// tick works out the avatar's mood from what happens now
function tick() {
  if (!live.on) return
  const now = performance.now()
  const talking = voice.busy()
  live.mood = live.approval ? 'asking' : talking ? 'speaking' : live.turn || transcribing > 0 ? 'thinking'
    : hearing ? 'hearing' : now < awakeUntil ? 'listening' : 'asleep'
  live.mouth = voice.mouth()
  live.happy = now < happyUntil
  live.seen = camera ? camera.summary() : ''
}

// frame takes 20 ms of microphone sound and finds speech in it; each stretch is written down, at most two waiting
function frame(f) {
  if (!live.on) return
  live.level = level(f)
  if (live.muted) return
  const s = vad.add(f)
  hearing = vad.on
  if (!s) return
  const to = performance.now()
  const from = to - (s.length / RATE) * 1000
  if (transcribing >= 2) return // still writing down the last two: this one is lost
  transcribing++
  fetch('api/transcribe?prompt=' + encodeURIComponent(WAKE + '.'), { method: 'POST', headers: { 'Content-Type': 'audio/wav' }, body: wav(s) })
    .then(async (res) => {
      const d = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(d.error || res.statusText)
      if (live.on) heard(d.text || '', from, to)
    })
    .catch((e) => {
      console.warn('live: speech recognition failed', e)
      if (live.on) live.note = t('Speech recognition failed: {e}', { e: e.message })
    })
    .finally(() => transcribing--)
}

// heard decides what to do with what was said: a wake word, a follow-up, an answer to an approval, a request to stop,
// or nothing. Without the wake word and outside a conversation, the text is dropped right here.
function heard(text, from, to) {
  if (heardNothing(text)) return
  const rest = findWake(text, WAKE)
  const woke = rest !== null
  // The microphone also hears Athena. Speech heard while it spoke that is mostly its own words is its own voice,
  // dropped; unless it starts with the wake word and Athena did not say that word itself.
  const { overlaps, spoken } = voice.echo(from, to)
  if (overlaps && sameWords(text, spoken)) {
    const saidWake = spoken.toLowerCase().includes(WAKE.toLowerCase())
    if (!woke || saidWake) return
  }
  const busy = voice.busy() || !!live.turn || overlaps
  const awake = performance.now() < awakeUntil
  if (busy && !live.approval) {
    // while it speaks it hears itself too: only its name or "stopp" count
    if (!woke && !isStop(text)) return
    stopAll()
    if (woke && rest && !isStop(rest)) ask(rest)
    else talk()
  } else if (live.approval) {
    const yes = yesNo((rest || '') + ' ' + text)
    if (yes !== null) answer(yes)
    else if (woke && rest) ask(rest) // something else: a new message makes Athena drop what waits
  } else if (woke && !rest.trim()) {
    talk()
    voice.say(saidNorwegian ? 'Ja?' : 'Yes?')
  } else if (woke) {
    ask(rest)
  } else if (awake) {
    ask(text)
  } else {
    showOther(text) // not for Athena: shown here for a moment, then gone
  }
}

// ask sends what was said to Athena as a spoken message, with what the camera sees, and speaks the answer
async function ask(text) {
  const seen = camera ? camera.summary() : ''
  saidNorwegian = norwegian(text)
  awakeUntil = 0
  live.heard = text
  live.note = ''
  const body = { text, voice: true, seen }
  const path = '/sessions/' + live.session + '/messages'
  let r
  try {
    try {
      r = await post(path, body)
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 409) throw e
      stopAll() // an answer is still running: stop it and ask again
      await new Promise((ok) => setTimeout(ok, 300))
      r = await post(path, body)
    }
  } catch (e) {
    live.note = t('Could not ask Athena: {e}', { e: e.message })
    return
  }
  if (!live.on) return
  live.turn = r.turn_id
  sent.reset()
  hooks.onturn?.(r.turn_id)
  followTurn(r.turn_id)
}

// followTurn reads the answer as it streams in and speaks it sentence by sentence
function followTurn(turn) {
  if (closeTurn) closeTurn()
  const end = () => {
    if (live.turn !== turn) return
    live.turn = ''
    closeTurn = null
    const rest = sent.flush()
    if (rest) voice.say(rest)
    voice.whenDone(talk)
  }
  closeTurn = stream('/turns/' + turn + '/events', (type, d) => {
    if (live.turn !== turn) return
    switch (type) {
      case 'text':
        for (const s of sent.add(d.delta)) voice.say(s)
        break
      case 'text_reset': // the text before a tool call is not the answer
        sent.reset()
        break
      case 'tool_call':
        live.note = t('using {tool}', { tool: (d.tool || '').replaceAll('__', ' ') })
        break
      case 'approval': {
        const name = (d.tool || '').replaceAll('__', ' ').replaceAll('_', ' ')
        live.approval = { id: d.id, tool: name }
        voice.say(saidNorwegian ? `Jeg vil bruke ${name}. Er det greit? Si ja eller nei.` : `I want to use ${name}. Is that all right? Say yes or no.`)
        break
      }
      case 'approval_answer':
        live.approval = null
        break
      case 'done':
        end()
        break
      case 'error':
        if (d?.error && d.error !== 'cancelled' && !d.error.startsWith('superseded')) live.note = t('The answer failed: {e}', { e: d.error })
        end()
        break
    }
  })
}

// answer answers the approval that waits
async function answer(yes) {
  const a = live.approval
  live.approval = null
  if (!a) return
  live.note = yes ? t('You answered yes') : t('You answered no')
  try {
    await post('/approvals/' + a.id, { answer: yes ? 'once' : 'deny' })
  } catch (e) {
    live.note = t('Could not answer: {e}', { e: e.message })
  }
}

// stopAll stops speaking and the running answer
export function stopAll() {
  voice?.stop()
  const turn = live.turn
  live.turn = ''
  if (closeTurn) closeTurn()
  closeTurn = null
  sent.reset()
  if (turn) post('/turns/' + turn + '/cancel').catch(() => {})
}

// gesture acts on a gesture held steady: an open palm stops speaking, thumbs up or down answers an approval, and a
// thumbs up otherwise makes the avatar smile
function gesture(g) {
  const busy = voice?.busy() || !!live.turn
  if (g.endsWith('open palm') && busy) {
    live.note = t('Open palm: stopped')
    stopAll()
    talk()
  } else if (g.endsWith('thumbs up') && live.approval) {
    answer(true)
  } else if (g.endsWith('thumbs down') && live.approval) {
    answer(false)
  } else if (g.endsWith('thumbs up')) {
    happyUntil = performance.now() + 3000
  }
}

// showOther shows speech that was not for Athena (no name, not in a conversation), so you can tell it hears you;
// it is not sent anywhere
let otherTimer
function showOther(text) {
  live.other = text
  clearTimeout(otherTimer)
  otherTimer = setTimeout(() => (live.other = ''), 6000)
}
