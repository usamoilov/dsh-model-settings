/**
 * dsh-model-defaults, host side: the routes of the Default model settings page.
 * The work happens in the dsh-model-settings program (internal/defaults); this
 * module registers its routes and lends it the DSH services it needs:
 * the model catalog, the default model and the `web` search service.
 *
 * Config (row `model-defaults`, all optional): profiles, webUnit, overlays,
 * searxngURL — see README.md; `binary` overrides where the program is found.
 */
import { startSidecar } from '../../../shim/sidecar.js'

export const name = 'dsh-model-defaults'
export const inject = ['connection', 'agentDefaultModel', 'sessionController']

/** Request headers passed to the program (never the session cookie). */
function headersOf(request) {
  const out = {}
  request.headers.forEach((value, key) => {
    if (key !== 'cookie' && key !== 'authorization') out[key] = value
  })
  return out
}

export async function apply(ctx, config = {}) {
  const program = startSidecar(ctx, {
    plugin: 'model-defaults',
    config,
    label: name,
    handlers: {
      async catalog() {
        const value = await ctx.sessionController.modelCatalog()
        return { groups: value?.groups || [], failures: value?.failures || [] }
      },
      currentSelection: () => ctx.agentDefaultModel.currentSelection(),
      saveSelection: (selection) => ctx.agentDefaultModel.saveSelection(selection),
      async webSearch({ query, maxResults, timeoutMs }, signal) {
        let web = null
        try {
          web = ctx.get('web')
        } catch {
          web = null
        }
        if (!web?.search) throw Object.assign(new Error('the web search service is not mounted'), { code: 'NO_SERVICE' })
        const result = await web.search({ query, maxResults }, AbortSignal.any([signal, AbortSignal.timeout(timeoutMs)]))
        return { content: result?.content ?? null, sources: result?.sources || [] }
      },
    },
  })

  const { routes } = await program.ready()
  for (const route of routes) {
    ctx.effect(() => ctx.connection.fetch.register({
      path: route.path,
      methods: route.methods,
      requestBody: 'buffered',
      fetch: async (request) => {
        try {
          const body = request.method === 'GET' || request.method === 'HEAD' ? null : await request.text()
          const reply = await program.call('http', { method: request.method, url: request.url, headers: headersOf(request), body }, request.signal)
          return new Response(reply.body, { status: reply.status, headers: reply.headers })
        } catch (error) {
          return Response.json({ error: `${name}: ${error?.message || error}` }, { status: 502, headers: { 'cache-control': 'no-store' } })
        }
      },
    }), `${name}: ${route.path}`)
  }
}
