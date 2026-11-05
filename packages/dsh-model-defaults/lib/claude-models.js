/**
 * The Claude models this machine's Claude Code login can use, as Claude Code
 * itself reports them (`Query.supportedModels()` of the Claude Agent SDK, the
 * SDK the `subagent_claude_code` tool runs on).
 *
 * The probe starts Claude Code with a streaming prompt that never sends a
 * message, so no request reaches the model and no usage is spent; it is
 * aborted right after the answer. Results are cached; without the SDK or on
 * failure a small alias list is used instead.
 */
import { existsSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

/** Aliases Claude Code always understands; used only when the live list is unavailable. */
export const FALLBACK_CLAUDE_MODELS = [
  { id: 'opus', name: 'Opus', model: null, description: '' },
  { id: 'sonnet', name: 'Sonnet', model: null, description: '' },
  { id: 'haiku', name: 'Haiku', model: null, description: '' },
]
/** A Claude Code model value: an alias (`opus`, `sonnet[1m]`) or a model id (`claude-opus-5-5`). */
export const CLAUDE_MODEL_PATTERN = /^[a-z0-9][a-z0-9.\-[\]]{1,79}$/i

/** Where the Claude Agent SDK lives: the given profile's node_modules first. */
export function findClaudeSdk(...profileDirs) {
  const home = os.homedir()
  const dirs = [...profileDirs.filter(Boolean),
    path.join(home, '.dsh', 'profiles', 'web'),
    path.join(home, '.dsh', 'profiles', 'headless')]
  for (const dir of dirs) {
    const file = path.join(dir, 'node_modules', '@anthropic-ai', 'claude-agent-sdk', 'sdk.mjs')
    if (existsSync(file)) return file
  }
  return null
}

/** Turn the SDK's ModelInfo list into page options; `default` is Claude Code's own pick. */
export function toOptions(list) {
  const out = []
  let recommended = null
  for (const m of Array.isArray(list) ? list : []) {
    const id = typeof m?.value === 'string' ? m.value : null
    if (!id || !CLAUDE_MODEL_PATTERN.test(id)) continue
    const entry = {
      id,
      name: typeof m.displayName === 'string' && m.displayName ? m.displayName : id,
      model: typeof m.resolvedModel === 'string' ? m.resolvedModel : null,
      description: typeof m.description === 'string' ? m.description : '',
    }
    if (id === 'default') recommended = entry
    else out.push(entry)
  }
  return { models: out, recommended }
}

/**
 * @param {{ sdkFile: string|null, ttlMs?: number, timeoutMs?: number, importer?: (file: string) => Promise<object> }} options
 */
export function createClaudeModels({ sdkFile, ttlMs = 3600000, timeoutMs = 20000, importer = (file) => import(pathToFileURL(file).href) }) {
  let cache = null
  let pending = null

  async function probe() {
    if (!sdkFile) return { source: 'fallback', reason: 'sdk-missing', models: FALLBACK_CLAUDE_MODELS, recommended: null }
    let release = () => {}
    const idle = new Promise((resolve) => { release = resolve })
    async function* prompt() { await idle }
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeoutMs)
    try {
      const sdk = await importer(sdkFile)
      const query = sdk.query({
        prompt: prompt(),
        options: {
          abortController: controller,
          cwd: os.tmpdir(),
          persistSession: false,
          settingSources: [],
          strictMcpConfig: true,
          mcpServers: {},
          tools: [],
          env: {
            HOME: os.homedir(),
            PATH: process.env.PATH || '/usr/local/bin:/usr/bin:/bin',
            LANG: process.env.LANG || 'C.UTF-8',
            USER: process.env.USER || os.userInfo().username,
            TMPDIR: os.tmpdir(),
          },
        },
      })
      const { models, recommended } = toOptions(await query.supportedModels())
      if (!models.length) return { source: 'fallback', reason: 'empty', models: FALLBACK_CLAUDE_MODELS, recommended: null }
      return { source: 'claude-code', models, recommended }
    } catch (error) {
      return { source: 'fallback', reason: error?.message || String(error), models: FALLBACK_CLAUDE_MODELS, recommended: null }
    } finally {
      clearTimeout(timer)
      release()
      controller.abort() // ends the Claude Code process
    }
  }

  /** Cached list; `refresh` probes again. Failures are cached briefly (1 min) so the page stays fast. */
  async function list({ refresh = false } = {}) {
    const fresh = cache && Date.now() - cache.at < (cache.value.source === 'fallback' ? 60000 : ttlMs)
    if (!refresh && fresh) return cache.value
    if (pending) return pending
    pending = probe().then((value) => {
      cache = { at: Date.now(), value }
      return value
    }).finally(() => {
      pending = null
    })
    return pending
  }

  return { list }
}
