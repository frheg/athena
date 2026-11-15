// Interface text. English is the source: t('Approvals') returns the translation for the chosen language, or the
// English text when there is none. To add Norwegian, fill in `no` below; nothing else has to change.

const dictionaries = {
  en: {},
  no: {
    // 'Approvals': 'Godkjenninger',
  },
}

let lang = 'en'
try {
  lang = localStorage.getItem('athena.lang') || 'en'
} catch {
  // storage is blocked
}

export const languages = [
  { code: 'en', name: 'English' },
  // { code: 'no', name: 'Norsk' },
]

export function setLanguage(code) {
  lang = dictionaries[code] ? code : 'en'
  try {
    localStorage.setItem('athena.lang', lang)
  } catch {
    // storage is blocked
  }
  location.reload()
}

export function language() {
  return lang
}

export function t(text, vars) {
  let s = (dictionaries[lang] && dictionaries[lang][text]) || text
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replaceAll('{' + k + '}', v)
  return s
}
