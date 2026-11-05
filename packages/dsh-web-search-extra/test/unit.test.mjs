import assert from 'node:assert/strict'
import { buildClaudePrompt } from '../lib/claude.js'
import { limiter, normalizeConfig, searchInOrder } from '../lib/index.js'
import { normalizeSources, parseAnswer, sourcesFromToolText } from '../lib/parse.js'
import { buildSummaryPrompt, createSearxngSearch } from '../lib/searxng.js'

// --- parsing Claude replies
const reply = 'Here:\n```json\n{"answer":"Node.js 25 is current.","sources":[' +
  '{"url":"https://nodejs.org/en/blog","title":"Node.js Blog","snippet":"  Node.js 25.1.0   released ","publishedAt":"2026-10-28"},' +
  '{"url":"https://nodejs.org/en/blog/","title":"dup"},{"url":"javascript:alert(1)"},{"url":"https://example.com/x","publishedAt":"nope"}]}\n```'
assert.deepEqual(parseAnswer(reply), {
  answer: 'Node.js 25 is current.',
  sources: [
    { url: 'https://nodejs.org/en/blog', title: 'Node.js Blog', snippet: 'Node.js 25.1.0 released', publishedAt: '2026-10-28' },
    { url: 'https://example.com/x' },
  ],
})
assert.deepEqual(parseAnswer('see https://a.example/page').sources, [{ url: 'https://a.example/page' }])
assert.deepEqual(sourcesFromToolText('Links: [{"title":"R \\"25\\"","url":"https://nodejs.org/r"}]'), [{ url: 'https://nodejs.org/r', title: 'R "25"' }])
assert.deepEqual(normalizeSources(null), [])
assert.ok(buildClaudePrompt('a "b"\nc', 5).includes('Query: "a \\"b\\"\\nc"'))

// --- summary prompt numbers the sources
const prompt = buildSummaryPrompt('версия node', [{ url: 'https://a', title: 'A', snippet: 's1' }, { url: 'https://b' }])
assert.ok(prompt.includes('[1] A\nhttps://a\ns1'))
assert.ok(prompt.includes('[2] https://b\nhttps://b'))
assert.ok(prompt.includes('Query: "версия node"'))

// --- auto: first non-empty provider wins, failures fall through, abort stops
const calls = []
const providers = {
  'deepseek-official': { available: () => true, search: async () => { calls.push('ds'); throw new Error('quota') } },
  'searxng': { available: () => true, search: async () => { calls.push('sx'); return { sources: [{ url: 'https://q' }], truncated: false } } },
  'claude-code': { available: () => true, search: async () => { calls.push('claude'); return { sources: [{ url: 'https://c' }] } } },
}
const result = await searchInOrder(['deepseek-official', 'searxng', 'claude-code'], (id) => providers[id], { query: 'x' })
assert.equal(result.sources[0].url, 'https://q')
assert.deepEqual(calls, ['ds', 'sx'])
await assert.rejects(
  () => searchInOrder(['missing', 'deepseek-official'], (id) => providers[id] || null, { query: 'x' }),
  /missing: not installed; deepseek-official: quota/,
)
const empty = { available: () => true, search: async () => ({ sources: [] }) }
const off = { available: () => false, search: async () => { throw new Error('must not run') } }
await assert.rejects(() => searchInOrder(['a', 'b', 'auto'], (id) => ({ a: empty, b: off })[id], { query: 'x' }), /a: empty answer; b: unavailable/)
const controller = new AbortController()
controller.abort()
await assert.rejects(() => searchInOrder(['x'], () => ({ available: () => true, search: async () => { throw new Error('aborted') } }), { query: 'x' }, controller.signal), /aborted/)

// --- SearXNG search with a fake SearXNG and a fake model
const realFetch = globalThis.fetch
globalThis.fetch = async (url) => {
  assert.ok(String(url).startsWith('http://sx.test/search?q='))
  return { ok: true, json: async () => ({ results: [
    { url: 'https://nodejs.org/en', title: 'Node.js', content: 'Node.js 26 LTS', publishedDate: '2026-10-28T00:00:00' },
    { url: 'https://nodejs.org/en/', title: 'dup' },
    { url: 'https://github.com/nodejs/node/releases', title: 'Releases' },
  ] }) }
}
const streamed = []
const llm = {
  resolveModelInfo: async () => ({ reasoning: { efforts: [{ id: 'off' }, { id: 'high' }] } }),
  stream: async function* (options) {
    streamed.push(options)
    yield { type: 'block-start', index: 0, blockType: 'text' }
    yield { type: 'text-delta', index: 0, text: 'Актуальная LTS — Node.js 26 [1].' }
    yield { type: 'finish', reason: 'stop' }
  },
}
const sxSearch = createSearxngSearch({ llm, searxngURL: () => 'http://sx.test', summaryModel: () => ({ provider: 'local-llm', model: 'q' }), summary: () => true, summaryTimeoutMs: 1000 })
const q = await sxSearch({ query: 'node lts', maxResults: 5 })
assert.equal(q.content, 'Актуальная LTS — Node.js 26 [1].')
assert.deepEqual(q.sources.map((s) => s.url), ['https://nodejs.org/en', 'https://github.com/nodejs/node/releases'])
assert.equal(streamed[0].reasoningEffort, 'off')
assert.equal(streamed[0].provider, 'local-llm')
// a failing model still returns the results
const brokenLlm = { resolveModelInfo: async () => ({}), stream: async function* () { throw new Error('busy') } }
const warnings = []
const q2 = await createSearxngSearch({ llm: brokenLlm, searxngURL: () => 'http://sx.test', summaryModel: () => ({ provider: 'p', model: 'm' }), summary: () => true, summaryTimeoutMs: 1000, log: (m) => warnings.push(m) })({ query: 'x' })
assert.equal(q2.content, undefined)
assert.equal(q2.sources.length, 2)
assert.ok(warnings[0].includes('busy'))
// summary off: no model call
const q3 = await createSearxngSearch({ llm: { stream: () => { throw new Error('no') } }, searxngURL: () => 'http://sx.test', summaryModel: () => null, summary: () => false, summaryTimeoutMs: 1000 })({ query: 'x' })
assert.equal(q3.content, undefined)
globalThis.fetch = realFetch

// --- limiter
const run = limiter(2)
let active = 0
let peak = 0
await Promise.all([1, 2, 3, 4].map(() => run(async () => {
  active += 1
  peak = Math.max(peak, active)
  await new Promise((r) => setTimeout(r, 15))
  active -= 1
})))
assert.equal(peak, 2)

// --- config: defaults and validation
assert.deepEqual(normalizeConfig({}).summaryModel, 'auto')
assert.equal(normalizeConfig({}).summary, true)
assert.equal(normalizeConfig({}).claudeModel, 'haiku')
assert.deepEqual(normalizeConfig({ summaryModel: { provider: 'p', model: 'm' }, summary: false }), { ...normalizeConfig({}), summaryModel: { provider: 'p', model: 'm' }, summary: false })
assert.deepEqual(normalizeConfig({ summaryModel: { provider: 'p' } }).summaryModel, 'auto', 'incomplete ref falls back to auto')
assert.deepEqual(normalizeConfig({ autoOrder: ['searxng', 'claude-code'] }).autoOrder, ['searxng', 'claude-code'])

console.log('dsh-web-search-extra unit: ok')
