/**
 * The bridge between DSH and the dsh-model-settings program (Go).
 *
 * DSH loads plugins as Node.js modules, so each plugin of this repository is a
 * thin module that starts the program (`dsh-model-settings serve`) and talks
 * to it over stdin/stdout: newline-delimited JSON, requests both ways.
 * The module only does what needs DSH itself — registering routes, hooks and
 * search providers, and calling DSH services; all decisions, file edits,
 * searches and Claude Code runs happen in the program.
 *
 *   request       {"id": 1, "method": "...", "params": {...}}
 *   response      {"id": 1, "result": ...} | {"id": 1, "error": {"message", "code", "status"}}
 *   notification  {"method": "...", "params": {...}}
 *   cancel        {"cancel": 1}
 *
 * The program is looked up in: plugin option `binary`, $DSH_MODEL_SETTINGS_BIN,
 * `<repository>/bin/dsh-model-settings`, `dsh-model-settings` on PATH.
 * It stops when DSH closes the link; if it stops on its own, the next call starts it again.
 */
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import path from 'node:path'
import { createInterface } from 'node:readline'
import { fileURLToPath } from 'node:url'

/** The program version these modules speak to. */
export const VERSION = '1.0.0'
export const REPOSITORY = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const EXE = process.platform === 'win32' ? 'dsh-model-settings.exe' : 'dsh-model-settings'
const RESTART_PAUSE_MS = 5000

/** Path of the program. */
export function findBinary(config, env = process.env) {
  for (const file of [config?.binary, env.DSH_MODEL_SETTINGS_BIN, path.join(REPOSITORY, 'bin', EXE)]) {
    if (typeof file === 'string' && file && existsSync(file)) return file
  }
  for (const dir of String(env.PATH || '').split(path.delimiter)) {
    const file = dir && path.join(dir, EXE)
    if (file && existsSync(file)) return file
  }
  throw new Error(`dsh-model-settings is not built: run \`go build -o bin/ ./cmd/dsh-model-settings\` in ${REPOSITORY}, or set DSH_MODEL_SETTINGS_BIN`)
}

/** A JSON-safe copy: functions, symbols and cycles are dropped. */
export function plain(value, stack = new Set(), depth = 0) {
  if (value === undefined || value === null) return null
  if (typeof value === 'function' || typeof value === 'symbol') return undefined
  if (typeof value === 'bigint') return Number(value)
  if (typeof value !== 'object') return value
  if (stack.has(value) || depth > 32) return undefined
  if (typeof value.toJSON === 'function') return plain(value.toJSON(), stack, depth + 1)
  stack.add(value)
  try {
    if (Array.isArray(value)) return value.map((item) => plain(item, stack, depth + 1) ?? null)
    const entries = value instanceof Map ? [...value].map(([k, v]) => [String(k), v]) : Object.entries(value)
    const out = {}
    for (const [key, item] of entries) {
      const copy = plain(item, stack, depth + 1)
      if (copy !== undefined) out[key] = copy
    }
    return out
  } finally {
    stack.delete(value)
  }
}

/** The running profile (name and folder), when DSH provides it. */
function profileOf(ctx) {
  try {
    const p = ctx.get('profileContext')
    return { name: p?.name || '', dir: p?.dir || '' }
  } catch {
    return { name: '', dir: '' }
  }
}

function linkError(error) {
  return Object.assign(new Error(error?.message || 'dsh-model-settings: unknown error'), {
    ...(error?.code ? { code: error.code } : {}),
    ...(Number.isInteger(error?.status) ? { status: error.status } : {}),
  })
}

/**
 * Start the program for one plugin.
 * @param {object} ctx - the plugin's DSH context
 * @param {{ plugin: string, config?: object, label: string,
 *           handlers?: Record<string, (params: any, signal: AbortSignal) => any>,
 *           onNotify?: Record<string, (params: any) => void> }} options
 * @returns {{ ready: () => Promise<any>, call: (method: string, params?: any, signal?: AbortSignal) => Promise<any>,
 *             notify: (method: string, params?: any) => void }}
 */
export function startSidecar(ctx, { plugin, config = {}, label, handlers = {}, onNotify = {} }) {
  const binary = findBinary(config)
  const hello = { plugin, config: plain(config) ?? {}, profile: profileOf(ctx), node: process.execPath, argv1: process.argv[1] || '' }
  let child = null
  let started = null
  let first = null
  let lastStart = 0
  let nextId = 0
  let closed = false
  const pending = new Map()
  const running = new Map()

  const log = (line) => {
    const m = /^(info|warn) (.*)$/.exec(line)
    if (m?.[1] === 'warn') console.warn(m[2])
    else console.info(m ? m[2] : `[${label}] ${line}`)
  }

  const send = (proc, message) => {
    if (!proc || proc.exitCode !== null || proc.stdin.destroyed) return false
    proc.stdin.write(`${JSON.stringify(message)}\n`)
    return true
  }

  function answer(proc, message) {
    const handler = handlers[message.method]
    const controller = new AbortController()
    running.set(message.id, controller)
    Promise.resolve()
      .then(() => {
        if (!handler) throw Object.assign(new Error(`${label}: DSH side has no method ${message.method}`), { code: 'UNKNOWN_METHOD' })
        return handler(message.params ?? {}, controller.signal)
      })
      .then(
        (result) => send(proc, { id: message.id, result: plain(result) ?? null }),
        (error) => send(proc, {
          id: message.id,
          error: {
            message: error?.message || String(error),
            ...(error?.code ? { code: String(error.code) } : {}),
            ...(Number.isInteger(error?.status) ? { status: error.status } : {}),
          },
        }),
      )
      .finally(() => running.delete(message.id))
  }

  function onLine(proc, line) {
    let message
    try {
      message = JSON.parse(line)
    } catch {
      return log(line)
    }
    if (message.cancel != null) running.get(message.cancel)?.abort()
    else if (message.method && message.id != null) answer(proc, message)
    else if (message.method) onNotify[message.method]?.(message.params ?? {})
    else if (message.id != null) {
      const waiter = pending.get(message.id)
      pending.delete(message.id)
      if (!waiter) return
      if (message.error) waiter.reject(linkError(message.error))
      else waiter.resolve(message.result)
    }
  }

  function request(proc, method, params, signal) {
    return new Promise((resolve, reject) => {
      if (signal?.aborted) return reject(signal.reason)
      const id = ++nextId
      const onAbort = () => {
        pending.delete(id)
        send(proc, { cancel: id })
        reject(signal.reason)
      }
      pending.set(id, {
        resolve: (value) => { signal?.removeEventListener('abort', onAbort); resolve(value) },
        reject: (error) => { signal?.removeEventListener('abort', onAbort); reject(error) },
      })
      signal?.addEventListener('abort', onAbort, { once: true })
      if (!send(proc, { id, method, params: params ?? null })) {
        pending.delete(id)
        reject(new Error(`${label}: the program is not running`))
      }
    })
  }

  function start() {
    lastStart = Date.now()
    const proc = spawn(binary, ['serve'], { stdio: ['pipe', 'pipe', 'pipe'], env: process.env, windowsHide: true })
    child = proc
    proc.stdin.on('error', () => {})
    createInterface({ input: proc.stdout }).on('line', (line) => onLine(proc, line))
    createInterface({ input: proc.stderr }).on('line', log)
    proc.on('error', (error) => console.warn(`[${label}] cannot start ${binary}: ${error.message}`))
    proc.on('exit', (code, signal) => {
      if (child !== proc) return
      child = null
      started = null
      for (const waiter of pending.values()) waiter.reject(new Error(`${label}: the program stopped (${signal || code})`))
      pending.clear()
      for (const controller of running.values()) controller.abort()
      running.clear()
      if (!closed) console.warn(`[${label}] the program stopped (${signal || code}); the next call starts it again`)
    })
    started = request(proc, 'hello', hello).then((reply) => {
      if (reply?.version !== VERSION) {
        console.warn(`[${label}] ${binary} is version ${reply?.version}, these modules expect ${VERSION}: rebuild it (go build -o bin/ ./cmd/dsh-model-settings)`)
      }
      return reply?.plugin ?? {}
    })
    started.catch(() => {})
    return started
  }

  first = start()
  ctx.effect(() => () => {
    closed = true
    const proc = child
    child = null
    if (!proc) return
    proc.stdin.end()
    setTimeout(() => proc.exitCode === null && proc.kill(), 2000).unref()
  }, `${label}: program`)

  /** A running program: started again on demand, at most every few seconds. */
  async function live() {
    if (closed) throw new Error(`${label}: disposed`)
    if (!child) {
      if (Date.now() - lastStart < RESTART_PAUSE_MS) throw new Error(`${label}: the program stopped; it is started again in a few seconds`)
      start()
    }
    const proc = child
    await started
    return proc
  }

  return {
    ready: () => first,
    async call(method, params, signal) {
      const proc = await live()
      return request(proc, method, params, signal)
    },
    notify(method, params) {
      send(child, { method, params: params ?? null })
    },
  }
}
