// Unit checks of the helper modules: messages, plugin config, Claude model list.
import assert from 'node:assert/strict'
import os from 'node:os'
import { MESSAGES, failure, localeOf, translator } from '../lib/i18n.js'
import { readConfig } from '../lib/index.js'
import { CLAUDE_MODEL_PATTERN, FALLBACK_CLAUDE_MODELS, createClaudeModels, toOptions } from '../lib/claude-models.js'

// ---- messages: every language has the English keys; placeholders are filled; unknown → English
for (const [locale, dict] of Object.entries(MESSAGES)) {
  const missing = Object.keys(MESSAGES.en).filter((key) => !(key in dict))
  assert.deepEqual(missing, [], `${locale}: missing keys`)
}
const header = (value) => ({ headers: new Headers(value ? { 'x-dsh-locale': value } : {}) })
assert.equal(localeOf(header('ru')), 'ru')
assert.equal(localeOf(header('zh-CN')), 'zh')
assert.equal(localeOf(header('de')), 'en')
assert.equal(localeOf(header()), 'en')
assert.equal(localeOf(null), 'en')
assert.equal(translator('en')('err.notInCatalog', { model: 'p / m' }), 'The catalog has no model p / m')
assert.equal(translator('ru')('auto'), 'Авто')
assert.equal(translator('xx')('auto'), 'Auto')
assert.equal(translator('en')('no.such.key'), 'no.such.key')
assert.equal(translator('en')('err.search', {}), 'Search failed: {error}', 'a missing parameter stays visible')
const err = failure(422, 'err.compose', { target: 'Profile web', problem: 'x' })
assert.equal(err.status, 422)
assert.equal(err.key, 'err.compose')
assert.equal(err.message, 'Profile web: x. Changes were rolled back.')

// ---- plugin config: defaults, validation, ~ expansion
const empty = readConfig(undefined)
assert.deepEqual(empty, { profiles: [], webUnit: null, overlays: [], searxngURL: 'http://127.0.0.1:8888' })
const cfg = readConfig({
  profiles: ['web', 'bad/name', 7, 'headless'],
  webUnit: 'dsh-web.service',
  overlays: [{ path: '~/svc/extra.patch.yml', label: 'Service', patches: ['~/svc/base.patch.yml', 3] }, { path: 5 }, { path: '/x/y.yml' }],
  searxngURL: 'http://search.lan:8080',
})
const home = os.homedir()
assert.deepEqual(cfg.profiles, ['web', 'headless'])
assert.equal(cfg.webUnit, 'dsh-web.service')
assert.deepEqual(cfg.overlays, [
  { path: `${home}/svc/extra.patch.yml`, label: 'Service', profile: 'headless', patches: [`${home}/svc/base.patch.yml`] },
  { path: '/x/y.yml', label: 'y.yml', profile: 'headless', patches: [] },
])
assert.equal(cfg.searxngURL, 'http://search.lan:8080')
assert.equal(readConfig({ webUnit: 'rm -rf /; x.service' }).webUnit, null, 'a unit name is never a shell string')
assert.equal(readConfig({ webUnit: 'dsh-web' }).webUnit, null)

// ---- Claude models: Claude Code's list, `default` as the recommendation, fallback on any failure
const sdkList = [
  { value: 'default', displayName: 'Default (recommended)', resolvedModel: 'claude-opus-5-5', description: 'Opus 5.5 · Best for everyday tasks' },
  { value: 'opus', displayName: 'Opus 5.5', resolvedModel: 'claude-opus-5-5', description: 'For complex work' },
  { value: 'haiku', displayName: 'Haiku 5.5', resolvedModel: 'claude-haiku-5-5', description: 'Fastest' },
  { value: 'claude-opus-4-8', displayName: 'Opus 4.8' },
  { value: 'sonnet[1m]', displayName: 'Sonnet (1M context)' },
  { value: 'bad value; rm', displayName: 'Bad' },
  { displayName: 'no value' },
  null,
]
const options = toOptions(sdkList)
assert.deepEqual(options.recommended, { id: 'default', name: 'Default (recommended)', model: 'claude-opus-5-5', description: 'Opus 5.5 · Best for everyday tasks' })
assert.deepEqual(options.models.map((m) => m.id), ['opus', 'haiku', 'claude-opus-4-8', 'sonnet[1m]'])
assert.deepEqual(options.models[2], { id: 'claude-opus-4-8', name: 'Opus 4.8', model: null, description: '' })
assert.deepEqual(toOptions(undefined), { models: [], recommended: null })
assert.ok(CLAUDE_MODEL_PATTERN.test('claude-haiku-4-5-20251001'))
assert.ok(!CLAUDE_MODEL_PATTERN.test('-x'))

let probes = 0
let seenOptions = null
let aborted = false
const fakeSdk = {
  query({ prompt, options: o }) {
    probes++
    seenOptions = o
    o.abortController.signal.addEventListener('abort', () => { aborted = true })
    prompt.next() // the prompt never yields a message
    return { supportedModels: async () => sdkList }
  },
}
const live = createClaudeModels({ sdkFile: '/fake/sdk.mjs', importer: async () => fakeSdk })
const first = await live.list()
assert.equal(first.source, 'claude-code')
assert.equal(first.models.length, 4)
assert.equal(first.recommended.model, 'claude-opus-5-5')
assert.ok(aborted, 'the Claude Code process is ended after the answer')
assert.deepEqual(seenOptions.settingSources, [], 'no user or project settings are loaded')
assert.deepEqual(seenOptions.tools, [])
assert.equal(seenOptions.persistSession, false)
assert.equal(Object.keys(seenOptions.env).some((k) => /KEY|TOKEN|SECRET/i.test(k)), false, 'no secrets are passed in env')
await live.list()
assert.equal(probes, 1, 'cached')
await live.list({ refresh: true })
assert.equal(probes, 2, 'refresh probes again')
const twice = await Promise.all([live.list({ refresh: true }), live.list({ refresh: true })])
assert.equal(probes, 3, 'concurrent calls share one probe')
assert.equal(twice[0], twice[1])

const missing = await createClaudeModels({ sdkFile: null }).list()
assert.deepEqual(missing, { source: 'fallback', reason: 'sdk-missing', models: FALLBACK_CLAUDE_MODELS, recommended: null })
const broken = await createClaudeModels({ sdkFile: '/x', importer: async () => { throw new Error('not logged in') } }).list()
assert.equal(broken.source, 'fallback')
assert.equal(broken.reason, 'not logged in')
const emptyList = await createClaudeModels({ sdkFile: '/x', importer: async () => ({ query: () => ({ supportedModels: async () => [] }) }) }).list()
assert.equal(emptyList.reason, 'empty')
const slow = await createClaudeModels({
  sdkFile: '/x',
  timeoutMs: 50,
  importer: async () => ({
    query: ({ options: o }) => ({
      supportedModels: () => new Promise((resolve, reject) => o.abortController.signal.addEventListener('abort', () => reject(new Error('aborted')))),
    }),
  }),
}).list()
assert.equal(slow.source, 'fallback', 'a hanging probe times out')


console.log('dsh-model-defaults unit (modules): ok')
