/**
 * dsh-web-search-extra — extra `ctx.web` search providers for DSH's `web_search` tool:
 *
 *   searxng       self-hosted SearXNG results + an optional short answer by a DSH model
 *   claude-code   Claude through the Claude Code subscription (WebSearch tool only)
 *   auto          the first provider that answers, in order DeepSeek → SearXNG → Claude
 *
 * Providers only register; the `web` row's `searchProvider` decides which one
 * `web_search` uses (the settings page of dsh-model-defaults writes it).
 *
 * Config (row `web-search-extra`):
 *   searxngURL      default http://127.0.0.1:8888
 *   summary         false → SearXNG results only (faster; the conversation model reads them)
 *   summaryModel    { provider, model } that writes the answer, or 'auto' = the DSH default model (default)
 *   claudeModel     a Claude Code model id or alias, or 'auto' = Claude Code's own default
 *   autoOrder       provider ids tried by `auto`
 *   timeoutMs / summaryTimeoutMs / concurrency
 */
import { CLAUDE_AUTO, createClaudeSearch, findSdk, hasClaudeCredentials } from './claude.js'
import { DEFAULT_SEARXNG, createSearxngSearch } from './searxng.js'

export const name = 'dsh-web-search-extra'
export const inject = ['web', 'llm']

export const PROVIDER_IDS = { searxng: 'searxng', claude: 'claude-code', auto: 'auto' }
export const DEFAULT_CLAUDE_MODEL = 'haiku'
export const DEFAULT_AUTO_ORDER = ['deepseek-official', 'searxng', 'claude-code']

/** A tiny FIFO semaphore: at most `limit` tasks at once. */
export function limiter(limit) {
  let active = 0
  const queue = []
  const next = () => {
    if (active >= limit || !queue.length) return
    active += 1
    const { task, resolve, reject } = queue.shift()
    Promise.resolve().then(task).then(resolve, reject).finally(() => {
      active -= 1
      next()
    })
  }
  return (task) => new Promise((resolve, reject) => {
    queue.push({ task, resolve, reject })
    next()
  })
}

/**
 * Try providers in order; the first non-empty result wins.
 * @param {string[]} order - provider ids
 * @param {(id: string) => object|null} lookup - provider by id
 */
export async function searchInOrder(order, lookup, request, signal) {
  const errors = []
  for (const id of order) {
    if (id === PROVIDER_IDS.auto) continue
    const provider = lookup(id)
    if (!provider) {
      errors.push(`${id}: not installed`)
      continue
    }
    try {
      if (typeof provider.available === 'function' && !provider.available()) {
        errors.push(`${id}: unavailable`)
        continue
      }
      const result = await provider.search(request, signal)
      if (result?.sources?.length || result?.content) return result
      errors.push(`${id}: empty answer`)
    } catch (error) {
      if (signal?.aborted) throw error
      errors.push(`${id}: ${error?.message || error}`)
    }
  }
  throw new Error(`auto: no search provider answered — ${errors.join('; ')}`)
}

/** Read the plugin config with defaults. */
export function normalizeConfig(raw) {
  const cfg = raw && typeof raw === 'object' ? raw : {}
  const summaryModel = cfg.summaryModel ?? 'auto'
  const order = Array.isArray(cfg.autoOrder) && cfg.autoOrder.length ? cfg.autoOrder : DEFAULT_AUTO_ORDER
  return {
    searxngURL: (typeof cfg.searxngURL === 'string' && cfg.searxngURL) || DEFAULT_SEARXNG,
    summary: cfg.summary !== false,
    summaryModel: summaryModel === 'auto' || (summaryModel && typeof summaryModel === 'object' && summaryModel.provider && summaryModel.model)
      ? summaryModel
      : 'auto',
    claudeModel: (typeof cfg.claudeModel === 'string' && cfg.claudeModel) || DEFAULT_CLAUDE_MODEL,
    autoOrder: order,
    timeoutMs: Number(cfg.timeoutMs) > 0 ? Number(cfg.timeoutMs) : 55000,
    summaryTimeoutMs: Number(cfg.summaryTimeoutMs) > 0 ? Number(cfg.summaryTimeoutMs) : 90000,
    concurrency: Number(cfg.concurrency) > 0 ? Number(cfg.concurrency) : 2,
  }
}

export function apply(ctx, config = {}) {
  let profileDir = null
  try {
    profileDir = ctx.get('profileContext')?.dir ?? null
  } catch {
    profileDir = null
  }
  const sdkFile = findSdk(profileDir)
  const cfg = normalizeConfig(config)
  const summaryModel = () => {
    if (cfg.summaryModel !== 'auto') return cfg.summaryModel
    try {
      const selection = ctx.get('agentDefaultModel')?.currentSelection?.()
      return selection?.provider ? { provider: selection.provider, model: selection.model } : null
    } catch {
      return null
    }
  }

  const claudeRun = limiter(cfg.concurrency)
  const claudeSearch = createClaudeSearch({ sdkFile, model: () => cfg.claudeModel, timeoutMs: cfg.timeoutMs })
  const searxngSearch = createSearxngSearch({
    llm: ctx.llm,
    searxngURL: () => cfg.searxngURL,
    summaryModel,
    summary: () => cfg.summary,
    summaryTimeoutMs: cfg.summaryTimeoutMs,
    log: (message) => console.warn(`[dsh-web-search-extra] ${message}`),
  })

  const searxng = (id) => ({
    id,
    // availability must not touch the network; a down SearXNG shows up as a search error
    available: () => true,
    search: (request, signal) => searxngSearch(request, signal),
  })
  const own = {
    [PROVIDER_IDS.searxng]: searxng(PROVIDER_IDS.searxng),
    [PROVIDER_IDS.claude]: {
      id: PROVIDER_IDS.claude,
      available: () => Boolean(sdkFile) && hasClaudeCredentials(),
      ready: async () => hasClaudeCredentials(),
      search: (request, signal) => claudeRun(() => claudeSearch(request, signal)),
    },
  }
  // other providers (DeepSeek) come from ctx.web's registry; guarded, since that map is internal
  const lookup = (id) => own[id] || ctx.web?.searchProviders?.get?.(id) || null
  own[PROVIDER_IDS.auto] = {
    id: PROVIDER_IDS.auto,
    available: () => cfg.autoOrder.some((id) => id !== PROVIDER_IDS.auto && lookup(id)),
    search: (request, signal) => searchInOrder(cfg.autoOrder, lookup, request, signal),
  }

  for (const provider of Object.values(own)) {
    ctx.effect(() => ctx.web.registerSearchProvider(provider), `dsh-web-search-extra: ${provider.id}`)
  }
  const answer = !cfg.summary ? 'off' : cfg.summaryModel === 'auto' ? 'default model' : `${cfg.summaryModel.provider}/${cfg.summaryModel.model}`
  console.info(`[dsh-web-search-extra] providers: searxng, claude-code, auto ` +
    `(searxng answer: ${answer}; claude: ${cfg.claudeModel === CLAUDE_AUTO ? 'auto' : cfg.claudeModel}, sdk ${sdkFile ? 'found' : 'missing'}; ` +
    `auto: ${cfg.autoOrder.join(' → ')})`)
}
