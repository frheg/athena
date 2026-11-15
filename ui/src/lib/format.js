import { t } from './i18n.js'

export function when(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  const now = new Date()
  const sameDay = d.toDateString() === now.toDateString()
  if (sameDay) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const days = (now - d) / 86400000
  if (days < 6) return d.toLocaleDateString([], { weekday: 'short' }) + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' })
}

export function full(iso) {
  return iso ? new Date(iso).toLocaleString() : ''
}

export function args(a) {
  if (!a || (typeof a === 'object' && Object.keys(a).length === 0)) return ''
  try {
    return JSON.stringify(a, null, 2)
  } catch {
    return String(a)
  }
}

// a tool name as people read it: web__fetch -> web · fetch
export function toolName(n) {
  return (n || '').replace('__', ' · ').replaceAll('_', ' ')
}

export const classHelp = {
  read: t('Looks something up. Nothing changes.'),
  write: t('A change inside Athena that is easy to undo.'),
  act: t('Does something outside Athena, for example on a server.'),
  communicate: t('Sends something out of Athena, for example a mail.'),
  remember: t('Stores a fact about you. You can see and edit it under Memory.'),
}
