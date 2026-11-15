// Talks to core. Sign-in is done by the gate in front (the browser sends it), so there is no token here.
// Paths are relative, so the UI works under any prefix (/ standalone, /athena/ behind a shared proxy).

export class ApiError extends Error {
  constructor(status, message) {
    super(message)
    this.status = status
  }
}

export async function api(method, path, body) {
  const opts = { method, headers: {} }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  let res
  try {
    res = await fetch('api' + path, opts)
  } catch (e) {
    throw new ApiError(0, 'Athena does not answer (network): ' + e.message)
  }
  let data = null
  try {
    data = await res.json()
  } catch {
    // empty or not JSON
  }
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || res.statusText)
  return data
}

export const get = (p) => api('GET', p)
export const post = (p, b = {}) => api('POST', p, b)
export const patch = (p, b) => api('PATCH', p, b)
export const del = (p) => api('DELETE', p)
export const put = (p, b) => api('PUT', p, b)

const TURN_EVENTS = ['status', 'text', 'text_reset', 'waiting_model', 'recalled', 'tool_call', 'tool_result',
  'approval', 'approval_answer', 'tainted', 'done', 'error']
const OWNER_EVENTS = ['approval', 'approval_closed', 'notification']

// stream opens an event stream and calls on(type, data, id) for every event. The browser reconnects by itself and
// sends Last-Event-ID, so nothing is missed. A turn's stream is closed after done or error.
export function stream(path, on, { closeOnEnd = true } = {}) {
  const es = new EventSource('api' + path)
  const types = closeOnEnd ? TURN_EVENTS : OWNER_EVENTS
  for (const type of types) {
    es.addEventListener(type, (ev) => {
      let data = null
      try {
        data = JSON.parse(ev.data)
      } catch {
        data = ev.data
      }
      on(type, data, Number(ev.lastEventId))
      if (closeOnEnd && (type === 'done' || type === 'error')) es.close()
    })
  }
  return () => es.close()
}

// upload sends one file to the file store and reports progress (0 to 1). It answers with what the store read from
// the file. XMLHttpRequest, because fetch cannot report upload progress.
export function upload(file, onprogress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', 'api/files?name=' + encodeURIComponent(file.name || 'pasted'))
    xhr.upload.onprogress = (e) => e.lengthComputable && onprogress && onprogress(e.loaded / e.total)
    xhr.onload = () => {
      let data = null
      try {
        data = JSON.parse(xhr.responseText)
      } catch {
        // not JSON
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data)
      else reject(new ApiError(xhr.status, (data && data.error) || xhr.statusText || 'upload failed'))
    }
    xhr.onerror = () => reject(new ApiError(0, 'Athena does not answer (network)'))
    xhr.send(file)
  })
}
