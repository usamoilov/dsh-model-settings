/**
 * Host half of dsh-model-defaults: one settings page for the models DSH uses —
 * the default model of every profile (the web UI's and headless ones), the web
 * search provider, the subagent model — and a switch that turns DeepSeek off
 * everywhere.
 *
 * Routes (authenticated `/api`, same session cookie as the web UI):
 *   GET  /api/model-defaults.state        — catalog and everything the page shows
 *   POST /api/model-defaults.save         — apply changes (only the parts sent)
 *   POST /api/model-defaults.restart      — restart the web UI unit (option `webUnit`)
 *   POST /api/model-defaults.test-search  — one real query through the live `web` service
 * POSTs require the `x-dsh-model-defaults: 1` header and a JSON body, so a
 * cross-site form cannot trigger them. Messages follow the page's locale
 * (`x-dsh-locale`: en, ru, zh).
 *
 * The running profile's default goes through DSH's own `agentDefaultModel`
 * service, which also re-applies the profile file to the live tree; other
 * profiles (and overlays) are edited on disk and apply to the next
 * `dsh --profile <name>` run.
 *
 * Config (row `model-defaults`, all optional):
 *   profiles      profile names to manage (default: the running one and `headless`, those that exist)
 *   webUnit       systemd --user unit of the web UI, enables the restart button
 *   overlays      [{ path, label, profile, patches }] extra `--patch` files that restate
 *                 `subagent-claude-code` (kept in sync with the Claude model)
 *   searxngURL    SearXNG address shown on the page (default http://127.0.0.1:8888)
 */
import { spawn } from 'node:child_process'
import fs from 'node:fs/promises'
import { existsSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { CLAUDE_MODEL_PATTERN, createClaudeModels, findClaudeSdk } from './claude-models.js'
import { failure, localeOf, translator } from './i18n.js'
import {
  AUTO, DEEPSEEK_ROWS, SEARCH_PROVIDERS, applyProfile, checkComposes, deepseekSearchEnabled,
  editPatchFile, editProfile, isDeepseekProvider, readProfile, subagentChanges,
} from './profiles.js'

export const name = 'dsh-model-defaults'
export const inject = ['connection', 'agentDefaultModel', 'sessionController']

export const STATE_ROUTE = '/api/model-defaults.state'
export const SAVE_ROUTE = '/api/model-defaults.save'
export const RESTART_ROUTE = '/api/model-defaults.restart'
export const TEST_SEARCH_ROUTE = '/api/model-defaults.test-search'
const NO_STORE = { 'cache-control': 'no-store' }
const DEFAULT_SEARXNG = 'http://127.0.0.1:8888'

/** Packages linked into a profile (its package.json dependencies). */
async function profilePackages(dir) {
  try {
    return Object.keys(JSON.parse(await fs.readFile(path.join(dir, 'package.json'), 'utf8')).dependencies || {})
  } catch {
    return []
  }
}

const expandHome = (value) => String(value).replace(/^~(?=\/|$)/, os.homedir())

function tildify(file) {
  const home = os.homedir()
  return file.startsWith(`${home}/`) ? `~/${file.slice(home.length + 1)}` : file
}

function stamp() {
  const d = new Date()
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/** Normalize the optional plugin config. */
export function readConfig(raw) {
  const cfg = raw && typeof raw === 'object' ? raw : {}
  const list = (value) => (Array.isArray(value) ? value : [])
  return {
    profiles: list(cfg.profiles).filter((p) => typeof p === 'string' && /^[\w.-]+$/.test(p)),
    webUnit: typeof cfg.webUnit === 'string' && /^[\w@.-]+\.service$/.test(cfg.webUnit) ? cfg.webUnit : null,
    overlays: list(cfg.overlays)
      .filter((o) => o && typeof o.path === 'string')
      .map((o) => ({
        path: expandHome(o.path),
        label: typeof o.label === 'string' && o.label ? o.label : path.basename(o.path),
        profile: typeof o.profile === 'string' && o.profile ? o.profile : 'headless',
        patches: list(o.patches).filter((p) => typeof p === 'string').map(expandHome),
      })),
    searxngURL: typeof cfg.searxngURL === 'string' && cfg.searxngURL ? cfg.searxngURL : DEFAULT_SEARXNG,
  }
}

/** The composed tree declares this provider route (pi-ai dict key, or DeepSeek's built-in routes). */
function providerDeclared(dump, provider) {
  if (isDeepseekProvider(provider)) return true
  const escaped = provider.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp(`^\\s+${escaped}:\\s*$`, 'm').test(dump)
}

export function apply(ctx, rawConfig = {}) {
  const config = readConfig(rawConfig)
  let profileContext = null
  try {
    profileContext = ctx.get('profileContext')
  } catch {
    profileContext = null
  }
  const running = profileContext?.name || 'web'
  const runningDir = profileContext?.dir || path.join(process.env.DSH_HOME || path.join(os.homedir(), '.dsh'), 'profiles', running)
  const profilesRoot = path.dirname(runningDir)
  const wanted = config.profiles.length ? config.profiles : [running, 'headless']
  const profiles = [...new Set(wanted)]
    .map((profile) => ({ name: profile, dir: profile === running ? runningDir : path.join(profilesRoot, profile) }))
    .filter((profile) => existsSync(profile.dir))
  const claudeModels = createClaudeModels({ sdkFile: findClaudeSdk(runningDir) })
  let queue = Promise.resolve()

  const label = (t, selection) => {
    if (!selection?.provider) return '—'
    return `${selection.provider} / ${selection.model}${selection.reasoningEffort ? ` (${t('effort', { effort: selection.reasoningEffort })})` : ''}`
  }
  const refLabel = (t, ref) => (ref === AUTO ? t('auto') : label(t, ref))

  async function catalog(t) {
    try {
      const value = await ctx.sessionController.modelCatalog()
      return { groups: value.groups || [], failures: value.failures || [] }
    } catch (error) {
      return { groups: [], failures: [{ id: '—', name: t('catalog.name'), message: error?.message || String(error) }] }
    }
  }

  async function readProfiles() {
    const out = {}
    for (const profile of profiles) {
      const file = path.join(profile.dir, 'cordis.patch.yml')
      let info
      try {
        info = readProfile(await fs.readFile(file, 'utf8').catch(() => ''))
      } catch (error) {
        info = { error: error.message, errorKey: error.key, errorParams: error.params, defaultModel: null, deepseek: { models: false, search: false, rows: {} }, providers: [], search: {}, subagents: {} }
      }
      const packages = await profilePackages(profile.dir)
      const searchAvailable = Object.entries(SEARCH_PROVIDERS)
        .filter(([, meta]) => !meta.plugin || packages.includes(meta.plugin))
        .map(([id]) => id)
      out[profile.name] = { name: profile.name, path: tildify(file), running: profile.name === running, searchAvailable, ...info }
    }
    if (out[running]) {
      // the live service is the truth for the running profile
      const live = ctx.agentDefaultModel.currentSelection()
      out[running].defaultModel = { provider: live.provider, model: live.model, reasoningEffort: live.reasoningEffort ?? null }
    }
    return out
  }

  async function readOverlays() {
    const out = []
    for (const overlay of config.overlays) {
      if (!existsSync(overlay.path)) continue
      try {
        const info = readProfile(await fs.readFile(overlay.path, 'utf8'))
        out.push({ path: tildify(overlay.path), label: overlay.label, claudeModel: info.subagents.claudeCode.model, row: info.subagents.claudeCode.row })
      } catch (error) {
        out.push({ path: tildify(overlay.path), label: overlay.label, error: error.message })
      }
    }
    return out
  }

  async function searxngHealth() {
    const started = Date.now()
    try {
      const response = await fetch(new URL('/healthz', config.searxngURL), { signal: AbortSignal.timeout(2500) })
      return { url: config.searxngURL, ok: response.ok, ms: Date.now() - started }
    } catch (error) {
      return { url: config.searxngURL, ok: false, error: error?.message || String(error) }
    }
  }

  /**
   * The running profile's file and its live provider list disagree about DeepSeek:
   * the file turns it off but a DeepSeek provider is still served, or the file
   * turns the API-key provider on but `deepseek-official` is not served.
   */
  function needsRestart(profileState, cat) {
    if (!profileState || profileState.error) return false
    const live = [...cat.groups, ...cat.failures].map((g) => g.id)
    if (profileState.deepseek.models) return live.some(isDeepseekProvider)
    return profileState.deepseek.rows?.['llm-deepseek'] !== true && !live.includes('deepseek-official')
  }

  async function state(t, { refreshClaude = false } = {}) {
    const [cat, profileStates, claude] = await Promise.all([catalog(t), readProfiles(), claudeModels.list({ refresh: refreshClaude })])
    for (const p of Object.values(profileStates)) if (p.errorKey) p.error = t(p.errorKey, p.errorParams)
    return {
      running,
      catalog: {
        groups: cat.groups.map((group) => ({
          id: group.id,
          name: group.name,
          deepseek: isDeepseekProvider(group.id),
          models: (group.models || []).map((model) => ({
            id: model.id,
            name: model.name || model.id,
            reasoning: model.reasoning
              ? { efforts: (model.reasoning.efforts || []).map((e) => ({ id: e.id, name: e.name || e.id })), defaultEffort: model.reasoning.defaultEffort ?? null }
              : null,
          })),
        })),
        failures: cat.failures.map((f) => ({ id: f.id, name: f.name, message: f.message })),
      },
      profiles: profileStates,
      overlays: await readOverlays(),
      searxng: await searxngHealth(),
      restartNeeded: needsRestart(profileStates[running], cat),
      canRestart: Boolean(config.webUnit) && running === 'web',
      deepseekRows: DEEPSEEK_ROWS,
      searchProviders: Object.fromEntries(Object.entries(SEARCH_PROVIDERS).map(([id, meta]) => [id, { label: meta.label, deepseek: meta.deepseek }])),
      claude,
    }
  }

  function parseSelection(raw) {
    if (raw == null) return null
    if (typeof raw !== 'object' || typeof raw.provider !== 'string' || typeof raw.model !== 'string' || !raw.provider || !raw.model) {
      throw failure(400, 'err.noModel')
    }
    const effort = raw.reasoningEffort
    if (effort != null && (typeof effort !== 'string' || !/^[\w-]{1,32}$/.test(effort))) throw failure(400, 'err.effort')
    return { provider: raw.provider, model: raw.model, ...(effort ? { reasoningEffort: effort } : {}) }
  }

  /** 'auto' or { provider, model } (no effort). */
  function parseRef(raw, whatKey) {
    if (raw === AUTO) return AUTO
    if (raw && typeof raw === 'object' && typeof raw.provider === 'string' && raw.provider && typeof raw.model === 'string' && raw.model) {
      return { provider: raw.provider, model: raw.model }
    }
    throw Object.assign(failure(400, 'err.noRef'), { whatKey })
  }

  function parseClaudeModel(raw) {
    if (raw !== AUTO && !(typeof raw === 'string' && CLAUDE_MODEL_PATTERN.test(raw))) throw failure(400, 'err.claudeModel')
    return raw
  }

  function parseSearch(raw) {
    if (raw == null) return null
    if (typeof raw !== 'object' || !Object.hasOwn(SEARCH_PROVIDERS, raw.provider)) throw failure(400, 'err.searchProvider')
    const out = { provider: raw.provider }
    if (raw.claudeModel != null) out.claudeModel = parseClaudeModel(raw.claudeModel)
    if (raw.summaryModel != null) out.summaryModel = parseRef(raw.summaryModel, 'what.answer')
    if (raw.summary != null) out.summary = raw.summary === true
    return out
  }

  /** One choice for every delegated task: { kind: 'claude', model } or { kind: 'dsh', ref }. */
  function parseSubagents(raw) {
    if (raw == null) return null
    if (typeof raw !== 'object') throw failure(400, 'err.subagents')
    if (raw.kind === 'claude') return { kind: 'claude', model: parseClaudeModel(raw.model) }
    if (raw.kind === 'dsh') return { kind: 'dsh', ref: parseRef(raw.ref, 'what.subagent') }
    throw Object.assign(failure(400, 'err.noRef'), { whatKey: 'what.subagent' })
  }

  async function claudeName(model) {
    if (!model || model === AUTO) return null
    const { models } = await claudeModels.list()
    return models.find((m) => m.id === model || m.model === model)?.name || model
  }

  function inCatalog(cat, ref) {
    const group = cat.groups.find((g) => g.id === ref.provider)
    return group?.models?.find((m) => m.id === ref.model) || null
  }

  async function save(body, t) {
    const selection = parseSelection(body?.selection)
    const search = parseSearch(body?.search)
    const subagents = parseSubagents(body?.subagents)
    const targets = profiles.filter((profile) => body?.apply?.[profile.name] === true)
    const deepseekOff = { models: body?.deepseek?.models === true }
    const before = await readProfiles()
    const cat = await catalog(t)

    // ---- validate everything before writing anything
    if (selection && !targets.length) throw failure(400, 'err.applyWhere')
    if (selection) {
      if (deepseekOff.models && isDeepseekProvider(selection.provider)) throw failure(400, 'err.deepseekBoth')
      const model = inCatalog(cat, selection)
      if (targets.some((p) => p.name === running) && !model) throw failure(400, 'err.notInWebCatalog', { model: `${selection.provider} / ${selection.model}` })
      if (selection.reasoningEffort && model?.reasoning && !model.reasoning.efforts.some((e) => e.id === selection.reasoningEffort)) {
        throw failure(400, 'err.effortUnsupported', { effort: selection.reasoningEffort })
      }
    }
    if (search) {
      for (const profile of profiles) {
        if (!before[profile.name]?.searchAvailable?.includes(search.provider)) {
          const meta = SEARCH_PROVIDERS[search.provider]
          throw failure(400, 'err.searchNotInstalled', { label: meta.label, profile: profile.name, plugin: meta.plugin })
        }
      }
      if (search.summaryModel && search.summaryModel !== AUTO) {
        if (!inCatalog(cat, search.summaryModel)) throw failure(400, 'err.notInCatalog', { model: label(t, search.summaryModel) })
        if (deepseekOff.models && isDeepseekProvider(search.summaryModel.provider)) throw failure(400, 'err.answerDeepseek')
      }
    }
    const subRef = subagents?.kind === 'dsh' && subagents.ref !== AUTO ? subagents.ref : null
    if (subRef) {
      if (!inCatalog(cat, subRef)) throw failure(400, 'err.notInCatalog', { model: label(t, subRef) })
      if (deepseekOff.models && isDeepseekProvider(subRef.provider)) throw failure(400, 'err.subDeepseek')
    }
    for (const profile of profiles) {
      const willSet = selection && targets.includes(profile)
      const current = before[profile.name]
      if (current?.error) throw failure(422, 'err.profile', { profile: profile.name, error: current.errorKey ? t(current.errorKey, current.errorParams) : current.error })
      if (deepseekOff.models && !willSet && isDeepseekProvider(current?.defaultModel?.provider)) {
        throw failure(400, 'err.defaultDeepseek', { profile: profile.name, model: label(t, current.defaultModel) })
      }
      if (deepseekOff.models && !subagents && current?.subagents?.route === 'dsh' && current?.subagents?.dsh !== AUTO && isDeepseekProvider(current?.subagents?.dsh?.provider)) {
        throw failure(400, 'err.profileSubDeepseek', { profile: profile.name })
      }
    }

    // ---- write
    const searchProvider = search?.provider ?? before[running]?.search?.provider ?? 'deepseek-official'
    const searchRowsOff = !deepseekSearchEnabled(searchProvider, deepseekOff.models)
    const disable = Object.fromEntries([
      ...DEEPSEEK_ROWS.models.map((id) => [id, deepseekOff.models]),
      ...DEEPSEEK_ROWS.search.map((id) => [id, searchRowsOff]),
    ])
    const when = stamp()
    const notes = []
    const changes = { disable, search, ...subagentChanges(subagents), stamp: when, t }
    for (const profile of profiles) {
      const willSet = selection && targets.includes(profile)
      const prior = before[profile.name]
      if (profile.name === running) {
        await editProfile(profile, (text) => applyProfile(text, changes), { t })
        // DSH's own writer: persists the default and re-applies the profile file to the live tree
        const next = willSet ? selection : ctx.agentDefaultModel.currentSelection()
        await ctx.agentDefaultModel.saveSelection({
          provider: next.provider,
          model: next.model,
          ...(next.reasoningEffort ? { reasoningEffort: next.reasoningEffort } : {}),
        })
        if (willSet) notes.push(t('note.default', { profile: profile.name, model: label(t, selection) }))
      } else {
        const verify = (dump) => {
          if (search && SEARCH_PROVIDERS[search.provider].plugin && !/^- id: web-search-extra$/m.test(dump)) return t('verify.noExtra')
          if (willSet && !providerDeclared(dump, selection.provider)) return t('verify.noProvider', { provider: selection.provider })
          if (subRef && !providerDeclared(dump, subRef.provider)) return t('verify.noProviderSub', { provider: subRef.provider })
          if (search?.summaryModel && search.summaryModel !== AUTO && !providerDeclared(dump, search.summaryModel.provider)) {
            return t('verify.noProviderAnswer', { provider: search.summaryModel.provider })
          }
          return null
        }
        const result = await editProfile(profile, (text) => applyProfile(text, { ...changes, defaultModel: willSet ? selection : undefined }), { verify, t })
        if (result.changed && willSet) notes.push(t('note.defaultNext', { profile: profile.name, model: label(t, selection) }))
      }
      if (prior && prior.deepseek.models !== deepseekOff.models) notes.push(t(deepseekOff.models ? 'note.deepseekOff' : 'note.deepseekOn', { profile: profile.name }))
    }

    const updatedOverlays = []
    if (subagents?.kind === 'claude') {
      // overlays restate the provider row (model next to e.g. bypassPermissions); the tool row stays in the profile
      for (const overlay of config.overlays) {
        if (!existsSync(overlay.path)) continue
        await editPatchFile({
          file: overlay.path,
          lock: overlay.path,
          compose: () => checkComposes(overlay.profile, { patches: [...overlay.patches.filter((p) => existsSync(p)), overlay.path] }),
          label: overlay.label,
        }, (text) => applyProfile(text, { claudeSubagentModel: subagents.model, stamp: when, t }), { t })
        updatedOverlays.push(overlay.label)
      }
    }

    if (search) {
      const meta = SEARCH_PROVIDERS[search.provider]
      let extra = ''
      if (search.provider === 'claude-code') {
        const model = search.claudeModel || before[running]?.search?.claudeModel
        extra = t('note.search.claude', { model: (await claudeName(model)) || t('auto') })
      } else if (search.provider === 'searxng') {
        const summaryRef = search.summaryModel || before[running]?.search?.summaryModel || AUTO
        extra = search.summary === false ? t('note.search.noAnswer') : t('note.search.answer', { model: refLabel(t, summaryRef) })
      } else if (search.provider === AUTO) {
        extra = t('note.search.auto')
      }
      notes.push(t('note.search', { label: search.provider === AUTO ? t('auto') : meta.label, extra, deepseek: searchRowsOff ? t('note.search.deepseekOff') : '' }))
    }
    if (subagents?.kind === 'claude') {
      const name = await claudeName(subagents.model)
      notes.push(t('note.subClaude', {
        label: name ? t('sub.claude', { model: name }) : t('sub.claudeAuto'),
        overlays: updatedOverlays.length ? t('note.overlays', { list: updatedOverlays.join(', ') }) : '',
      }))
    } else if (subagents) {
      notes.push(t('note.subDsh', { label: subagents.ref === AUTO ? t('sub.auto') : label(t, subagents.ref) }))
    }
    if (subagents) notes.push(t('note.subNext'))

    const after = await state(t)
    if (after.restartNeeded) notes.push(t('note.restart'))
    if (!notes.length) notes.push(t('note.none'))
    return { state: after, notes }
  }

  const guardPost = async (request) => {
    if (request.headers.get('x-dsh-model-defaults') !== '1') throw failure(403, 'err.header')
    if (!String(request.headers.get('content-type') || '').includes('application/json')) throw failure(415, 'err.json')
    try {
      return await request.json()
    } catch {
      throw failure(400, 'err.badJson')
    }
  }

  function respondError(t, error, fallbackKey) {
    const status = Number.isInteger(error?.status) ? error.status : 500
    if (status === 500) console.warn('[dsh-model-defaults]', fallbackKey, error instanceof Error ? error.stack : error)
    let message
    if (error?.key) message = t(error.key, { ...(error.params || {}), ...(error.whatKey ? { what: t(error.whatKey) } : {}) })
    else message = status === 500 ? `${t(fallbackKey)}: ${error?.message || error}` : error?.message
    return Response.json({ error: message }, { status, headers: NO_STORE })
  }

  ctx.effect(() => ctx.connection.fetch.register({
    path: STATE_ROUTE,
    methods: ['GET'],
    requestBody: 'buffered',
    fetch: async (request) => {
      const t = translator(localeOf(request))
      try {
        const refreshClaude = new URL(request.url).searchParams.get('refreshClaude') === '1'
        return Response.json(await state(t, { refreshClaude }), { headers: NO_STORE })
      } catch (error) {
        return respondError(t, error, 'err.state')
      }
    },
  }), 'dsh-model-defaults: state route')

  ctx.effect(() => ctx.connection.fetch.register({
    path: SAVE_ROUTE,
    methods: ['POST'],
    requestBody: 'buffered',
    fetch: async (request) => {
      const t = translator(localeOf(request))
      try {
        const body = await guardPost(request)
        // one save at a time
        const run = queue.then(() => save(body, t))
        queue = run.catch(() => {})
        return Response.json(await run, { headers: NO_STORE })
      } catch (error) {
        return respondError(t, error, 'err.save')
      }
    },
  }), 'dsh-model-defaults: save route')

  ctx.effect(() => ctx.connection.fetch.register({
    path: TEST_SEARCH_ROUTE,
    methods: ['POST'],
    requestBody: 'buffered',
    fetch: async (request) => {
      const t = translator(localeOf(request))
      try {
        const body = await guardPost(request)
        let web = null
        try {
          web = ctx.get('web')
        } catch {
          web = null
        }
        if (!web?.search) throw failure(409, 'err.searchService')
        const query = typeof body?.query === 'string' && body.query.trim() ? body.query.trim().slice(0, 200) : 'latest stable Node.js version'
        const started = Date.now()
        const result = await web.search({ query, maxResults: 5 }, AbortSignal.timeout(150000))
        return Response.json({
          query,
          ms: Date.now() - started,
          content: typeof result.content === 'string' ? result.content.slice(0, 800) : null,
          sources: (result.sources || []).slice(0, 5).map((s) => ({ url: s.url, title: s.title || null })),
        }, { headers: NO_STORE })
      } catch (error) {
        if (error?.key) return respondError(t, error, 'err.searchService')
        const status = Number.isInteger(error?.status) ? error.status : 502
        return Response.json({ error: t('err.search', { error: `${error?.code ? `${error.code}: ` : ''}${error?.message || error}` }) }, { status, headers: NO_STORE })
      }
    },
  }), 'dsh-model-defaults: test search route')

  ctx.effect(() => ctx.connection.fetch.register({
    path: RESTART_ROUTE,
    methods: ['POST'],
    requestBody: 'buffered',
    fetch: async (request) => {
      const t = translator(localeOf(request))
      try {
        await guardPost(request)
        if (running !== 'web') throw failure(409, 'err.restartWebOnly')
        if (!config.webUnit) throw failure(409, 'err.restartOff')
        setTimeout(() => {
          // --no-block: systemd restarts the unit on its own; this process will be stopped
          const child = spawn('systemctl', ['--user', '--no-block', 'restart', config.webUnit], { detached: true, stdio: 'ignore' })
          child.on('error', (error) => console.warn('[dsh-model-defaults] restart failed:', error.message))
          child.unref()
        }, 500)
        return Response.json({ ok: true }, { headers: NO_STORE })
      } catch (error) {
        return respondError(t, error, 'err.restart')
      }
    },
  }), 'dsh-model-defaults: restart route')

  console.info(`[dsh-model-defaults] host plugin loaded (profile ${running}; manages ${profiles.map((p) => p.name).join(', ')}; ` +
    `overlays ${config.overlays.length}, restart ${config.webUnit || 'off'})`)
}
