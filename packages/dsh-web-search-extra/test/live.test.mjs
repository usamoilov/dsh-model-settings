// Live checks against a real SearXNG on 127.0.0.1:8888; a real Claude search only with --claude (uses the subscription).
import assert from 'node:assert/strict'
import { searxngSearch } from '../lib/searxng.js'
import { apply } from '../lib/index.js'

const started = Date.now()
const sources = await searxngSearch('http://127.0.0.1:8888', 'latest stable Node.js version', { limit: 8 })
assert.ok(sources.length > 0, 'SearXNG returns results')
console.log(`searxng: ${Date.now() - started} ms, ${sources.length} sources; first: ${sources[0].url}`)

const registered = {}
apply({
  get: () => null,
  effect: (fn) => fn(),
  llm: { resolveModelInfo: async () => ({}), stream: async function* () { throw new Error('no llm in this test') } },
  web: { registerSearchProvider: (p) => { registered[p.id] = p; return () => {} }, searchProviders: new Map() },
}, { summary: false })
assert.deepEqual(Object.keys(registered).sort(), ['auto', 'claude-code', 'searxng'])
const viaAuto = await registered.auto.search({ query: 'nodejs releases', maxResults: 3 })
assert.ok(viaAuto.sources.length > 0 && viaAuto.sources.length <= 3, 'auto falls through to SearXNG when DeepSeek is absent')

if (process.argv.includes('--claude')) {
  const t = Date.now()
  const r = await registered['claude-code'].search({ query: 'latest stable Node.js version', maxResults: 3 })
  assert.ok(r.sources.length > 0)
  console.log(`claude: ${Math.round((Date.now() - t) / 1000)} s, ${r.sources.length} sources`)
}
console.log('dsh-web-search-extra live: ok')
