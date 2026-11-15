// Shared state: the route, and counts the navigation shows (waiting approvals, unread notifications).
import { get, stream } from './api.js'

export const app = $state({
  route: parse(location.hash),
  approvals: 0,
  unread: 0,
  online: true,
  toast: '',
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

export function startOwnerStream() {
  refreshCounts()
  return stream('/events', (type, data) => {
    refreshCounts()
    if (type === 'notification' && document.hidden && 'Notification' in window && Notification.permission === 'granted') {
      new Notification(data.title, { body: (data.body || '').slice(0, 200) })
    }
    for (const fn of listeners) fn(type, data)
  }, { closeOnEnd: false })
}
