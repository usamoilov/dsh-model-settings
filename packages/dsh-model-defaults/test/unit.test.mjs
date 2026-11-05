import assert from 'node:assert/strict'
import { applyProfile, deepseekSearchEnabled, isDeepseekProvider, readProfile, subagentChanges } from '../lib/profiles.js'
import { translator } from '../lib/i18n.js'

// A web profile patch: comments, an insert block, !!js expressions, an llm-deepseek row.
const web = `# User patch layer for the web profile (managed by hand).
- insert:
    - id: preset-main
      name: '@deepseek-ai/dsh-agent-preset'
      config:
        plugins:
          - id: tool-bash
            name: '@deepseek-ai/dsh-tool-bash'
            disabled: !!js process.platform === 'win32'
# hand-written note: the default is a local model
- id: agent-default-model
  config:
    provider: local-llm
    model: local-model
- id: llm-pi-ai
  config:
    providers:
      local-llm:
        displayName: Local model
        apiKeyEnv: LOCAL_LLM_KEY
- id: llm-deepseek
  name: "@deepseek-ai/dsh-llm-deepseek-api-key"
  config:
    models:
      - id: deepseek-flash
`

const read = readProfile(web)
assert.deepEqual(read.defaultModel, { provider: 'local-llm', model: 'local-model', reasoningEffort: null })
assert.deepEqual(read.providers, ['local-llm'])
assert.equal(read.deepseek.models, false)
assert.equal(read.deepseek.search, false)

// turn DeepSeek off: flag on the existing row, new rows for the rest; comments and !!js survive
const off = applyProfile(web, {
  disable: { 'llm-deepseek': true, 'llm-deepseek-account': true, 'web-search-deepseek': true, 'web-deepseek-api': true },
  stamp: '04.11.2026',
})
assert.ok(off.includes('# User patch layer for the web profile (managed by hand).'))
assert.ok(off.includes('# hand-written note: the default is a local model'))
assert.ok(off.includes("disabled: !!js process.platform === 'win32'"), '!!js expression kept verbatim')
assert.ok(/- id: llm-deepseek\n {2}name: "@deepseek-ai\/dsh-llm-deepseek-api-key"\n {2}config:\n[\s\S]*?\n {2}disabled: true/.test(off))
assert.ok(off.includes('# dsh-model-defaults: DeepSeek turned off (04.11.2026)\n- id: llm-deepseek-account\n  disabled: true'))
const offRead = readProfile(off)
assert.equal(offRead.deepseek.models, true)
assert.equal(offRead.deepseek.search, true)
assert.deepEqual(offRead.defaultModel, read.defaultModel)
// idempotent
assert.equal(applyProfile(off, { disable: { 'llm-deepseek': true, 'llm-deepseek-account': true } }), off)

// turn it back on: the flag goes, rows that only carried the flag disappear, the original row stays
const on = applyProfile(off, {
  disable: { 'llm-deepseek': false, 'llm-deepseek-account': false, 'web-search-deepseek': false, 'web-deepseek-api': false },
})
assert.ok(!on.includes('disabled: true'))
assert.ok(!on.includes('llm-deepseek-account'))
assert.ok(on.includes('- id: llm-deepseek\n  name: "@deepseek-ai/dsh-llm-deepseek-api-key"'))
assert.ok(on.includes("disabled: !!js process.platform === 'win32'"))
assert.equal(readProfile(on).deepseek.models, false)

// default model: replaced in place (comment above stays), effort added and removed
const withEffort = applyProfile(web, { defaultModel: { provider: 'local-llm', model: 'local-model', reasoningEffort: 'off' } })
assert.ok(withEffort.includes('# hand-written note: the default is a local model\n- id: agent-default-model\n  config:\n    provider: local-llm\n    model: local-model\n    reasoningEffort: off'))
assert.equal(readProfile(withEffort).defaultModel.reasoningEffort, 'off')
const noEffort = applyProfile(withEffort, { defaultModel: { provider: 'deepseek-official', model: 'deepseek-flash' } })
assert.deepEqual(readProfile(noEffort).defaultModel, { provider: 'deepseek-official', model: 'deepseek-flash', reasoningEffort: null })
assert.equal((noEffort.match(/id: agent-default-model/g) || []).length, 1)

// a profile without the row gets one appended; an empty file becomes a list
const bare = applyProfile('', { defaultModel: { provider: 'local-llm', model: 'm' }, stamp: 'x' })
assert.deepEqual(readProfile(bare).defaultModel, { provider: 'local-llm', model: 'm', reasoningEffort: null })
assert.ok(bare.startsWith('# dsh-model-defaults: default model (x)\n- id: agent-default-model'))
assert.deepEqual(readProfile('[]\n').defaultModel, null)

// a disabled flag written as a !!js expression is reported but never removed
const expr = "- id: llm-deepseek\n  disabled: !!js process.env.X === '1'\n"
assert.equal(readProfile(expr).deepseek.rows['llm-deepseek'], 'expression')
assert.ok(applyProfile(expr, { disable: { 'llm-deepseek': false } }).includes("!!js process.env.X === '1'"))

// broken YAML is refused, not overwritten
assert.throws(() => applyProfile('- id: [unclosed\n', { disable: { 'llm-deepseek': true } }), /YAML/)
assert.throws(() => readProfile('id: not-a-list\n'), /YAML list/)

// web search provider: the web row restates fetchProvider; the extra row gets its settings
assert.deepEqual(read.search, { provider: 'deepseek-official', fetchProvider: 'http', claudeModel: null, summaryModel: null, summary: true })
const claude = applyProfile(web, { search: { provider: 'claude-code', claudeModel: 'claude-haiku-5-5' }, stamp: 's' })
assert.deepEqual(readProfile(claude).search, { provider: 'claude-code', fetchProvider: 'http', claudeModel: 'claude-haiku-5-5', summaryModel: null, summary: true })
assert.ok(claude.includes('# dsh-model-defaults: web search provider (s)\n- id: web\n  config:\n    searchProvider: claude-code\n    fetchProvider: http'))
assert.ok(claude.includes('- id: web-search-extra\n  config:\n    claudeModel: claude-haiku-5-5'))
// switching edits the same rows in place; other keys survive
const custom = claude.replace('    fetchProvider: http', '    fetchProvider: http\n    extra: 1')
const sxSearch = applyProfile(custom, { search: { provider: 'searxng', summaryModel: 'auto', summary: false } })
assert.equal((sxSearch.match(/- id: web$/gm) || []).length, 1)
assert.ok(/searchProvider: searxng\n {4}fetchProvider: http\n {4}extra: 1/.test(sxSearch))
assert.deepEqual(readProfile(sxSearch).search, { provider: 'searxng', fetchProvider: 'http', claudeModel: 'claude-haiku-5-5', summaryModel: 'auto', summary: false })
const sxFixed = applyProfile(sxSearch, { search: { provider: 'auto', summaryModel: { provider: 'local-llm', model: 'q' }, summary: true } })
assert.deepEqual(readProfile(sxFixed).search.summaryModel, { provider: 'local-llm', model: 'q' })
assert.equal(readProfile(sxFixed).search.provider, 'auto')
assert.equal(deepseekSearchEnabled('auto', false), true)
assert.equal(deepseekSearchEnabled('auto', true), false)
assert.equal(deepseekSearchEnabled('searxng', false), false)
assert.equal(deepseekSearchEnabled('deepseek-official', true), true)

// --- subagents
const webWithSubagents = `- insert:
    - id: preset-main
      name: '@deepseek-ai/dsh-agent-preset'
      config:
        plugins:
          - id: delegation
            name: cordis:group
            config:
              - id: tool-subagent
                name: '@deepseek-ai/dsh-tool-subagent'
                config:
                  provider: spawn
                  toolName: subagent
                  modelSelectionSettings: true
              - id: tool-subagent-fork
                name: '@deepseek-ai/dsh-tool-subagent'
                config:
                  provider: fork
                  toolName: subagent_fork
- id: subagent-claude-code
  config:
    permissionMode: acceptEdits
    model: claude-opus-5-5
`
const subRead = readProfile(webWithSubagents)
assert.deepEqual(subRead.subagents, { route: 'claude', claudeCode: { model: 'claude-opus-5-5', row: true, tool: true }, dsh: 'auto', dshPlace: 'preset' })
// nested preset copy gets agentOptions; the fork tool is untouched; Claude model → auto drops `model`
const subSet = applyProfile(webWithSubagents, { dshSubagent: { provider: 'local-llm', model: 'q' }, claudeSubagentModel: 'auto' })
assert.ok(/toolName: subagent\n {18}modelSelectionSettings: true\n {18}agentOptions:\n {20}provider: local-llm\n {20}model: q/.test(subSet))
assert.ok(!/subagent_fork\n\s+agentOptions/.test(subSet))
assert.ok(subSet.includes('- id: subagent-claude-code\n  config:\n    permissionMode: acceptEdits\n'))
assert.ok(!/model: claude-opus-5-5/.test(subSet))
assert.deepEqual(readProfile(subSet).subagents, { route: 'claude', claudeCode: { model: 'auto', row: true, tool: true }, dsh: { provider: 'local-llm', model: 'q' }, dshPlace: 'preset' })
const subBack = applyProfile(subSet, { dshSubagent: 'auto', claudeSubagentModel: 'claude-sonnet-5-5' })
assert.ok(!subBack.includes('agentOptions'))
assert.equal(readProfile(subBack).subagents.claudeCode.model, 'claude-sonnet-5-5')
// headless: no preset in the patch → a top-level row restating the base config, removed again on Auto
const headless = '- id: agent-default-model\n  config:\n    provider: local-llm\n    model: q\n'
assert.equal(readProfile(headless).subagents.dshPlace, 'base')
const top = applyProfile(headless, { dshSubagent: { provider: 'deepseek-official', model: 'deepseek-flash' }, stamp: 't' })
assert.ok(top.includes('# dsh-model-defaults: DSH subagent model (t)\n- id: tool-subagent\n  config:\n    provider: spawn\n    toolName: subagent\n    agentOptions:\n      provider: deepseek-official\n      model: deepseek-flash'))
assert.deepEqual(readProfile(top).subagents.dsh, { provider: 'deepseek-official', model: 'deepseek-flash' })
assert.equal(readProfile(top).subagents.dshPlace, 'top')
assert.equal(applyProfile(top, { dshSubagent: 'auto' }).trim(), headless.trim(), 'auto restores the base row')
// Claude Auto on a profile without the row adds nothing; a model adds the row
assert.equal(applyProfile(headless, { claudeSubagentModel: 'auto' }), headless)
// a no-op keeps the file byte for byte; a real edit keeps the file's flow-list style
const tight = '- id: memory\n  config:\n    args: [serve]\n- id: llm-pi-ai\n  config:\n    providers:\n      q:\n        input: [text]\n'
assert.equal(applyProfile(tight, { disable: { 'llm-deepseek': false }, search: undefined }), tight)
const tightEdited = applyProfile(tight, { disable: { 'llm-deepseek': true } })
assert.ok(tightEdited.includes('args: [serve]') && tightEdited.includes('input: [text]'), 'tight style kept')
const padded = tight.replace('[serve]', '[ serve ]').replace('[text]', '[ text ]')
assert.ok(applyProfile(padded, { disable: { 'llm-deepseek': true } }).includes('args: [ serve ]'), 'padded style kept')
// one subagent choice: a DSH model turns the Claude Code tool off and sets the children's route
const toLocal = applyProfile(webWithSubagents, { ...subagentChanges({ kind: 'dsh', ref: { provider: 'local-llm', model: 'q' } }), stamp: 'q' })
assert.ok(toLocal.includes('# dsh-model-defaults: subagents not on Claude — subagent_claude_code tool turned off (q)\n- id: tool-subagent-claude-code\n  disabled: true'))
assert.ok(toLocal.includes('    model: claude-opus-5-5'), 'the Claude model is kept for switching back')
assert.deepEqual(readProfile(toLocal).subagents, { route: 'dsh', claudeCode: { model: 'claude-opus-5-5', row: true, tool: false }, dsh: { provider: 'local-llm', model: 'q' }, dshPlace: 'preset' })
assert.equal(applyProfile(toLocal, subagentChanges({ kind: 'dsh', ref: { provider: 'local-llm', model: 'q' } })), toLocal, 'idempotent')
// Auto: tool off, children inherit the conversation's model
const toAuto = applyProfile(toLocal, subagentChanges({ kind: 'dsh', ref: 'auto' }))
assert.deepEqual(readProfile(toAuto).subagents, { route: 'dsh', claudeCode: { model: 'claude-opus-5-5', row: true, tool: false }, dsh: 'auto', dshPlace: 'preset' })
// back to Claude: the flag-only row disappears, children follow the conversation again
const toClaude = applyProfile(toLocal, subagentChanges({ kind: 'claude', model: 'claude-sonnet-5-5' }))
assert.ok(!toClaude.includes('tool-subagent-claude-code') && !toClaude.includes('agentOptions'))
assert.deepEqual(readProfile(toClaude).subagents, { route: 'claude', claudeCode: { model: 'claude-sonnet-5-5', row: true, tool: true }, dsh: 'auto', dshPlace: 'preset' })
// a hand-written tool row keeps its other keys; a !!js flag is reported and left alone
const handTool = '- id: tool-subagent-claude-code\n  config:\n    maxDepth: provider-managed\n'
const handOff = applyProfile(handTool, subagentChanges({ kind: 'dsh', ref: 'auto' }))
assert.ok(handOff.includes('maxDepth: provider-managed\n  disabled: true'))
assert.equal(applyProfile(handOff, { claudeTool: true }).trim(), handTool.trim())
const exprTool = "- id: tool-subagent-claude-code\n  disabled: !!js process.env.X === '1'\n"
assert.equal(readProfile(exprTool).subagents.claudeCode.tool, 'expression')
assert.ok(applyProfile(exprTool, { claudeTool: true }).includes("!!js process.env.X === '1'"))
assert.deepEqual(subagentChanges(null), {})
// an overlay patch keeps its other keys (here bypassPermissions)
const overlay = '# keep\n- id: subagent-claude-code\n  config:\n    permissionMode: bypassPermissions\n    model: claude-opus-5-5\n'
assert.equal(applyProfile(overlay, { claudeSubagentModel: 'claude-haiku-5-5' }), overlay.replace('claude-opus-5-5', 'claude-haiku-5-5'))
assert.equal(applyProfile(overlay, { claudeSubagentModel: 'auto' }), '# keep\n- id: subagent-claude-code\n  config:\n    permissionMode: bypassPermissions\n')

assert.equal(isDeepseekProvider('deepseek-official'), true)
assert.equal(isDeepseekProvider('deepseek-account'), true)
assert.equal(isDeepseekProvider('local-llm'), false)


// comments on new rows follow the page language
assert.ok(applyProfile('', { defaultModel: { provider: 'p', model: 'm' }, stamp: 'r', t: translator('ru') }).startsWith('# dsh-model-defaults: модель по умолчанию (r)\n'))
assert.ok(applyProfile('', { defaultModel: { provider: 'p', model: 'm' }, t: translator('zh') }).startsWith('# dsh-model-defaults: 默认模型\n'))

console.log('dsh-model-defaults unit (profiles): ok')
