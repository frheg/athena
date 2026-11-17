// The camera for live mode. The browser only takes small pictures and sends them to Athena a few times a second;
// the pose and gesture models run on the server (the pose service), and the pictures are not kept there. What comes
// back is points and names; a few words of it go with what you say ("a person is in view; left hand: thumbs up").

const WIDTH = 320 // enough for one person in front of the camera, and small to send (about 15 kB)
const GAP = 120 // ms at least between pictures; each waits for the last answer, so a slow link sends fewer

export class Camera {
  constructor({ ongesture, onerror } = {}) {
    this.ongesture = ongesture || (() => {})
    this.onerror = onerror || (() => {})
    this.seen = null
    this.seenAt = 0
    this.gesture = ''
    this.since = 0
    this.reported = ''
    this.running = false
    this.failures = 0
  }

  async start() {
    this.stream = await navigator.mediaDevices.getUserMedia({ video: { width: 640, height: 480, frameRate: 15 } })
    this.video = document.createElement('video')
    this.video.muted = true
    this.video.playsInline = true
    this.video.srcObject = this.stream
    await this.video.play()
    this.canvas = document.createElement('canvas')
    this.running = true
    this.loop()
  }

  async loop() {
    while (this.running) {
      const t0 = performance.now()
      try {
        await this.send()
        this.failures = 0
      } catch (e) {
        if (++this.failures === 3) this.onerror(e.message)
        await new Promise((ok) => setTimeout(ok, Math.min(5000, 500 * this.failures)))
      }
      await new Promise((ok) => setTimeout(ok, Math.max(0, GAP - (performance.now() - t0))))
    }
  }

  async send() {
    const v = this.video
    if (!v || v.readyState < 2 || !v.videoWidth) return
    this.canvas.width = WIDTH
    this.canvas.height = Math.round((v.videoHeight * WIDTH) / v.videoWidth)
    this.canvas.getContext('2d').drawImage(v, 0, 0, this.canvas.width, this.canvas.height)
    const jpeg = await new Promise((ok) => this.canvas.toBlob(ok, 'image/jpeg', 0.7))
    if (!jpeg || !this.running) return
    const res = await fetch('api/camera', { method: 'POST', headers: { 'Content-Type': 'image/jpeg' }, body: jpeg })
    const d = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(d.error || res.statusText)
    if (this.running) this.take(d)
  }

  // take keeps what was seen and reports a gesture once it has been held for 600 ms
  take(a) {
    const now = performance.now()
    const h = (a.hands || []).find((h) => h.gesture && h.score >= 0.6)
    const g = h ? h.side + ' hand: ' + h.gesture : ''
    this.seen = { pose: a.pose, poses: a.poses || [], hands: a.hands || [] }
    this.seenAt = now
    if (g !== this.gesture) {
      this.gesture = g
      this.since = now
    }
    if (!g) this.reported = ''
    if (g && now - this.since >= 600 && g !== this.reported) {
      this.reported = g
      this.ongesture(g)
    }
  }

  // summary says in words what the camera sees now ('' when it has nothing recent)
  summary() {
    if (!this.seen || performance.now() - this.seenAt > 2000) return ''
    const s = this.seen
    if (!s.pose && !s.hands.length) return 'nobody is in view'
    const parts = [...s.poses, ...s.hands.filter((h) => h.gesture && h.score >= 0.6).map((h) => h.side + ' hand: ' + h.gesture)].sort()
    if (s.pose) parts.unshift('a person is in view')
    return parts.join('; ')
  }

  // recent returns what was seen in the last second, for drawing
  recent() {
    return this.seen && performance.now() - this.seenAt < 1000 ? this.seen : null
  }

  stop() {
    this.running = false
    for (const t of this.stream?.getTracks() || []) t.stop()
    if (this.video) this.video.srcObject = null
    this.stream = null
    this.seen = null
  }
}
