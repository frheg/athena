import { t } from './i18n.js'

export function when(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  const now = new Date()
  if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const days = (now - d) / 86400000
  if (days < 6) return d.toLocaleDateString([], { weekday: 'short' }) + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' })
}

export function full(iso) {
  return iso ? new Date(iso).toLocaleString() : ''
}

// "3 min ago", "in 2 h"
export function relative(iso) {
  if (!iso) return ''
  const s = (new Date(iso) - new Date()) / 1000
  const a = Math.abs(s)
  const v = a < 60 ? [Math.round(a), 's'] : a < 3600 ? [Math.round(a / 60), 'min'] : a < 86400 ? [Math.round(a / 3600), 'h'] : [Math.round(a / 86400), 'd']
  return s < 0 ? `${v[0]} ${v[1]} ago` : `in ${v[0]} ${v[1]}`
}

// 1234 -> 1.2k, 2500000 -> 2.5M
export function num(n) {
  if (n == null || isNaN(n)) return '0'
  const a = Math.abs(n)
  if (a >= 1e6) return (n / 1e6).toFixed(a >= 1e7 ? 0 : 1) + 'M'
  if (a >= 1e4) return Math.round(n / 1e3) + 'k'
  if (a >= 1e3) return (n / 1e3).toFixed(1) + 'k'
  return String(Math.round(n))
}

// seconds -> "850 ms", "4.2 s", "3 min 10 s"
export function dur(sec) {
  if (sec == null || isNaN(sec)) return '-'
  if (sec < 1) return Math.round(sec * 1000) + ' ms'
  if (sec < 60) return sec.toFixed(sec < 10 ? 1 : 0) + ' s'
  return Math.floor(sec / 60) + ' min ' + Math.round(sec % 60) + ' s'
}

export function args(a) {
  if (!a || (typeof a === 'object' && Object.keys(a).length === 0)) return ''
  try {
    return JSON.stringify(a, null, 2)
  } catch {
    return String(a)
  }
}

// web__fetch -> web / fetch
export function toolName(n) {
  return (n || '').replace('__', ' / ').replaceAll('_', ' ')
}

export const classHelp = {
  read: t('Looks something up. Nothing changes.'),
  write: t('A change inside Athena that is easy to undo.'),
  act: t('Does something outside Athena, for example on a server.'),
  communicate: t('Sends something out of Athena, for example a mail.'),
  remember: t('Stores a fact about you. You can see and edit it under Memory.'),
}

// 2048 -> "2 KB", 3500000 -> "3.3 MB"
export function bytes(n) {
  if (!n) return '0 B'
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(n >= 10 * (1 << 20) ? 0 : 1) + ' MB'
  if (n >= 1 << 10) return Math.round(n / (1 << 10)) + ' KB'
  return n + ' B'
}
