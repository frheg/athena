// Shared state: the route, the numbers the navigation and pages show, and the models a chat can use.
import { get, stream } from './api.js'

export const app = $state({
  route: parse(location.hash),
  approvals: 0,
  unread: 0,
  online: true,
  toast: '',
  stats: null, // GET /api/stats
  status: null, // GET /api/status
  models: { models: [], default: '', active: '', switchable: false }, // active '' = the default
})

function parse(hash) {
  const [view = 'chat', id = ''] = hash.replace(/^#\/?/, '').split('/')
  return { view: view || 'chat', id }
}

window.addEventListener('hashchange', () => {
  app.route = parse(location.hash)
})

export function go(view, id = '') {
  location.hash = '#/' + view + (id ? '/' + id : '')
}

let toastTimer
export function toast(msg) {
  app.toast = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (app.toast = ''), 5000)
}

// listeners for owner-wide events (views subscribe to refresh themselves)
const listeners = new Set()
export function onOwnerEvent(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export async function refreshCounts() {
  try {
    const [a, n] = await Promise.all([get('/approvals?status=waiting'), get('/notifications?unread=1')])
    app.approvals = a.approvals.length
    app.unread = n.notifications.length
    app.online = true
  } catch {
    app.online = false
  }
}

export async function refreshStats() {
  try {
    const [s, st] = await Promise.all([get('/stats'), get('/status')])
    app.stats = s
    app.status = st
    app.online = true
  } catch {
    app.online = false
  }
}

export async function refreshModels() {
  try {
    app.models = await get('/models')
  } catch {
    // keep the last list
  }
}

export function startOwnerStream() {
  refreshCounts()
  refreshStats()
  refreshModels()
  const iv = setInterval(refreshStats, 30000)
  const close = stream('/events', (type, data) => {
    refreshCounts()
    if (type === 'notification' && document.hidden && 'Notification' in window && Notification.permission === 'granted') {
      new Notification(data.title, { body: (data.body || '').slice(0, 200) })
    }
    for (const fn of listeners) fn(type, data)
  }, { closeOnEnd: false })
  return () => {
    clearInterval(iv)
    close()
  }
}
