import MarkdownIt from 'markdown-it'
import { ref } from 'vue'
import type { HighlighterGeneric } from 'shiki'

// html stays off: agent output is untrusted and lands in v-html.
const md = new MarkdownIt({ html: false, linkify: true, breaks: false })

const defaultLink = md.renderer.rules.link_open
md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  tokens[idx]!.attrSet('target', '_blank')
  tokens[idx]!.attrSet('rel', 'noopener noreferrer')
  return defaultLink ? defaultLink(tokens, idx, options, env, self) : self.renderToken(tokens, idx, options)
}

const themes = { light: 'github-light', dark: 'github-dark' }
const cache = new Map<string, string>()
const queued = new Set<string>()

// version bumps when a highlight lands so rendered markdown recomputes.
export const highlightVersion = ref(0)

let highlighter: Promise<HighlighterGeneric<any, any>> | null = null

function getHighlighter() {
  highlighter ??= import('shiki/bundle/web').then(({ createHighlighter }) =>
    createHighlighter({ themes: Object.values(themes), langs: [] }),
  )
  return highlighter
}

async function highlight(code: string, lang: string, key: string) {
  try {
    const h = await getHighlighter()
    const { bundledLanguages } = await import('shiki/bundle/web')
    const known = lang in bundledLanguages
    if (known && !h.getLoadedLanguages().includes(lang)) await h.loadLanguage(lang as any)
    cache.set(key, h.codeToHtml(code, { lang: known ? lang : 'text', themes, defaultColor: false }))
    highlightVersion.value++
  }
  catch {
    // Unhighlighted is fine; markdown-it already escaped it.
  }
}

let highlighting = true

md.options.highlight = (code, lang) => {
  if (!lang || !highlighting) return ''
  const key = `${lang}\u0000${code}`
  const hit = cache.get(key)
  if (hit) return hit
  if (!queued.has(key)) {
    queued.add(key)
    void highlight(code, lang.toLowerCase(), key)
  }
  return ''
}

// A block still streaming is rendered plain: every delta would be a new cache
// entry and a new highlight.
export function renderMarkdown(src: string, highlight = true): string {
  highlighting = highlight
  try {
    return md.render(src)
  }
  finally {
    highlighting = true
  }
}
