/**
 * dsh-model-defaults/guard: delegated work runs only on the subagent model
 * chosen on the settings page. The decisions (and the fallback to the default
 * model after a failed run) are made by the dsh-model-settings program
 * (internal/guard); this module wraps `tools/execute` and passes it the facts
 * only DSH knows: the call, whether `subagent_claude_code` is present, the
 * conversation's model and the live default model.
 *
 * Config (row `model-defaults-guard`, all optional): fallbackMinutes (default 60, 0 = never).
 */
import { plain, startSidecar } from '../../../shim/sidecar.js'

export const name = 'dsh-model-defaults-guard'
export const inject = ['tools']

const pick = (value) => (value && typeof value === 'object' ? { provider: value.provider ?? null, model: value.model ?? null } : null)
const safe = (fn) => {
  try {
    return fn()
  } catch {
    return null
  }
}
const textOf = (result) => (result?.content || []).map((block) => (block?.type === 'text' ? block.text : '')).join(' ')

function unavailable(error) {
  const message = `The subagent guard is unavailable (${error?.message || error}); delegation is refused until it works again.`
  return { content: [{ type: 'text', text: `Error: ${message}` }], isError: true, error: { message, info: { name: 'SubagentModelGuard', code: 'SUBAGENT_GUARD_UNAVAILABLE' } } }
}

export async function apply(ctx, config = {}) {
  const program = startSidecar(ctx, { plugin: 'guard', config, label: 'dsh-model-defaults' })
  const { watch, claudeTool } = await program.ready()
  const watched = new Set(watch)

  ctx.on('tools/execute', async (exec, next) => {
    if (exec.name === claudeTool) {
      const result = await next()
      program.notify('guard.result', { isError: Boolean(result?.isError), text: textOf(result) })
      return result
    }
    if (!watched.has(exec.name)) return next()
    let verdict
    try {
      verdict = await program.call('guard.check', {
        tool: exec.name,
        arguments: plain(exec.arguments ?? {}),
        claudeTool: Boolean(safe(() => ctx.tools.get(claudeTool, exec.agent))),
        header: pick(safe(() => exec.agent?.session?.requestHeader?.()?.config)),
        options: pick(exec.agent?.options),
        defaultModel: pick(safe(() => ctx.get('agentDefaultModel')?.currentSelection?.())),
      })
    } catch (error) {
      return unavailable(error)
    }
    if (!verdict.allow) return verdict.result
    const result = await next()
    if (verdict.track) program.notify('guard.result', { isError: Boolean(result?.isError), text: textOf(result) })
    return result
  })

  ctx.on('subagent/end', (info) => {
    program.notify('guard.end', { id: info?.id == null ? '' : String(info.id), stopReason: info?.stopReason == null ? '' : String(info.stopReason) })
  })
}
