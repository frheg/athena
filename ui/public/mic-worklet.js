// Runs in the audio thread: hands each block of microphone samples (mono, the context's rate) to the page.
class Tap extends AudioWorkletProcessor {
  process(inputs) {
    const ch = inputs[0] && inputs[0][0]
    if (ch) this.port.postMessage(ch.slice(0))
    return true
  }
}
registerProcessor('tap', Tap)
