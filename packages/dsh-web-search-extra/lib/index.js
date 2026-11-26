/**
 * dsh-web-search-extra: extra providers for DSH's `web_search` tool — searxng,
 * claude-code and auto. The searches run in the dsh-model-settings program
 * (internal/search); this module registers the providers with `ctx.web` and
 * lends the program `ctx.llm` (the short SearXNG answer), the live default
 * model and DSH's own providers (DeepSeek, for `auto`).
 *
 * Config (row `web-search-extra`, all optional): searxngURL, summary,
 * summaryModel, claudeModel, autoOrder, timeoutMs, summaryTimeoutMs,
 * concurrency — see README.md; `binary` overrides where the program is found.
 */
import { plain, startSidecar } from '../../../shim/sidecar.js'

export const name = 'dsh-web-search-extra'
export const inject = ['web', 'llm']

const coded = (message, code) => Object.assign(new Error(message), { code })

export async function apply(ctx, config = {}) {
  const available = {}
  const program = startSidecar(ctx, {
    plugin: 'web-search-extra',
    config,
    label: name,
    handlers: {
      currentSelection() {
        try {
          return ctx.get('agentDefaultModel')?.currentSelection?.() ?? null
        } catch {
          return null
        }
      },
      async llmModelInfo({ provider, model }) {
        const info = await ctx.llm.resolveModelInfo(provider, model)
        return { efforts: (info?.reasoning?.efforts || []).map((effort) => effort.id) }
      },
      async llmComplete({ provider, model, reasoningEffort, maxTokens, prompt }, signal) {
        let text = ''
        for await (const chunk of ctx.llm.stream({
          provider,
          model,
          ...(reasoningEffort ? { reasoningEffort } : {}),
          maxTokens,
          messages: [{ role: 'user', content: [{ type: 'text', text: prompt }] }],
          signal,
        })) {
          if (chunk?.type === 'text-delta' && typeof chunk.text === 'string') text += chunk.text
        }
        return { text }
      },
      // DSH's own providers (DeepSeek) for `auto`; the registry map is internal, so it is guarded
      async providerSearch({ id, request }, signal) {
        const provider = ctx.web?.searchProviders?.get?.(id)
        if (!provider) throw coded(`${id}: not installed`, 'NOT_INSTALLED')
        if (typeof provider.available === 'function' && !provider.available()) throw coded(`${id}: unavailable`, 'UNAVAILABLE')
        const result = await provider.search(request, signal)
        return { content: result?.content ?? null, sources: result?.sources || [], truncated: Boolean(result?.truncated) }
      },
    },
    onNotify: {
      providers: (params) => Object.assign(available, params?.available),
    },
  })

  const { providers } = await program.ready()
  for (const p of providers) {
    available[p.id] = p.available
    const provider = {
      id: p.id,
      available: () => available[p.id] !== false,
      search: (request, signal) => program.call('search', { id: p.id, request: plain(request) }, signal),
      ...(p.ready ? { ready: () => program.call('ready', { id: p.id }) } : {}),
    }
    ctx.effect(() => ctx.web.registerSearchProvider(provider), `${name}: ${p.id}`)
  }
}
