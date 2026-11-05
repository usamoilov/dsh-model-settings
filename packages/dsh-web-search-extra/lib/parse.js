/** Turn Claude's reply (and, as a fallback, raw WebSearch output) into normalized sources. */

const isHttp = (value) => typeof value === 'string' && /^https?:\/\/[^\s"'<>]+$/i.test(value.trim())
const clip = (value, max) => {
  if (typeof value !== 'string') return undefined
  const text = value.replace(/\s+/g, ' ').trim()
  if (!text) return undefined
  return text.length > max ? `${text.slice(0, max - 1)}…` : text
}

/** Keep valid http(s) sources, drop duplicates, trim fields. */
export function normalizeSources(list) {
  const seen = new Set()
  const out = []
  for (const item of Array.isArray(list) ? list : []) {
    const url = typeof item === 'string' ? item : item?.url
    if (!isHttp(url)) continue
    const key = url.trim().replace(/#.*$/, '').replace(/\/$/, '')
    if (seen.has(key)) continue
    seen.add(key)
    const source = { url: url.trim() }
    const title = clip(item?.title, 200)
    const snippet = clip(item?.snippet, 600)
    const publishedAt = clip(item?.publishedAt, 40)
    if (title) source.title = title
    if (snippet) source.snippet = snippet
    if (publishedAt && !Number.isNaN(Date.parse(publishedAt))) source.publishedAt = publishedAt
    out.push(source)
  }
  return out
}

/**
 * Parse Claude's JSON reply; tolerate code fences and text around the object.
 * @returns {{ answer: string, sources: object[] }}
 */
export function parseAnswer(text) {
  const raw = String(text || '').trim()
  if (!raw) return { answer: '', sources: [] }
  const unfenced = raw.replace(/^```(?:json)?\s*/i, '').replace(/\s*```$/, '')
  const start = unfenced.indexOf('{')
  const end = unfenced.lastIndexOf('}')
  if (start >= 0 && end > start) {
    try {
      const value = JSON.parse(unfenced.slice(start, end + 1))
      return { answer: clip(value?.answer, 4000) || '', sources: normalizeSources(value?.sources) }
    } catch {
      // not JSON: fall through and keep the prose as the answer
    }
  }
  const urls = [...raw.matchAll(/https?:\/\/[^\s)"'<>\]]+/g)].map((m) => ({ url: m[0] }))
  return { answer: clip(raw, 4000) || '', sources: normalizeSources(urls) }
}

/** Fallback: `{"title":"…","url":"…"}` pairs from WebSearch tool output. */
export function sourcesFromToolText(text) {
  const out = []
  const re = /"title"\s*:\s*"((?:[^"\\]|\\.)*)"\s*,\s*"url"\s*:\s*"((?:[^"\\]|\\.)*)"/g
  for (const match of String(text || '').matchAll(re)) {
    let title = match[1]
    let url = match[2]
    try {
      title = JSON.parse(`"${title}"`)
      url = JSON.parse(`"${url}"`)
    } catch {
      // keep raw
    }
    out.push({ url, title })
  }
  return normalizeSources(out)
}
