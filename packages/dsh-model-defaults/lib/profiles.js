/**
 * Read and edit DSH profile patches (`~/.dsh/profiles/<name>/cordis.patch.yml`)
 * and optional overlays (extra `--patch` files a service runs with) that
 * restate the Claude Code subagent row.
 *
 * Edits go through the `yaml` Document API, so comments, `!!js` expressions and
 * hand-written rows survive. Only these places are touched:
 *   - `agent-default-model` → config (provider, model, reasoningEffort);
 *   - DeepSeek rows (`llm-deepseek`, `llm-deepseek-account`, `web-search-deepseek`,
 *     `web-deepseek-api`) → a `disabled: true` flag, added or removed;
 *   - `web` → searchProvider (+ fetchProvider restated), `web-search-extra` → its config;
 *   - `subagent-claude-code` → model (removed for "auto");
 *   - `tool-subagent-claude-code` (the global `subagent_claude_code` tool) → a `disabled: true`
 *     flag when the subagents run on a DSH model instead of Claude;
 *   - the spawn `tool-subagent` (top level, or nested in a preset of the patch) → agentOptions.
 * A profile row's `config` replaces the base row's whole config, so every edit
 * keeps the keys already in the row and restates what the base row had.
 */
import { execFile } from 'node:child_process'
import fs from 'node:fs/promises'
import path from 'node:path'
import { dshEntry, loadAtomicWrite, loadYaml } from './deps.js'
import { failure, translator } from './i18n.js'

export const DEFAULT_MODEL_ID = 'agent-default-model'
export const WEB_ID = 'web'
/** Row of the extra search providers plugin (dsh-web-search-extra). */
export const EXTRA_SEARCH_ID = 'web-search-extra'
export const CLAUDE_SUBAGENT_ID = 'subagent-claude-code'
/** Host row of the Claude Code bundle that exposes `subagent_claude_code` to every preset. */
export const CLAUDE_TOOL_ID = 'tool-subagent-claude-code'
export const TOOL_SUBAGENT_ID = 'tool-subagent'
/** "Auto" everywhere a model is chosen. */
export const AUTO = 'auto'
/** Search providers the settings page offers. */
export const SEARCH_PROVIDERS = {
  'deepseek-official': { label: 'DeepSeek', deepseek: true },
  searxng: { label: 'SearXNG', deepseek: false, plugin: 'dsh-web-search-extra' },
  'claude-code': { label: 'Claude', deepseek: false, plugin: 'dsh-web-search-extra' },
  auto: { label: 'Auto', deepseek: null, plugin: 'dsh-web-search-extra' },
}
/** Earlier provider ids, still registered by dsh-web-search-extra and read as their new names. */
/** Base composition of the `web` row (dsh-base): search through DeepSeek, fetch over plain HTTP. */
export const WEB_BASE = { searchProvider: 'deepseek-official', fetchProvider: 'http' }
/** Base config of the top-level spawn subagent tool (dsh-base). */
export const TOOL_SUBAGENT_BASE = { provider: 'spawn', toolName: 'subagent' }
/** Profile rows that bring DeepSeek in: model providers, and web search through the DeepSeek API. */
export const DEEPSEEK_ROWS = {
  models: ['llm-deepseek', 'llm-deepseek-account'],
  search: ['web-search-deepseek', 'web-deepseek-api'],
}
const MARK = 'dsh-model-defaults'
const JS_TAG = { tag: 'tag:yaml.org,2002:js', resolve: (value) => value }
const KEEP_BACKUPS = 5

/** Provider routes served by DeepSeek itself (API key and account). */
export function isDeepseekProvider(id) {
  return /^deepseek/i.test(String(id || ''))
}

/** Whether DeepSeek's own search rows stay enabled for a search choice. */
export function deepseekSearchEnabled(provider, deepseekModelsOff) {
  if (provider === 'deepseek-official') return true
  if (provider === AUTO) return !deepseekModelsOff
  return false
}

function parse(text) {
  const YAML = loadYaml()
  const source = String(text ?? '').trim() === '' ? '[]\n' : String(text)
  const doc = YAML.parseDocument(source, { customTags: [JS_TAG] })
  if (doc.errors.length) throw failure(422, 'err.yaml', { error: doc.errors[0].message })
  if (!YAML.isSeq(doc.contents)) throw failure(422, 'err.notList')
  doc.contents.flow = false
  return { YAML, doc }
}

/** Indexes of top-level patch rows with this id (rows with `insert:` are not patches of an id). */
function rowIndexes(YAML, doc, id) {
  const out = []
  doc.contents.items.forEach((item, index) => {
    if (YAML.isMap(item) && !item.has('insert') && item.get('id') === id) out.push(index)
  })
  return out
}

/** Every map node anywhere in the document whose `id` is `id` (rows inside `insert:` presets included). */
function findMaps(YAML, doc, id, test = () => true) {
  const found = []
  YAML.visit(doc, {
    Map(_key, node) {
      if (node.get('id') === id && test(node)) found.push(node)
    },
  })
  return found
}

const isSpawnSubagent = (YAML) => (node) => {
  const config = node.get('config', true)
  return YAML.isMap(config) && config.get('provider') === 'spawn'
}

function plainConfig(YAML, node) {
  const config = node.get('config', true)
  return YAML.isMap(config) ? config.toJSON() : {}
}

/**
 * What a profile patch says: default model, DeepSeek rows, pi-ai providers,
 * web search provider, subagent models. A later row for the same id wins.
 */
export function readProfile(text) {
  const { YAML, doc } = parse(text)
  const rows = (doc.toJS() || []).filter((row) => row && typeof row === 'object' && !('insert' in row))
  let defaultModel = null
  const disabled = {}
  const providers = new Set()
  let web = null
  let extra = null
  let claudeSubagent = null
  let topSubagent = null
  let claudeTool = true
  for (const row of rows) {
    const config = row.config && typeof row.config === 'object' ? row.config : null
    if (row.id === DEFAULT_MODEL_ID && config) {
      defaultModel = { provider: config.provider ?? null, model: config.model ?? null, reasoningEffort: config.reasoningEffort ?? null }
    }
    if ([...DEEPSEEK_ROWS.models, ...DEEPSEEK_ROWS.search].includes(row.id) && 'disabled' in row) {
      disabled[row.id] = row.disabled === true ? true : typeof row.disabled === 'string' ? 'expression' : false
    }
    if (row.id === 'llm-pi-ai' && config?.providers && typeof config.providers === 'object') {
      for (const key of Object.keys(config.providers)) providers.add(key)
    }
    if (row.id === WEB_ID && config) web = config
    if (row.id === EXTRA_SEARCH_ID && config) extra = config
    if (row.id === CLAUDE_SUBAGENT_ID && config) claudeSubagent = config
    if (row.id === CLAUDE_TOOL_ID && 'disabled' in row) claudeTool = row.disabled === true ? false : typeof row.disabled === 'string' ? 'expression' : true
    if (row.id === TOOL_SUBAGENT_ID && config?.provider === 'spawn') topSubagent = config
  }
  // the preset copy (web profile) wins over a top-level row: it is what the active preset runs
  const nested = findMaps(YAML, doc, TOOL_SUBAGENT_ID, isSpawnSubagent(YAML))
    .filter((node) => !doc.contents.items.includes(node))
  const subagentConfig = nested.length ? plainConfig(YAML, nested[nested.length - 1]) : topSubagent
  const agentOptions = subagentConfig?.agentOptions
  const all = (ids) => ids.every((id) => disabled[id] === true)
  // the answer model: `summaryModel`, absent = the DSH default model
  const rawSummary = extra?.summaryModel
  const summaryModel = rawSummary === AUTO ? AUTO
    : rawSummary && typeof rawSummary === 'object' && rawSummary.provider ? { provider: rawSummary.provider, model: rawSummary.model } : null
  const rawProvider = (web && typeof web.searchProvider === 'string' && web.searchProvider) || WEB_BASE.searchProvider
  return {
    defaultModel,
    deepseek: { models: all(DEEPSEEK_ROWS.models), search: all(DEEPSEEK_ROWS.search), rows: disabled },
    providers: [...providers],
    search: {
      provider: rawProvider,
      fetchProvider: (web && typeof web.fetchProvider === 'string' && web.fetchProvider) || WEB_BASE.fetchProvider,
      claudeModel: typeof extra?.claudeModel === 'string' ? extra.claudeModel : null,
      summaryModel,
      summary: extra?.summary !== false,
    },
    subagents: {
      // the delegated work goes to Claude Code while its tool is on, else to the DSH `subagent` tool
      route: claudeTool === false ? 'dsh' : 'claude',
      claudeCode: {
        model: typeof claudeSubagent?.model === 'string' && claudeSubagent.model ? claudeSubagent.model : AUTO,
        row: Boolean(claudeSubagent),
        tool: claudeTool,
      },
      dsh: agentOptions && typeof agentOptions === 'object' && agentOptions.provider && agentOptions.model
        ? { provider: agentOptions.provider, model: agentOptions.model }
        : AUTO,
      dshPlace: nested.length ? 'preset' : topSubagent ? 'top' : 'base',
    },
  }
}

/**
 * Turn one subagent choice into profile changes.
 *   { kind: 'claude', model }  → Claude Code tool on with that model; DSH children follow the conversation;
 *   { kind: 'dsh', ref }       → Claude Code tool off; DSH children on `ref` ('auto' = the conversation's model).
 */
export function subagentChanges(choice) {
  if (!choice) return {}
  if (choice.kind === 'claude') return { claudeTool: true, claudeSubagentModel: choice.model, dshSubagent: AUTO }
  return { claudeTool: false, dshSubagent: choice.ref }
}

/**
 * Edit the config of the last top-level row with this id: `change(current)`
 * returns the new config, or null to drop the row's config. A missing row is
 * created from `create` (null = leave it missing).
 */
function editRowConfig(YAML, doc, id, change, { create = null, comment } = {}) {
  const seq = doc.contents
  const indexes = rowIndexes(YAML, doc, id).filter((i) => seq.items[i].has('config'))
  if (indexes.length) {
    const index = indexes[indexes.length - 1]
    const row = seq.items[index]
    const next = change(plainConfig(YAML, row))
    if (next === null) {
      row.delete('config')
      const keys = row.items.map((pair) => (YAML.isScalar(pair.key) ? pair.key.value : pair.key))
      if (keys.every((key) => key === 'id' || key === 'name')) seq.items.splice(index, 1)
    } else {
      row.set('config', doc.createNode(next))
    }
    return
  }
  if (!create) return
  const node = doc.createNode({ id, config: create })
  if (comment) node.commentBefore = comment
  seq.items.push(node)
}

const isEqual = (a, b) => JSON.stringify(a) === JSON.stringify(b)

/**
 * Apply changes to a patch and return the new text.
 * @param {string} text
 * @param {{
 *   defaultModel?: { provider, model, reasoningEffort? },
 *   disable?: Record<string, boolean>,
 *   search?: { provider: string, claudeModel?: string, summaryModel?: 'auto'|{provider,model}, summary?: boolean },
 *   claudeSubagentModel?: string,   // 'auto' removes `model`
 *   claudeTool?: boolean,           // false disables the global subagent_claude_code tool
 *   dshSubagent?: 'auto'|{ provider, model },
 *   stamp?: string,
 *   t?: (key: string) => string,     // translator for the comments on new rows (default English)
 * }} changes
 */
export function applyProfile(text, changes = {}) {
  const original = String(text ?? '')
  const next = applyChanges(original, changes)
  // nothing changed in meaning → keep the file byte for byte (no reformatting)
  if (isEqual(readProfileRaw(next), readProfileRaw(original))) return original
  return next
}

/** Plain JS form of a patch, for meaning-level comparison. */
function readProfileRaw(text) {
  const { doc } = parse(text)
  return doc.toJS()
}

/** Keep the file's own flow-list style: `[serve]` stays `[serve]`, `[ serve ]` stays padded. */
function flowPadding(text) {
  const padded = (text.match(/\[ [^\]\n]*? \]/g) || []).length
  const tight = (text.match(/\[[^\s\][][^\]\n]*\]/g) || []).length
  return padded >= tight
}

function applyChanges(text, { defaultModel, disable = {}, search, claudeSubagentModel, claudeTool, dshSubagent, stamp = '', t = translator('en') } = {}) {
  const { YAML, doc } = parse(text)
  const seq = doc.contents
  const note = (key) => ` ${MARK}: ${t(key)}${stamp ? ` (${stamp})` : ''}`

  if (defaultModel) {
    const config = { provider: defaultModel.provider, model: defaultModel.model }
    if (defaultModel.reasoningEffort) config.reasoningEffort = defaultModel.reasoningEffort
    editRowConfig(YAML, doc, DEFAULT_MODEL_ID, () => config, { create: config, comment: note('comment.default') })
  }

  if (search?.provider) {
    editRowConfig(YAML, doc, WEB_ID, (current) => ({ ...current, searchProvider: search.provider, fetchProvider: current.fetchProvider || WEB_BASE.fetchProvider }),
      { create: { searchProvider: search.provider, fetchProvider: WEB_BASE.fetchProvider }, comment: note('comment.search') })
    const extra = {}
    if (search.claudeModel) extra.claudeModel = search.claudeModel
    if (search.summaryModel) extra.summaryModel = search.summaryModel
    if (typeof search.summary === 'boolean') extra.summary = search.summary
    if (Object.keys(extra).length) {
      editRowConfig(YAML, doc, EXTRA_SEARCH_ID, (current) => ({ ...current, ...extra }), { create: extra, comment: note('comment.searchExtra') })
    }
  }

  if (claudeSubagentModel) {
    const set = claudeSubagentModel !== AUTO
    editRowConfig(YAML, doc, CLAUDE_SUBAGENT_ID, (current) => {
      const next = { ...current }
      if (set) next.model = claudeSubagentModel
      else delete next.model
      return Object.keys(next).length ? next : null
    }, { create: set ? { model: claudeSubagentModel } : null, comment: note('comment.claudeModel') })
  }

  if (dshSubagent) {
    const options = dshSubagent === AUTO ? null : { provider: dshSubagent.provider, model: dshSubagent.model }
    const nested = findMaps(YAML, doc, TOOL_SUBAGENT_ID, isSpawnSubagent(YAML)).filter((node) => !seq.items.includes(node))
    if (nested.length) {
      for (const node of nested) {
        const config = node.get('config', true)
        if (options) config.set('agentOptions', doc.createNode(options))
        else config.delete('agentOptions')
      }
    } else {
      editRowConfig(YAML, doc, TOOL_SUBAGENT_ID, (current) => {
        const next = { ...TOOL_SUBAGENT_BASE, ...current }
        if (options) next.agentOptions = options
        else delete next.agentOptions
        return isEqual(next, TOOL_SUBAGENT_BASE) ? null : next
      }, { create: options ? { ...TOOL_SUBAGENT_BASE, agentOptions: options } : null, comment: note('comment.dshSubagent') })
    }
  }

  for (const [id, off] of Object.entries(disable)) setDisabled(YAML, doc, id, off, note('comment.deepseekOff'))
  if (typeof claudeTool === 'boolean') {
    setDisabled(YAML, doc, CLAUDE_TOOL_ID, !claudeTool, note('comment.claudeToolOff'))
  }
  return doc.toString({ flowCollectionPadding: flowPadding(text) })
}

/**
 * Add or remove a `disabled: true` flag on a top-level row: a missing row is created
 * with the comment; a row left with nothing but its id (and name) is dropped again.
 * A flag written as a `!!js` expression is never touched.
 */
function setDisabled(YAML, doc, id, off, comment) {
  const seq = doc.contents
  const indexes = rowIndexes(YAML, doc, id)
  if (off) {
    if (indexes.some((i) => seq.items[i].get('disabled') === true)) return
    if (indexes.length) seq.items[indexes[indexes.length - 1]].set('disabled', true)
    else {
      const node = doc.createNode({ id, disabled: true })
      node.commentBefore = comment
      seq.items.push(node)
    }
    return
  }
  for (const i of indexes.reverse()) {
    const row = seq.items[i]
    if (row.get('disabled') !== true) continue
    row.delete('disabled')
    const keys = row.items.map((pair) => (YAML.isScalar(pair.key) ? pair.key.value : pair.key))
    if (keys.every((key) => key === 'id' || key === 'name')) seq.items.splice(i, 1)
  }
}

/** Run `dsh --profile <name> [--patch …] --dump-config`: the composition must still load. */
export function checkComposes(profile, { timeout = 120000, patches = [] } = {}) {
  return new Promise((resolve) => {
    const args = [dshEntry(), '--profile', profile, ...patches.flatMap((p) => ['--patch', p]), '--dump-config']
    execFile(process.execPath, args, { timeout, maxBuffer: 64 * 1024 * 1024, env: process.env, encoding: 'utf8' },
      (error, stdout, stderr) => {
        if (error) resolve({ ok: false, output: '', error: (stderr || error.message || '').trim().slice(-800) })
        else resolve({ ok: true, output: stdout, error: null })
      })
  })
}

async function pruneBackups(file) {
  const dir = path.dirname(file)
  const prefix = `${path.basename(file)}.bak-model-defaults-`
  const names = (await fs.readdir(dir).catch(() => [])).filter((n) => n.startsWith(prefix)).sort()
  for (const name of names.slice(0, Math.max(0, names.length - KEEP_BACKUPS))) {
    await fs.rm(path.join(dir, name), { force: true })
  }
}

function timestamp(date = new Date()) {
  const p = (n) => String(n).padStart(2, '0')
  return `${date.getFullYear()}${p(date.getMonth() + 1)}${p(date.getDate())}-${p(date.getHours())}${p(date.getMinutes())}${p(date.getSeconds())}`
}

/**
 * Change one YAML patch file: under a lock back up and write atomically; then,
 * with the lock released (`dsh` takes the profile lock while booting), check that
 * the composition still loads, and roll back if it does not.
 * @param {{ file: string, lock: string, compose: () => Promise<{ok, output, error}>, label: string }} target
 * @param {(text: string) => string} change
 * @param {{ verify?: (dump: string) => string|null, t?: Function }} options - `verify` returns a translated problem
 */
export async function editPatchFile(target, change, { verify, t = translator('en') } = {}) {
  const { withFileLock, writeFileAtomic } = await loadAtomicWrite()
  const { file, lock } = target
  const written = await withFileLock(lock, async () => {
    let before = ''
    try {
      before = await fs.readFile(file, 'utf8')
    } catch (error) {
      if (error.code !== 'ENOENT') throw error
    }
    const after = change(before)
    if (after === before) return null
    const backup = before ? `${file}.bak-model-defaults-${timestamp()}` : null
    if (backup) await fs.writeFile(backup, before, { mode: 0o600 })
    await writeFileAtomic(file, after, { mode: 0o600 })
    return { before, after, backup }
  })
  if (!written) return { changed: false, backup: null }

  const result = await target.compose()
  const problem = result.ok ? verify?.(result.output) ?? null : t('err.composeFailed', { error: result.error })
  if (problem) {
    await withFileLock(lock, async () => {
      // restore only if nobody wrote the file after us
      const current = await fs.readFile(file, 'utf8').catch(() => null)
      if (current === written.after) await writeFileAtomic(file, written.before || '[]\n', { mode: 0o600 })
    })
    throw failure(422, 'err.compose', { target: target.label, problem })
  }
  await pruneBackups(file)
  return { changed: true, backup: written.backup }
}

/** A profile's own patch (`<dir>/cordis.patch.yml`, lock on `<dir>/package.json`). */
export function editProfile(profile, change, { verify, compose, t = translator('en') } = {}) {
  return editPatchFile({
    file: path.join(profile.dir, 'cordis.patch.yml'),
    lock: path.join(profile.dir, 'package.json'),
    compose: compose ? () => compose(profile.name) : () => checkComposes(profile.name),
    label: t('target.profile', { profile: profile.name }),
  }, change, { verify, t })
}
