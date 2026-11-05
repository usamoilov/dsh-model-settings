// Live checks on a machine with DSH installed (`dsh` on PATH or DSH_ROOT set). Real profiles are only READ; writes go to a temp copy.
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { loadAtomicWrite, loadYaml } from '../lib/deps.js'
import { applyProfile, checkComposes, editProfile, readProfile, subagentChanges } from '../lib/profiles.js'
import { createClaudeModels, findClaudeSdk } from '../lib/claude-models.js'

const home = os.homedir()
assert.equal(typeof loadYaml().parseDocument, 'function', 'yaml from the DSH install')
assert.equal(typeof (await loadAtomicWrite()).withFileLock, 'function', 'lock helper from the DSH install')

const existing = []
for (const name of ['web', 'headless']) {
  try {
    await fs.access(path.join(home, '.dsh', 'profiles', name, 'cordis.patch.yml'))
    existing.push(name)
  } catch {
    // profile not set up here
  }
}
assert.ok(existing.length, 'at least one DSH profile')

for (const name of existing) {
  const text = await fs.readFile(path.join(home, '.dsh', 'profiles', name, 'cordis.patch.yml'), 'utf8')
  const info = readProfile(text)
  assert.ok(info.defaultModel?.provider, `${name}: default model is read`)
  // flipping DeepSeek and restoring each row's current flag gives the same profile meaning,
  // and every comment line survives (works whether DeepSeek is on or off right now)
  const ids = ['llm-deepseek', 'llm-deepseek-account', 'web-search-deepseek', 'web-deepseek-api']
  const flipped = applyProfile(text, { disable: Object.fromEntries(ids.map((id) => [id, !info.deepseek.models])) })
  assert.equal(readProfile(flipped).deepseek.models, !info.deepseek.models)
  const back = applyProfile(flipped, { disable: Object.fromEntries(ids.map((id) => [id, info.deepseek.rows[id] === true])) })
  assert.deepEqual(readProfile(back), info, `${name}: round trip keeps the meaning`)
  // the same for the subagent choice: to a DSH model and back to the current one
  const current = info.subagents.route === 'dsh' ? { kind: 'dsh', ref: info.subagents.dsh } : { kind: 'claude', model: info.subagents.claudeCode.model }
  const other = current.kind === 'dsh' ? { kind: 'claude', model: 'haiku' } : { kind: 'dsh', ref: 'auto' }
  const away = applyProfile(text, subagentChanges(other))
  assert.equal(readProfile(away).subagents.route, other.kind)
  const home2 = readProfile(applyProfile(away, subagentChanges(current))).subagents
  assert.equal(home2.route, info.subagents.route, `${name}: subagent route restored`)
  if (current.kind === 'claude') assert.equal(home2.claudeCode.model, info.subagents.claudeCode.model)
  else assert.deepEqual(home2.dsh, info.subagents.dsh)
  // the plugin's own stamped notes are recreated with a new stamp; hand-written comments must survive
  for (const line of text.split('\n').filter((l) => l.trim().startsWith('#') && !l.includes('dsh-model-defaults:'))) {
    assert.ok(back.includes(line.trim()), `${name}: comment kept: ${line.trim().slice(0, 50)}`)
  }
  console.log(`${name}: default ${info.defaultModel.provider} / ${info.defaultModel.model}${info.defaultModel.reasoningEffort ? ` (${info.defaultModel.reasoningEffort})` : ''}; ` +
    `DeepSeek models off=${info.deepseek.models}, search off=${info.deepseek.search}; pi-ai providers: ${info.providers.join(', ')}; ` +
    `subagents: ${info.subagents.route === 'dsh' ? `subagent ${JSON.stringify(info.subagents.dsh)}` : `Claude Code ${info.subagents.claudeCode.model}`}`)
}

// editProfile on a temp copy: lock + atomic write + rollback when the profile would not compose
const tmp = await fs.mkdtemp(path.join(os.tmpdir(), 'md-test-'))
const profile = { name: 'temp', dir: tmp }
await fs.writeFile(path.join(tmp, 'package.json'), '{}')
await fs.writeFile(path.join(tmp, 'cordis.patch.yml'), '# keep me\n- id: agent-default-model\n  config:\n    provider: a\n    model: b\n')
const ok = await editProfile(profile, (t) => applyProfile(t, { defaultModel: { provider: 'c', model: 'd' } }), {
  compose: async () => ({ ok: true, output: '', error: null }),
})
assert.equal(ok.changed, true)
assert.ok(ok.backup && (await fs.readFile(ok.backup, 'utf8')).includes('provider: a'), 'backup written')
assert.ok((await fs.readFile(path.join(tmp, 'cordis.patch.yml'), 'utf8')).startsWith('# keep me\n'))
assert.equal(readProfile(await fs.readFile(path.join(tmp, 'cordis.patch.yml'), 'utf8')).defaultModel.provider, 'c')
await assert.rejects(() => editProfile(profile, (t) => applyProfile(t, { defaultModel: { provider: 'x', model: 'y' } }), {
  compose: async () => ({ ok: false, output: '', error: 'boom' }),
}), /rolled back/)
assert.equal(readProfile(await fs.readFile(path.join(tmp, 'cordis.patch.yml'), 'utf8')).defaultModel.provider, 'c', 'rolled back')
const same = await editProfile(profile, (t) => t, { compose: async () => { throw new Error('must not run') } })
assert.equal(same.changed, false)
await fs.rm(tmp, { recursive: true, force: true })

// the real profile composes (read-only check)
const composed = await checkComposes(existing[existing.length - 1])
assert.ok(composed.ok, `${existing[existing.length - 1]} composes: ${composed.error}`)

// the Claude models this machine's Claude Code login offers (no tokens are spent)
const sdkFile = findClaudeSdk()
const claude = await createClaudeModels({ sdkFile }).list()
if (sdkFile) assert.equal(claude.source, 'claude-code', `Claude Code answered: ${claude.reason}`)
console.log(`claude (${claude.source}): ${claude.models.map((m) => m.id).join(', ')}${claude.recommended ? `; default → ${claude.recommended.model}` : ''}`)

console.log('dsh-model-defaults live: ok')
