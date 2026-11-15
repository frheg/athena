// Safe markdown: marked renders, DOMPurify removes anything active. Remote images are not loaded unless the user
// asks for them per answer (an image URL can leak that you read the answer, and where from), so by default they
// become links.
import { Marked } from 'marked'
import DOMPurify from 'dompurify'

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer nofollow')
  }
})

function make(loadImages) {
  const m = new Marked({ gfm: true, breaks: true })
  m.use({
    renderer: {
      image({ href, text }) {
        const safe = /^https?:\/\//i.test(href || '') ? href : ''
        if (!safe) return escape(text || 'image')
        if (loadImages) return `<img src="${escape(safe)}" alt="${escape(text || '')}" loading="lazy" referrerpolicy="no-referrer">`
        return `<a href="${escape(safe)}" class="img-link">🖼 ${escape(text || 'image')}</a>`
      },
      html({ text }) {
        return escape(text) // raw HTML from the model is shown as text
      },
    },
  })
  return m
}

const plain = make(false)
const withImages = make(true)

export function render(md, loadImages = false) {
  const html = (loadImages ? withImages : plain).parse(md || '')
  return DOMPurify.sanitize(html, {
    ALLOWED_URI_REGEXP: /^(?:https?:|mailto:|#)/i,
    FORBID_TAGS: ['style', 'form', 'input', 'button', 'iframe', 'object', 'embed'],
    FORBID_ATTR: ['style'],
  })
}

export function hasImages(md) {
  return /!\[[^\]]*\]\(\s*https?:\/\//i.test(md || '')
}

function escape(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c])
}
