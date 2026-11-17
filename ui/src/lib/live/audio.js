// Sound for live mode: the microphone as 20 ms frames of 16 kHz speech, finding speech in them (the same rules as
// the athena-live command, cmd/athena-live/audio.go), WAV for speech recognition, and playing Athena's voice.

export const RATE = 16000 // what speech recognition wants
export const FRAME = RATE / 50 // 20 ms

// level is a frame's loudness in dBFS (0 is full scale, silence about -90)
export function level(f) {
  let s = 0
  for (const x of f) s += x * x
  return 20 * Math.log10(Math.max(Math.sqrt(s / f.length), 1) / 32768)
}

// Mic records from the microphone with the browser's echo cancellation and noise suppression, and calls onframe
// with 20 ms frames (Int16Array, 16 kHz). The audio stays on this device until speech is found in it.
export class Mic {
  constructor(ctx, onframe) {
    this.ctx = ctx
    this.onframe = onframe
    this.buf = new Int16Array(FRAME)
    this.n = 0
    this.pos = 0 // where the next 16 kHz sample starts, in input samples
    this.acc = 0
    this.cnt = 0
  }

  // start opens the microphone: deviceId picks one ('' = the browser's default). It reports the one it got.
  async start(deviceId = '') {
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true,
        ...(deviceId ? { deviceId: { exact: deviceId } } : {}) },
    })
    await this.ctx.audioWorklet.addModule(new URL('mic-worklet.js', document.baseURI)) // a file of its own: the page's rules forbid inline code
    this.src = this.ctx.createMediaStreamSource(this.stream)
    this.node = new AudioWorkletNode(this.ctx, 'tap')
    this.node.port.onmessage = (e) => this.take(e.data)
    this.src.connect(this.node)
    // to the speakers through silence: some browsers only run a node that leads to the output
    this.mute = this.ctx.createGain()
    this.mute.gain.value = 0
    this.node.connect(this.mute).connect(this.ctx.destination)
    this.ratio = this.ctx.sampleRate / RATE
    const track = this.stream.getAudioTracks()[0]
    return { id: track?.getSettings().deviceId || '', label: track?.label || '' }
  }

  // take turns the context's rate into 16 kHz by averaging (a simple low-pass that is enough for speech)
  take(block) {
    for (const x of block) {
      this.acc += x
      this.cnt++
      this.pos++
      if (this.pos >= this.ratio) {
        this.pos -= this.ratio
        const v = this.acc / this.cnt
        this.acc = this.cnt = 0
        this.buf[this.n++] = Math.max(-32768, Math.min(32767, Math.round(v * 32767)))
        if (this.n === FRAME) {
          this.onframe(this.buf)
          this.buf = new Int16Array(FRAME)
          this.n = 0
        }
      }
    }
  }

  stop() {
    if (this.node) this.node.port.onmessage = null
    this.src?.disconnect()
    this.node?.disconnect()
    this.mute?.disconnect()
    for (const t of this.stream?.getTracks() || []) t.stop()
    this.stream = this.src = this.node = this.mute = null
  }
}

// VAD finds stretches of speech by loudness: louder than the background by margin dB for a few frames starts one;
// quiet for the hangover ends it. It keeps a little audio from before the start, so the first sound is not cut.
// The background is the quietest 100 ms of the last 3 seconds, learned all the time (also while it thinks someone
// speaks): speech has short pauses that show the background, and a steady noise (a fan, a hum, a microphone the
// browser turns up) becomes the background within 3 seconds instead of passing for speech.
export class VAD {
  margin = 12 // dB above the background
  hangover = 35 // frames of quiet that end speech (700 ms)
  maxFrames = 750 // longest stretch (15 s)
  minVoiced = 15 // fewer loud frames is a knock or a cough (300 ms)
  floor = -60
  blocks = [] // the loudness of the last 30 blocks of 100 ms
  block = [] // the frames of the block being filled
  loud = 0
  quiet = 0
  voiced = 0
  on = false
  pre = []
  speech = []

  // add takes a frame; it returns a finished stretch of speech (Int16Array) if this frame ended one, else null
  add(f) {
    const db = level(f)
    this.block.push(db)
    if (this.block.length === 5) {
      this.blocks.push(this.block.reduce((a, b) => a + b) / 5)
      if (this.blocks.length > 30) this.blocks.shift()
      this.block = []
      this.floor = Math.max(Math.min(...this.blocks), -75)
    }
    const above = db > this.floor + this.margin
    if (!this.on) {
      this.pre.push(f)
      if (this.pre.length > 15) this.pre.shift() // 300 ms before the start
      this.loud = above ? this.loud + 1 : 0
      if (this.loud >= 3) {
        this.on = true
        this.quiet = 0
        this.voiced = this.loud
        this.speech = this.pre
        this.pre = []
      }
      return null
    }
    this.speech.push(f)
    if (above) {
      this.quiet = 0
      this.voiced++
    } else {
      this.quiet++
    }
    if (this.quiet >= this.hangover || this.speech.length >= this.maxFrames) {
      this.on = false
      this.loud = 0
      const frames = this.speech
      this.speech = []
      if (this.voiced < this.minVoiced) return null
      const out = new Int16Array(frames.length * FRAME)
      frames.forEach((fr, i) => out.set(fr, i * FRAME))
      return out
    }
    return null
  }
}

// wav wraps 16-bit mono samples in a WAV header
export function wav(samples, rate = RATE) {
  const b = new DataView(new ArrayBuffer(44 + samples.length * 2))
  const str = (o, s) => [...s].forEach((c, i) => b.setUint8(o + i, c.charCodeAt(0)))
  str(0, 'RIFF')
  b.setUint32(4, 36 + samples.length * 2, true)
  str(8, 'WAVEfmt ')
  b.setUint32(16, 16, true)
  b.setUint16(20, 1, true) // PCM
  b.setUint16(22, 1, true) // mono
  b.setUint32(24, rate, true)
  b.setUint32(28, rate * 2, true)
  b.setUint16(32, 2, true)
  b.setUint16(34, 16, true)
  str(36, 'data')
  b.setUint32(40, samples.length * 2, true)
  samples.forEach((s, i) => b.setInt16(44 + i * 2, s, true))
  return new Blob([b.buffer], { type: 'audio/wav' })
}

// envelope is the loudness of audio per 50 ms, from 0 to 1, to move the avatar with the voice
export function envelope(buf) {
  const d = buf.getChannelData(0)
  const step = Math.floor(buf.sampleRate / 20)
  const out = []
  for (let i = 0; i + step <= d.length; i += step) {
    let s = 0
    for (let j = i; j < i + step; j++) s += d[j] * d[j]
    out.push(Math.min(1, Math.sqrt(s / step) * 5))
  }
  return out
}
