/**
 * Web search with a self-hosted SearXNG metasearch (JSON API) and, optionally,
 * a short answer written by a DSH model from the results through `ctx.llm` —
 * the same route, key and attribution that conversations use.
 *
 * The answer is best effort: if the model is busy, slow or unavailable, the
 * search still returns the SearXNG sources, so `web_search` never fails because
 * of the model.
 */
import { normalizeSources } from './parse.js'

export const DEFAULT_SEARXNG = 'http://127.0.0.1:8888'

/** Ask SearXNG; returns normalized sources (title, url, snippet, publishedAt). */
export async function searxngSearch(baseURL, query, { signal, timeoutMs = 15000, limit = 10 } = {}) {
  const url = new URL('/search', baseURL)
  url.searchParams.set('q', String(query))
  url.searchParams.set('format', 'json')
  url.searchParams.set('safesearch', '0')
  const controller = new AbortController()
  const abort = () => controller.abort()
  signal?.addEventListener('abort', abort, { once: true })
  const timer = setTimeout(abort, timeoutMs)
  let body
  try {
    const response = await fetch(url, { signal: controller.signal, headers: { accept: 'application/json' } })
    if (!response.ok) throw new Error(`SearXNG HTTP ${response.status}`)
    body = await response.json()
  } catch (error) {
    if (signal?.aborted) throw new Error('search cancelled')
    if (controller.signal.aborted) throw new Error(`SearXNG did not answer within ${Math.round(timeoutMs / 1000)} s`)
    throw new Error(`SearXNG is unreachable at ${baseURL}: ${error?.message || error}`)
  } finally {
    clearTimeout(timer)
    signal?.removeEventListener('abort', abort)
  }
  const results = Array.isArray(body?.results) ? body.results : []
  return normalizeSources(results.map((r) => ({
    url: r?.url,
    title: r?.title,
    snippet: r?.content,
    publishedAt: r?.publishedDate || undefined,
  }))).slice(0, limit)
}

export function buildSummaryPrompt(query, sources) {
  const list = sources.map((s, i) => `[${i + 1}] ${s.title || s.url}\n${s.url}${s.snippet ? `\n${s.snippet}` : ''}`).join('\n\n')
  return [
    'You are a web search assistant. Answer the query below in 2-5 sentences, in the language of the query,',
    'using only these search results. Cite sources by their numbers in square brackets, e.g. [1].',
    'If the results do not answer the query, say so in one sentence.',
    '',
    `Query: ${JSON.stringify(String(query))}`,
    '',
    'Search results:',
    list,
  ].join('\n')
}

/** Stream one short completion through ctx.llm and return its text. */
async function summarize(llm, route, prompt, { signal, timeoutMs, maxTokens }) {
  const controller = new AbortController()
  const abort = () => controller.abort()
  signal?.addEventListener('abort', abort, { once: true })
  const timer = setTimeout(abort, timeoutMs)
  let effort
  try {
    const info = await llm.resolveModelInfo(route.provider, route.model)
    const efforts = info?.reasoning?.efforts?.map((e) => e.id) || []
    if (efforts.includes('off')) effort = 'off'
  } catch {
    effort = undefined
  }
  let text = ''
  try {
    for await (const chunk of llm.stream({
      provider: route.provider,
      model: route.model,
      ...(effort ? { reasoningEffort: effort } : {}),
      maxTokens,
      messages: [{ role: 'user', content: [{ type: 'text', text: prompt }] }],
      signal: controller.signal,
    })) {
      if (chunk?.type === 'text-delta' && typeof chunk.text === 'string') text += chunk.text
    }
  } finally {
    clearTimeout(timer)
    signal?.removeEventListener('abort', abort)
  }
  return text.trim()
}

/**
 * @param {{ llm: object, searxngURL: () => string, summaryModel: () => ({provider, model}|null),
 *           summary: () => boolean, summaryTimeoutMs: number, log?: (msg: string) => void }} deps
 */
export function createSearxngSearch(deps) {
  return async function searxng(request, signal) {
    const maxResults = Math.min(Math.max(Number(request.maxResults) || 8, 1), 20)
    const sources = await searxngSearch(deps.searxngURL(), request.query, { signal, limit: Math.max(maxResults, 8) })
    if (!sources.length) throw new Error('SearXNG found nothing')
    let content
    const route = deps.summary() ? deps.summaryModel() : null
    if (route?.provider && route?.model) {
      try {
        content = await summarize(deps.llm, route, buildSummaryPrompt(request.query, sources.slice(0, 8)), {
          signal, timeoutMs: deps.summaryTimeoutMs, maxTokens: 500,
        })
      } catch (error) {
        if (signal?.aborted) throw new Error('search cancelled')
        // a busy or slow model must not cost the search its results
        deps.log?.(`answer skipped (${route.provider}/${route.model}): ${error?.message || error}`)
      }
    }
    return { content: content || undefined, sources: sources.slice(0, maxResults), truncated: false }
  }
}
