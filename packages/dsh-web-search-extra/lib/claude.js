/**
 * Web search through Claude (Claude Code subscription on this machine, Claude Agent SDK).
 *
 * Every query is one short, isolated Claude Code run:
 *   - the only tool is WebSearch (no Bash, no files, no fetch, no MCP servers);
 *   - no user/project settings, hooks or CLAUDE.md (`settingSources: []`);
 *   - nothing persisted (`persistSession: false`), started in an empty temp dir.
 * Claude replies with JSON (`answer` + `sources`).
 */
import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { pathToFileURL } from 'node:url'
import { parseAnswer, sourcesFromToolText } from './parse.js'

/** Claude model aliases offered by the settings page; `auto` = Claude Code's own default. */
export const CLAUDE_AUTO = 'auto'

/** Where the Claude Agent SDK lives: the running profile's node_modules first. */
export function findSdk(profileDir) {
  const home = os.homedir()
  const candidates = [
    profileDir && path.join(profileDir, 'node_modules'),
    path.join(home, '.dsh', 'profiles', 'web', 'node_modules'),
    path.join(home, '.dsh', 'profiles', 'headless', 'node_modules'),
  ].filter(Boolean)
  for (const dir of candidates) {
    const file = path.join(dir, '@anthropic-ai', 'claude-agent-sdk', 'sdk.mjs')
    if (existsSync(file)) return file
  }
  return null
}

/** Claude Code can authenticate: a stored login or an explicit token/key. */
export function hasClaudeCredentials() {
  return existsSync(path.join(os.homedir(), '.claude', '.credentials.json')) ||
    Boolean(process.env.CLAUDE_CODE_OAUTH_TOKEN || process.env.ANTHROPIC_API_KEY)
}

export function buildClaudePrompt(query, maxResults) {
  return [
    'You are the web search backend of an AI agent. Research the query below with the WebSearch tool',
    '(one to three searches), then reply with ONLY a JSON object — no prose before or after, no code fences:',
    '{"answer": "<2-6 sentences answering the query with facts from the sources, in the language of the query>",',
    ' "sources": [{"url": "https://...", "title": "...", "snippet": "<1-2 sentences from that page relevant to the query>", "publishedAt": "<ISO date if the result shows one>"}]}',
    `List at most ${maxResults} sources, most relevant first, using only URLs that WebSearch returned.`,
    'If nothing relevant is found, return {"answer": "", "sources": []}.',
    '',
    `Query: ${JSON.stringify(String(query))}`,
  ].join('\n')
}

/**
 * @param {{ sdkFile: string|null, model: string, timeoutMs: number }} options
 */
export function createClaudeSearch({ sdkFile, model, timeoutMs }) {
  let sdk = null
  return async function claudeSearch(request, signal) {
    if (!sdkFile) throw new Error('Claude Agent SDK not found in the DSH profile (install @deepseek-ai/dsh-subagent-claude-code)')
    sdk ??= await import(pathToFileURL(sdkFile).href)
    const maxResults = Math.min(Math.max(Number(request.maxResults) || 8, 1), 20)
    const controller = new AbortController()
    const abort = () => controller.abort()
    signal?.addEventListener('abort', abort, { once: true })
    const timer = setTimeout(abort, timeoutMs)
    const cwd = mkdtempSync(path.join(os.tmpdir(), 'dsh-claude-search-'))
    const toolTexts = []
    let resultText = ''
    let failure = null
    const chosen = typeof model === 'function' ? model() : model
    try {
      const stream = sdk.query({
        prompt: buildClaudePrompt(request.query, maxResults),
        options: {
          abortController: controller,
          cwd,
          ...(chosen && chosen !== CLAUDE_AUTO ? { model: chosen } : {}),
          tools: ['WebSearch'],
          allowedTools: ['WebSearch'],
          permissionMode: 'dontAsk',
          settingSources: [],
          strictMcpConfig: true,
          mcpServers: {},
          persistSession: false,
          maxTurns: 8,
          env: {
            HOME: os.homedir(),
            PATH: process.env.PATH || '/usr/local/bin:/usr/bin:/bin',
            LANG: process.env.LANG || 'C.UTF-8',
            USER: process.env.USER || os.userInfo().username,
            TMPDIR: os.tmpdir(),
          },
        },
      })
      for await (const message of stream) {
        if (message.type === 'user') {
          for (const part of message.message?.content || []) {
            if (part?.type !== 'tool_result') continue
            toolTexts.push(typeof part.content === 'string' ? part.content
              : (part.content || []).map((c) => c?.text || '').join('\n'))
          }
        } else if (message.type === 'result') {
          if (message.subtype === 'success') resultText = String(message.result || '')
          else failure = message.subtype
        }
      }
    } catch (error) {
      if (controller.signal.aborted) {
        throw new Error(signal?.aborted ? 'search cancelled' : `Claude did not answer within ${Math.round(timeoutMs / 1000)} s`)
      }
      throw new Error(`Claude: ${error?.message || error}`)
    } finally {
      clearTimeout(timer)
      signal?.removeEventListener('abort', abort)
      rmSync(cwd, { recursive: true, force: true })
    }
    const parsed = parseAnswer(resultText)
    let sources = parsed.sources
    if (!sources.length) sources = sourcesFromToolText(toolTexts.join('\n'))
    if (!sources.length && !parsed.answer) throw new Error(`Claude returned no results${failure ? ` (${failure})` : ''}`)
    return { content: parsed.answer || undefined, sources: sources.slice(0, maxResults), truncated: false }
  }
}
