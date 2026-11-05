// Renders the settings page (lib/client.js) with a tiny hook runtime, no browser and no React:
// every language shows complete texts, the state fields are read, and a save sends only what changed.
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'

const source = await fs.readFile(new URL('../lib/client.js', import.meta.url), 'utf8')

/** Just enough of React for function components: state, effects, callbacks. */
function createReact() {
  let hooks = []
  let index = 0
  let effects = []
  const React = {
    createElement: (type, props, ...children) => ({ type, props: props || {}, children }),
    useState(init) {
      const k = index++
      if (!(k in hooks)) hooks[k] = typeof init === 'function' ? init() : init
      return [hooks[k], (value) => { hooks[k] = typeof value === 'function' ? value(hooks[k]) : value }]
    },
    useEffect(fn, deps) {
      const k = index++
      const prev = hooks[k]
      if (!prev || !deps || deps.some((d, j) => d !== prev[j])) {
        hooks[k] = deps || []
        effects.push(fn)
      }
    },
    useCallback(fn, deps) {
      const k = index++
      const prev = hooks[k]
      if (!prev || deps.some((d, j) => d !== prev.deps[j])) hooks[k] = { fn, deps }
      return hooks[k].fn
    },
  }
  const expand = (node) => {
    if (node == null || node === false || node === true) return null
    if (Array.isArray(node)) return node.map(expand)
    if (typeof node !== 'object') return String(node)
    if (typeof node.type === 'function') return expand(node.type({ ...node.props, children: node.children }))
    return { tag: node.type, props: node.props, children: expand(node.children) }
  }
  const render = (Component, props) => {
    index = 0
    effects = []
    const tree = expand(React.createElement(Component, props))
    for (const fn of effects) fn()
    return tree
  }
  return { React, render }
}

const flat = (tree) => (Array.isArray(tree) ? tree.flatMap(flat) : tree && typeof tree === 'object' ? [tree, ...flat(tree.children)] : [])
const text = (tree) => (Array.isArray(tree) ? tree.map(text).join('') : typeof tree === 'string' ? tree : tree ? text(tree.children) : '')
const tick = () => new Promise((resolve) => setTimeout(resolve, 0))

const STATE = {
  running: 'web',
  catalog: {
    groups: [
      { id: 'local-llm', name: 'Local LLM', deepseek: false, models: [{ id: 'local-model', name: 'Local model', reasoning: { efforts: [{ id: 'off', name: 'off' }, { id: 'high', name: 'high' }], defaultEffort: null } }] },
      { id: 'deepseek-official', name: 'DeepSeek', deepseek: true, models: [{ id: 'deepseek-flash', name: 'DeepSeek Flash', reasoning: null }] },
    ],
    failures: [{ id: 'broken', name: 'Broken provider', message: 'boom' }],
  },
  profiles: {
    web: {
      name: 'web', path: '~/.dsh/profiles/web/cordis.patch.yml', running: true,
      searchAvailable: ['deepseek-official', 'searxng', 'claude-code', 'auto'],
      defaultModel: { provider: 'local-llm', model: 'local-model', reasoningEffort: null },
      deepseek: { models: true, search: true, rows: {} }, providers: ['local-llm'],
      search: { provider: 'auto', fetchProvider: 'http', claudeModel: 'claude-haiku-5-5', summaryModel: { provider: 'local-llm', model: 'local-model' }, summary: true },
      subagents: { route: 'claude', claudeCode: { model: 'claude-haiku-5-5', row: true, tool: true }, dsh: 'auto', dshPlace: 'preset' },
    },
    headless: {
      name: 'headless', path: '~/.dsh/profiles/headless/cordis.patch.yml', running: false,
      searchAvailable: ['deepseek-official', 'searxng', 'claude-code', 'auto'],
      defaultModel: { provider: 'local-llm', model: 'local-model', reasoningEffort: 'off' },
      deepseek: { models: true, search: true, rows: {} }, providers: ['local-llm'],
      search: { provider: 'searxng', fetchProvider: 'http', claudeModel: null, summaryModel: null, summary: false },
      subagents: { route: 'claude', claudeCode: { model: 'auto', row: false, tool: true }, dsh: 'auto', dshPlace: 'base' },
    },
  },
  overlays: [{ path: '~/svc/extra.patch.yml', label: 'Service', claudeModel: 'opus' }, { path: '~/x.yml', label: 'X', error: 'bad' }],
  searxng: { url: 'http://127.0.0.1:8888', ok: false, error: 'ECONNREFUSED' },
  restartNeeded: true,
  canRestart: true,
  deepseekRows: {},
  searchProviders: { 'deepseek-official': { label: 'DeepSeek' }, searxng: { label: 'SearXNG' }, 'claude-code': { label: 'Claude' }, auto: { label: 'Auto' } },
  claude: {
    source: 'claude-code',
    models: [
      { id: 'opus', name: 'Opus 5.5', model: 'claude-opus-5-5', description: 'For complex work' },
      { id: 'haiku', name: 'Haiku 5.5', model: 'claude-haiku-5-5', description: 'Fastest' },
      { id: 'claude-opus-4-8', name: 'Opus 4.8', model: 'claude-opus-4-8', description: '' },
    ],
    recommended: { id: 'default', name: 'Default (recommended)', model: 'claude-opus-5-5', description: '' },
  },
}

/** Load the client into a fresh sandbox; returns the registered page and helpers. */
function load({ active, builtIn = ['zh', 'en'], state = STATE }) {
  const { React, render } = createReact()
  const calls = []
  const fetch = async (url, init = {}) => {
    calls.push({ url, init })
    const body = String(url).startsWith('/api/model-defaults.state') ? state : { state, notes: ['saved'] }
    return { ok: true, status: 200, json: async () => structuredClone(body) }
  }
  let mod = null
  vm.runInNewContext(source, {
    window: { __ModuleLoader__: { load: ({ factory }) => { mod = factory((name) => (name === 'react' ? React : null)) } } },
    fetch, setTimeout, console,
  })
  let dicts = null
  let added = null
  let slot = null
  const ctx = {
    effect: (fn) => fn(),
    locale: {
      register: (ns, value) => { dicts = value; return () => {} },
      addLanguage: (lang) => { added = lang; return () => {} },
      getSnapshot: () => ({ active, locales: builtIn.map((id) => ({ id })), revision: 1 }),
      bind: () => t,
    },
    slots: { inject: (name, fn) => fn(), register: (def, Component) => { slot = { def, Component }; return () => {} } },
  }
  function t(key, params) {
    const template = dicts[active]?.[key] ?? dicts.en[key] ?? key
    return params ? template.replace(/\{(\w+)\}/g, (m, n) => (n in params ? String(params[n]) : m)) : template
  }
  assert.deepEqual([...mod.inject], ['slots', 'locale'])
  mod.apply(ctx)
  return { slot, dicts, added, calls, t, page: () => render(slot.Component, { t }) }
}

// ---- dictionaries: same keys in every language, placeholders kept
{
  const { dicts } = load({ active: 'en' })
  const keys = Object.keys(dicts.en).sort()
  for (const locale of ['ru', 'zh']) {
    assert.deepEqual(Object.keys(dicts[locale]).sort(), keys, `${locale}: same keys as en`)
    for (const key of keys) {
      const want = (dicts.en[key].match(/\{\w+\}/g) || []).sort().join()
      assert.equal((dicts[locale][key].match(/\{\w+\}/g) || []).sort().join(), want, `${locale}.${key}: placeholders`)
    }
  }
}

// ---- every language renders complete texts
for (const active of ['en', 'ru', 'zh']) {
  const app = load({ active })
  assert.equal(app.slot.def.name, 'settings.section')
  assert.equal(app.slot.def.locale, 'modelDefaults')
  assert.equal(app.slot.def.label(), app.dicts[active].nav)
  assert.equal(text(app.page()), app.dicts[active].loading)
  await tick()
  const tree = app.page()
  const all = text(tree)
  const nodes = flat(tree)
  assert.ok(all.includes(app.dicts[active].title), `${active}: title`)
  assert.ok(!/\{\w+\}/.test(all), `${active}: no unfilled placeholder in: ${all.match(/.{0,30}\{\w+\}.{0,30}/)?.[0]}`)
  for (const key of Object.keys(app.dicts.en).filter((k) => k.includes('.'))) assert.ok(!all.includes(key), `${active}: raw key ${key}`)
  assert.equal(app.calls[0].init.headers['x-dsh-locale'], active, 'the page language goes to the host')
  // DeepSeek is off: its group is hidden from model choices
  const options = nodes.filter((n) => n.tag === 'option').map((n) => n.props.value)
  assert.ok(!options.some((v) => String(v).startsWith('deepseek-official\u0001')), `${active}: DeepSeek models hidden`)
  // Claude: the stored full id is matched to its alias and stays selectable; the default is named
  const selects = nodes.filter((n) => n.tag === 'select')
  const claudeSelect = selects.find((s) => flat(s.children).some((o) => o.props?.value === 'haiku'))
  assert.equal(claudeSelect.props.value, 'claude-haiku-5-5')
  assert.ok(text(claudeSelect).includes('Haiku 5.5 (claude-haiku-5-5)'))
  assert.ok(text(claudeSelect).includes('Opus 5.5'), `${active}: Auto names Claude Code's default`)
  const subSelect = selects.find((s) => String(s.props.value).startsWith('claude:'))
  assert.equal(subSelect.props.value, 'claude:claude-haiku-5-5')
  // SearXNG option names the answer model; the warning shows SearXNG is down; overlays appear
  assert.ok(all.includes('SearXNG + Local model'))
  assert.ok(all.includes('ECONNREFUSED'))
  assert.ok(all.includes('Service: Claude Code, Opus 5.5') || all.includes('Service：Claude Code，Opus 5.5'), `${active}: overlay line`)
  assert.ok(!all.includes('/reset'), `${active}: no chat hints`)
  // nothing changed yet: Save is disabled
  const save = nodes.find((n) => n.tag === 'button' && text(n) === app.dicts[active].save)
  assert.equal(save.props.disabled, true)
  assert.deepEqual({ ...app.added }, { id: 'ru', label: 'Русский', fallback: 'en' }, 'Russian is added to a DSH without it')
}

// ---- DSH that already ships Russian: no second language is added
assert.equal(load({ active: 'ru', builtIn: ['zh', 'en', 'ru'] }).added, null)

// ---- a save sends only the changed part, then shows the host's notes
{
  const app = load({ active: 'en' })
  app.page()
  await tick()
  let nodes = flat(app.page())
  const subSelect = nodes.filter((n) => n.tag === 'select').find((s) => String(s.props.value).startsWith('claude:'))
  subSelect.props.onChange({ target: { value: 'auto' } })
  nodes = flat(app.page())
  const save = nodes.find((n) => n.tag === 'button' && text(n) === 'Save')
  assert.equal(save.props.disabled, false)
  save.props.onClick()
  await tick()
  const post = app.calls.find((c) => c.url === '/api/model-defaults.save')
  assert.equal(post.init.headers['x-dsh-model-defaults'], '1')
  const body = JSON.parse(post.init.body)
  assert.deepEqual(body.subagents, { kind: 'dsh', ref: 'auto' })
  assert.equal(body.selection, null)
  assert.equal(body.search, null)
  assert.deepEqual(body.deepseek, { models: true })
  assert.ok(text(app.page()).includes('saved'))
}

// ---- search change: provider and Claude model are sent together
{
  const app = load({ active: 'en' })
  app.page()
  await tick()
  let nodes = flat(app.page())
  // the provider is one dropdown; the chosen option's hint is shown under it
  const providerSelect = () => flat(app.page()).filter((n) => n.tag === 'select').find((s) => flat(s.children).some((o) => o.props?.value === 'claude-code'))
  assert.equal(providerSelect().props.value, 'auto')
  assert.deepEqual(flat(providerSelect().children).filter((o) => o.tag === 'option').map((o) => o.props.value), ['deepseek-official', 'searxng', 'claude-code', 'auto'])
  assert.ok(!flat(app.page()).some((n) => n.tag === 'input' && n.props.type === 'radio'), 'no radio buttons left')
  assert.ok(text(app.page()).includes('Tries DeepSeek → SearXNG → Claude'))
  providerSelect().props.onChange({ target: { value: 'claude-code' } })
  assert.ok(text(app.page()).includes('Claude runs the query with its WebSearch tool'))
  assert.ok(!text(app.page()).includes('Tries DeepSeek → SearXNG → Claude'), 'only the chosen hint')
  nodes = flat(app.page())
  const claudeSelect = nodes.filter((n) => n.tag === 'select').find((s) => flat(s.children).some((o) => o.props?.value === 'haiku') && !String(s.props.value).startsWith('claude:'))
  claudeSelect.props.onChange({ target: { value: 'auto' } })
  nodes = flat(app.page())
  nodes.find((n) => n.tag === 'button' && text(n) === 'Save').props.onClick()
  await tick()
  const body = JSON.parse(app.calls.find((c) => c.url === '/api/model-defaults.save').init.body)
  assert.deepEqual(body.search, { provider: 'claude-code', claudeModel: 'auto', summaryModel: { provider: 'local-llm', model: 'local-model' }, summary: true })
  assert.equal(body.subagents, null)
}

// ---- search plugin missing in a profile: its options say so; an unknown provider stays shown
{
  const headless = { ...STATE.profiles.headless, searchAvailable: ['deepseek-official'], search: { ...STATE.profiles.headless.search, provider: 'my-search' } }
  const state = { ...STATE, profiles: { web: { ...STATE.profiles.web, search: { ...STATE.profiles.web.search, provider: 'my-search' } }, headless } }
  const app = load({ active: 'en', state })
  app.page()
  await tick()
  const select = flat(app.page()).filter((n) => n.tag === 'select').find((s) => flat(s.children).some((o) => o.props?.value === 'claude-code'))
  const labels = flat(select.children).filter((o) => o.tag === 'option').map((o) => text(o))
  assert.equal(select.props.value, 'my-search')
  assert.equal(labels[0], 'DeepSeek')
  assert.ok(labels[2].endsWith('— not installed'), labels[2])
  assert.equal(labels[4], 'my-search')
}

// ---- the Claude list could not be read: the page says so and offers a refresh
{
  const state = { ...STATE, claude: { source: 'fallback', reason: 'sdk-missing', models: [{ id: 'opus', name: 'Opus', model: null, description: '' }], recommended: null } }
  const app = load({ active: 'en', state })
  app.page()
  await tick()
  const tree = app.page()
  assert.ok(text(tree).includes('Claude Code did not report its models (sdk-missing)'))
  const refresh = flat(tree).find((n) => n.tag === 'button' && text(n) === 'Refresh')
  refresh.props.onClick()
  await tick()
  assert.ok(app.calls.some((c) => c.url === '/api/model-defaults.state?refreshClaude=1'))
}

// ---- no restart unit configured: the page asks for a manual restart and shows no button
{
  const app = load({ active: 'en', state: { ...STATE, canRestart: false } })
  app.page()
  await tick()
  const tree = app.page()
  assert.ok(text(tree).includes('Restart DSH Web to apply it.'))
  assert.ok(!flat(tree).some((n) => n.tag === 'button' && text(n) === 'Restart DSH Web'))
}

console.log('dsh-model-defaults client: ok')
