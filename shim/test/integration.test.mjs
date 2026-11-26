// The three plugin modules against the real program (bin/dsh-model-settings) inside a fake DSH:
// profile folders in a temp DSH_HOME, a fake `dsh --dump-config`, a local SearXNG, no Claude Code.
// Needs only Node.js and the built program: go build -o bin/ ./cmd/dsh-model-settings
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import http from 'node:http'
import os from 'node:os'
import path from 'node:path'

const tmp = await fs.mkdtemp(path.join(os.tmpdir(), 'dsh-model-settings-'))
const home = path.join(tmp, 'dsh')
const webDir = path.join(home, 'profiles', 'web')
const headlessDir = path.join(home, 'profiles', 'headless')
const dshRoot = path.join(tmp, 'dsh-install')
await fs.mkdir(webDir, { recursive: true })
await fs.mkdir(headlessDir, { recursive: true })
await fs.mkdir(path.join(dshRoot, 'lib'), { recursive: true })
await fs.mkdir(path.join(tmp, 'empty-path'))

const deps = { dependencies: { 'dsh-model-defaults': 'link:x', 'dsh-web-search-extra': 'link:y' } }
await fs.writeFile(path.join(webDir, 'package.json'), JSON.stringify({ ...deps, dsh: { profile: { bundles: ['dsh-model-defaults'] } } }))
await fs.writeFile(path.join(headlessDir, 'package.json'), JSON.stringify({ dependencies: { 'dsh-web-search-extra': 'link:y' } }))
const webPatch = '# my web profile\n- id: llm-pi-ai\n  config:\n    providers:\n      local-llm:\n        api: openai-completions\n'
const headlessPatch = '# headless, by hand\n- id: agent-default-model\n  config:\n    provider: deepseek-official\n    model: deepseek-flash\n- insert:\n    - id: model-defaults-guard\n      name: /x/guard.js\n'
await fs.writeFile(path.join(webDir, 'cordis.patch.yml'), webPatch)
await fs.writeFile(path.join(headlessDir, 'cordis.patch.yml'), headlessPatch)
// `dsh --profile <name> --dump-config`: prints the patch plus what the base composes
await fs.writeFile(path.join(dshRoot, 'lib', 'bin.js'), `
const fs = require('node:fs'); const path = require('node:path')
const profile = process.argv[process.argv.indexOf('--profile') + 1]
const text = fs.readFileSync(path.join(process.env.DSH_HOME, 'profiles', profile, 'cordis.patch.yml'), 'utf8')
if (text.includes('BROKEN')) { console.error('boom: bad row'); process.exit(1) }
process.stdout.write(text + '\\n- id: web-search-extra\\n- id: llm-pi-ai\\n  config:\\n    providers:\\n      local-llm:\\n')
`.replace(/^\n/, ''))
await fs.writeFile(path.join(dshRoot, 'package.json'), '{"type":"commonjs"}')

Object.assign(process.env, { DSH_HOME: home, DSH_ROOT: dshRoot, PATH: path.join(tmp, 'empty-path') })
delete process.env.CLAUDE_CODE_OAUTH_TOKEN

const disposers = []
const effect = (fn) => {
  const dispose = fn()
  if (typeof dispose === 'function') disposers.push(dispose)
}
const json = async (response) => ({ status: response.status, body: await response.json() })
const log = { info: console.info, warn: console.warn }
const lines = []
console.info = (line) => lines.push(String(line))
console.warn = (line) => lines.push(`warn ${line}`)

try {
  // ---- dsh-model-defaults: routes, state, a save across both profiles
  const { apply: applyDefaults } = await import('../../packages/dsh-model-defaults/lib/index.js')
  const routes = {}
  let selection = { provider: 'deepseek-official', model: 'deepseek-flash', reasoningEffort: null }
  const saved = []
  const webSearches = []
  await applyDefaults({
    get: (name) => {
      if (name === 'profileContext') return { name: 'web', dir: webDir }
      if (name === 'web') return { search: async (request) => { webSearches.push(request); return { content: 'answer', sources: [{ url: 'https://a.example', title: 'A' }] } } }
      return undefined
    },
    effect,
    connection: { fetch: { register: (route) => { routes[route.path] = route; return () => delete routes[route.path] } } },
    agentDefaultModel: {
      currentSelection: () => selection,
      saveSelection: async (next) => { saved.push(next); selection = { reasoningEffort: null, ...next } },
    },
    sessionController: {
      modelCatalog: async () => ({
        groups: [
          { id: 'local-llm', name: 'Local', models: [{ id: 'q', name: 'Q', reasoning: { efforts: [{ id: 'off' }, { id: 'high', name: 'High' }], defaultEffort: 'off' } }] },
          { id: 'deepseek-official', name: 'DeepSeek', models: [{ id: 'deepseek-flash' }], extra: () => 'functions are dropped' },
        ],
        failures: [],
      }),
    },
  }, { webUnit: 'dsh-web.service', searxngURL: 'http://127.0.0.1:9' })
  assert.deepEqual(Object.keys(routes).sort(), ['/api/model-defaults.restart', '/api/model-defaults.save', '/api/model-defaults.state', '/api/model-defaults.test-search'])
  const url = (p) => `http://dsh.local${p}`
  const post = (p, body, headers = {}) => routes[p].fetch(new Request(url(p), {
    method: 'POST', body: JSON.stringify(body), headers: { 'content-type': 'application/json', 'x-dsh-model-defaults': '1', ...headers },
  }))

  let { status, body: state } = await json(await routes['/api/model-defaults.state'].fetch(new Request(url('/api/model-defaults.state'), { headers: { 'x-dsh-locale': 'ru' } })))
  assert.equal(status, 200)
  assert.equal(state.running, 'web')
  assert.deepEqual(Object.keys(state.profiles), ['web', 'headless'])
  assert.deepEqual(state.apply, { web: true, headless: true })
  assert.deepEqual(state.catalog.groups.map((g) => [g.id, g.deepseek]), [['local-llm', false], ['deepseek-official', true]])
  assert.deepEqual(state.catalog.groups[0].models[0].reasoning, { efforts: [{ id: 'off', name: 'off' }, { id: 'high', name: 'High' }], defaultEffort: 'off' })
  assert.equal(state.catalog.groups[1].models[0].name, 'deepseek-flash')
  assert.equal(state.profiles.web.defaultModel.provider, 'deepseek-official', 'the live default for the running profile')
  assert.equal(state.profiles.web.guard, true)
  assert.equal(state.profiles.headless.guard, true)
  assert.deepEqual(state.profiles.headless.searchAvailable, ['deepseek-official', 'searxng', 'claude-code', 'auto'])
  assert.equal(state.profiles.headless.path.endsWith('cordis.patch.yml'), true)
  assert.equal(state.canRestart, true)
  assert.equal(state.searchProviders.auto.deepseek, null)
  assert.equal(state.claude.source, 'fallback')
  assert.equal(state.claude.reason, 'sdk-missing')
  assert.equal(state.searxng.url, 'http://127.0.0.1:9')
  assert.equal(state.searxng.ok, false, 'nothing listens there')
  assert.ok(state.searxng.error)

  // POST guards, in the page's language
  let r = await json(await routes['/api/model-defaults.save'].fetch(new Request(url('/api/model-defaults.save'), { method: 'POST', body: '{}', headers: { 'content-type': 'application/json', 'x-dsh-locale': 'ru' } })))
  assert.deepEqual(r, { status: 403, body: { error: 'Запрос без заголовка панели' } })
  r = await json(await post('/api/model-defaults.save', { selection: { provider: 'deepseek-official', model: 'deepseek-flash' }, apply: { web: true }, deepseek: { models: true } }))
  assert.equal(r.status, 400)
  assert.match(r.body.error, /cannot be the default while DeepSeek is being turned off/)
  r = await json(await post('/api/model-defaults.save', { deepseek: { models: true } }, { 'x-dsh-locale': 'zh' }))
  assert.equal(r.status, 400, 'profiles still default to DeepSeek')
  assert.match(r.body.error, /^配置档 web 的默认模型是 DeepSeek（deepseek-official \/ deepseek-flash）/)

  // one save: new default everywhere, DeepSeek off, SearXNG search, subagents on the local model
  r = await json(await post('/api/model-defaults.save', {
    selection: { provider: 'local-llm', model: 'q', reasoningEffort: 'off' },
    apply: { web: true, headless: true },
    remember: { web: true, headless: true },
    deepseek: { models: true },
    search: { provider: 'searxng', claudeModel: 'haiku', summaryModel: 'auto', summary: true },
    subagents: { kind: 'dsh', ref: { provider: 'local-llm', model: 'q' } },
  }))
  assert.equal(r.status, 200, JSON.stringify(r.body))
  assert.deepEqual(saved.at(-1), { provider: 'local-llm', model: 'q', reasoningEffort: 'off' })
  assert.ok(r.body.notes.some((n) => n.startsWith('web: default model — local-llm / q (reasoning: off)')), r.body.notes.join('\n'))
  assert.ok(r.body.notes.some((n) => n.startsWith('headless: default model — local-llm / q')))
  assert.ok(r.body.notes.some((n) => n.startsWith('Web search: SearXNG, answer written by Auto; DeepSeek search turned off')))
  assert.ok(r.body.notes.includes('Applies from the agent’s next request.'))
  const headless = await fs.readFile(path.join(headlessDir, 'cordis.patch.yml'), 'utf8')
  assert.ok(headless.startsWith('# headless, by hand\n- id: agent-default-model\n  config:\n    provider: local-llm\n    model: q\n    reasoningEffort: off\n'), headless)
  assert.ok(headless.includes('- id: tool-subagent-claude-code\n  disabled: true'))
  assert.ok(headless.includes('- id: tool-subagent\n  config:\n    provider: spawn\n    toolName: subagent\n    agentOptions:\n      provider: local-llm\n      model: q'))
  assert.ok(headless.includes('- id: web\n  config:\n    searchProvider: searxng\n    fetchProvider: http'))
  const web = await fs.readFile(path.join(webDir, 'cordis.patch.yml'), 'utf8')
  assert.ok(web.startsWith(webPatch), 'hand-written rows stay byte for byte')
  for (const id of ['llm-deepseek', 'llm-deepseek-account', 'web-search-deepseek', 'web-deepseek-api']) assert.ok(web.includes(`- id: ${id}\n  disabled: true`), id)
  assert.ok((await fs.readdir(headlessDir)).some((f) => f.startsWith('cordis.patch.yml.bak-model-defaults-')), 'backup kept')
  assert.equal((await fs.readdir(headlessDir)).some((f) => f.endsWith('.lock')), false, 'lock released')
  assert.equal(r.body.state.profiles.headless.subagents.route, 'dsh')
  assert.equal(r.body.state.profiles.web.deepseek.models, true)

  // the ticks are remembered; then nothing changes in the files (the fake catalog still serves DeepSeek: restart hint)
  r = await json(await post('/api/model-defaults.save', { remember: { web: true, headless: false }, deepseek: { models: true }, apply: {} }))
  assert.ok(r.body.notes.includes('headless: keeps its own default model; a model change on this page leaves it alone.'), r.body.notes.join('\n'))
  assert.deepEqual(JSON.parse(await fs.readFile(path.join(webDir, '.model-defaults.json'), 'utf8')), { apply: { web: true, headless: false } })
  r = await json(await post('/api/model-defaults.save', { deepseek: { models: true }, apply: {} }))
  assert.deepEqual(r.body.notes, ['Restart DSH Web to refresh the web UI model list.'])
  assert.equal(r.body.state.restartNeeded, true)

  // a profile that stops composing is rolled back
  await fs.appendFile(path.join(headlessDir, 'cordis.patch.yml'), '# BROKEN\n')
  const beforeBroken = await fs.readFile(path.join(headlessDir, 'cordis.patch.yml'), 'utf8')
  r = await json(await post('/api/model-defaults.save', { deepseek: { models: false }, apply: {} }))
  assert.equal(r.status, 422)
  assert.match(r.body.error, /^Profile headless: does not compose: boom: bad row\. Changes were rolled back\.$/)
  assert.equal(await fs.readFile(path.join(headlessDir, 'cordis.patch.yml'), 'utf8'), beforeBroken)
  await fs.writeFile(path.join(headlessDir, 'cordis.patch.yml'), beforeBroken.replace('# BROKEN\n', ''))

  // test search through the live `web` service; restart is scheduled only with webUnit
  r = await json(await post('/api/model-defaults.test-search', { query: '  node  ' }))
  assert.equal(r.status, 200)
  assert.equal(r.body.query, 'node')
  assert.deepEqual(r.body.sources, [{ url: 'https://a.example', title: 'A' }])
  assert.deepEqual(webSearches.at(-1), { query: 'node', maxResults: 5 })

  // ---- the guard: refuse, let Claude fail, then let the default model stand in
  const { apply: applyGuard } = await import('../../packages/dsh-model-defaults/lib/guard.js')
  const listeners = {}
  let claudePresent = true
  await applyGuard({
    get: (name) => (name === 'profileContext' ? { name: 'web', dir: path.join(tmp, 'no-profile') } : name === 'agentDefaultModel' ? { currentSelection: () => ({ provider: 'local-llm', model: 'q', reasoningEffort: null }) } : undefined),
    effect,
    tools: { get: (name) => (name === 'subagent_claude_code' && claudePresent ? { name } : undefined) },
    on: (event, fn) => { listeners[event] = fn },
  }, {})
  const agent = { session: { requestHeader: () => ({ config: { provider: 'local-llm', model: 'q', maxTokens: 5 } }) } }
  const run = (name, args = { prompt: 'x' }, result = { content: [{ type: 'text', text: 'started subagent child-1' }] }) => {
    let called = false
    return listeners['tools/execute']({ name, arguments: args, agent }, async () => { called = true; return result }).then((out) => ({ out, called }))
  }
  let g = await run('subagent')
  assert.equal(g.called, false)
  assert.equal(g.out.error.info.code, 'SUBAGENT_MODEL_NOT_CHOSEN')
  assert.match(g.out.content[0].text, /^Error: Refused by the owner's subagent setting: delegated work goes to Claude Code/)
  g = await run('bash', { command: 'ls' })
  assert.equal(g.called, true, 'other tools pass through')
  await run('subagent_claude_code', undefined, { content: [{ type: 'text', text: 'started subagent claude-1' }] })
  listeners['subagent/end']({ id: 'claude-1', stopReason: 'error' })
  g = await run('subagent')
  assert.equal(g.called, true, 'after a failed Claude run the default model may stand in')
  await run('subagent_claude_code', undefined, { content: [{ type: 'text', text: 'started subagent claude-2' }] })
  listeners['subagent/end']({ id: 'claude-2', stopReason: 'completed' })
  g = await run('subagent')
  assert.equal(g.called, false, 'Claude works again: the stand-in is closed')
  claudePresent = false
  g = await run('subagent', { prompt: 'x', provider: 'paid', model: 'big' })
  assert.equal(g.called, false, 'Auto: another model is refused')
  g = await run('subagent_fork')
  assert.equal(g.called, true)

  // ---- dsh-web-search-extra: providers, SearXNG with a model answer, Auto falling through DeepSeek
  const sx = http.createServer((req, res) => {
    const u = new URL(req.url, 'http://x')
    if (u.pathname === '/healthz') return res.end('OK')
    res.setHeader('content-type', 'application/json')
    res.end(JSON.stringify({ results: [
      { url: 'https://nodejs.org/en', title: 'Node.js', content: `about ${u.searchParams.get('q')}` },
      { url: 'https://nodejs.org/en/', title: 'dup' },
      { url: 'https://github.com/nodejs/node/releases', title: 'Releases' },
    ] }))
  })
  await new Promise((resolve) => sx.listen(0, '127.0.0.1', resolve))
  const sxURL = `http://127.0.0.1:${sx.address().port}`
  const { apply: applySearch } = await import('../../packages/dsh-web-search-extra/lib/index.js')
  const registered = {}
  const streamed = []
  await applySearch({
    get: (name) => (name === 'agentDefaultModel' ? { currentSelection: () => ({ provider: 'local-llm', model: 'q' }) } : undefined),
    effect,
    llm: {
      resolveModelInfo: async () => ({ reasoning: { efforts: [{ id: 'off' }, { id: 'high' }] } }),
      stream: async function* (options) {
        streamed.push(options)
        yield { type: 'text-delta', text: 'Node.js 26 ' }
        yield { type: 'text-delta', text: 'is LTS [1].' }
      },
    },
    web: {
      registerSearchProvider: (p) => { registered[p.id] = p; return () => delete registered[p.id] },
      searchProviders: new Map([['deepseek-official', { id: 'deepseek-official', available: () => true, search: async () => { throw new Error('quota') } }]]),
    },
  }, { searxngURL: sxURL })
  assert.deepEqual(Object.keys(registered).sort(), ['auto', 'claude-code', 'searxng'])
  assert.equal(registered.searxng.available(), true)
  assert.equal(registered['claude-code'].available(), false, 'no Claude Code here')
  let s = await registered.searxng.search({ query: 'node lts', maxResults: 5 })
  assert.equal(s.content, 'Node.js 26 is LTS [1].')
  assert.deepEqual(s.sources.map((x) => x.url), ['https://nodejs.org/en', 'https://github.com/nodejs/node/releases'])
  assert.equal(streamed[0].reasoningEffort, 'off')
  assert.equal(streamed[0].maxTokens, 500)
  assert.match(streamed[0].messages[0].content[0].text, /Query: "node lts"/)
  s = await registered.auto.search({ query: 'nodejs', maxResults: 1 })
  assert.equal(s.sources.length, 1, 'auto falls through DeepSeek to SearXNG')
  const aborted = new AbortController()
  aborted.abort()
  await assert.rejects(() => registered.searxng.search({ query: 'x' }, aborted.signal))
  await assert.rejects(() => registered['claude-code'].search({ query: 'x' }), /Claude Code not found/)
  sx.close()

  // the model-defaults state now sees SearXNG at its configured address
  assert.ok(lines.some((l) => l.startsWith('[dsh-web-search-extra] providers: searxng, claude-code, auto')), lines.join('\n'))
  assert.ok(lines.some((l) => l.startsWith('[dsh-model-defaults] host plugin loaded (profile web; manages web, headless; overlays 0, restart dsh-web.service)')))
} finally {
  for (const dispose of disposers.reverse()) dispose()
  console.info = log.info
  console.warn = log.warn
  await new Promise((resolve) => setTimeout(resolve, 300))
  await fs.rm(tmp, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 })
}
console.log('shim integration: ok')
