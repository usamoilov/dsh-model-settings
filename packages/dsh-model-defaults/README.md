# dsh-model-defaults

The **Default model** section of DSH Web settings: one default model for every DSH
profile, a switch that turns DeepSeek off, the `web_search` provider and the subagent
model. Overview, install and configuration: [repository README](../../README.md).

## What a save changes

Only the parts changed on the page are sent and written. Toggling DeepSeek alone does
not touch any profile's default model (so a `headless` profile keeps its own
`reasoningEffort: off`, for example).

| Choice | Rows written in `cordis.patch.yml` |
| --- | --- |
| Default model | `agent-default-model` → `{ provider, model, reasoningEffort? }` |
| DeepSeek off | `disabled: true` on `llm-deepseek` (provider “DeepSeek”, API key) and `llm-deepseek-account` (“DeepSeek Account”) |
| Web search | `web` → `searchProvider` (restating `fetchProvider`: a profile row replaces the whole base config); `web-search-extra` → `claudeModel`, `summaryModel`, `summary`. DeepSeek's search rows `web-search-deepseek`, `web-deepseek-api` stay on only for DeepSeek search, and for Auto while DeepSeek models are on |
| Subagents: Claude | `tool-subagent-claude-code` on; `subagent-claude-code` → `model` (removed for Auto) in every profile and configured overlay; DSH child agents follow the conversation |
| Subagents: DSH model or Auto | `tool-subagent-claude-code` → `disabled: true`; the spawn `tool-subagent` row → `agentOptions: { provider, model }` (removed for Auto). In a profile that defines the agent preset this is the preset's copy, otherwise a top-level row with the base `provider: spawn`, `toolName: subagent` |

Agents can tell the route from their tools: with `subagent_claude_code` present, work goes
to Claude Code; without it, to `subagent`. Rules that say so need no change when the choice changes.

Safety rules: a DeepSeek model cannot be the default while DeepSeek is being turned off, and
DeepSeek cannot be turned off while a profile defaults to it, writes the search answer with it, or
runs subagents on it.

## How changes apply

* **The running profile** (`web`): the default model is saved through DSH's own
  `agentDefaultModel` service — the same path as a pick in the composer. It re-reads the profile
  file and re-applies it live, so turning DeepSeek off usually needs no restart; when the live
  model list still differs from the file, the page offers a restart.
* **Other profiles** (`headless`): `~/.dsh/profiles/<name>/cordis.patch.yml` is edited on disk and
  applies from the next `dsh --profile <name>` run.

Each edit:

* uses the `yaml` library of the DSH install (the one its config editor uses): comments, `!!js`
  expressions and unrelated rows are kept; a no-op leaves the file byte for byte, and flow lists keep
  their style (`[serve]` or `[ serve ]`);
* runs under DSH's profile lock (`@deepseek-ai/dsh-atomic-write`, also taken by the config editor and
  `dsh plugin`) with an atomic write;
* keeps a copy `cordis.patch.yml.bak-model-defaults-<time>` (last 5);
* then checks `dsh --profile <name> [--patch …] --dump-config`: the profile must compose and contain the
  chosen provider, otherwise the file is restored and the page shows the error.

New rows carry a comment `# dsh-model-defaults: … (<time>)` in the page's language. Turning DeepSeek back
on removes only `disabled: true` and drops rows left with nothing else. A `disabled` flag written as a
`!!js` expression is never changed.

## Config

Row `model-defaults` in the web profile, all optional:

| Key | Default | Meaning |
| --- | --- | --- |
| `profiles` | running profile + `headless` (those that exist) | profiles the page manages |
| `webUnit` | — | `systemd --user` unit of DSH Web; enables the restart button |
| `overlays` | `[]` | `[{ path, label, profile, patches }]` extra `--patch` files that restate `subagent-claude-code`; kept in sync with the Claude subagent model and checked with `dsh --profile <profile> --patch <patches…> --patch <path> --dump-config` |
| `searxngURL` | `http://127.0.0.1:8888` | SearXNG address for the health line |

`~` at the start of a path is the home folder.

## Languages

Client texts live in [`lib/client.js`](lib/client.js) (namespace `modelDefaults`, registered with
`ctx.locale.register`); server messages and profile comments in [`lib/i18n.js`](lib/i18n.js). The
browser sends its active DSH locale in `x-dsh-locale`; unknown locales get English. To add a
language, add a dictionary with the same keys to both files (the tests check that every language has
every key and the same `{placeholders}`).

## API

Authenticated DSH routes (the web UI's cookie). `POST` requires `x-dsh-model-defaults: 1` and JSON.

* `GET /api/model-defaults.state[?refreshClaude=1]` — catalog, per-profile state, overlays,
  SearXNG health, `restartNeeded`, `canRestart`, and `claude: { source: 'claude-code' | 'fallback', reason?, models, recommended }`.
* `POST /api/model-defaults.save` —
  ```
  { selection: { provider, model, reasoningEffort } | null,
    apply: { web: true, headless: true },
    deepseek: { models: true },
    search: { provider: 'deepseek-official' | 'searxng' | 'claude-code' | 'auto',
              claudeModel: 'auto' | '<alias or id>', summaryModel: 'auto' | { provider, model }, summary } | null,
    subagents: { kind: 'claude', model: 'auto' | '<alias or id>' } | { kind: 'dsh', ref: 'auto' | { provider, model } } | null }
  ```
  → `{ state, notes }`.
* `POST /api/model-defaults.test-search` — `{ query }` → one search through the live `web` service.
* `POST /api/model-defaults.restart` — `systemctl --user --no-block restart <webUnit>`; only with `webUnit` set.

## Files

| File | Role |
| --- | --- |
| `lib/index.js` | host plugin: routes, state, save |
| `lib/profiles.js` | reading and editing profile patches; compose check |
| `lib/claude-models.js` | Claude Code's model list through the Claude Agent SDK |
| `lib/i18n.js` | server messages (en, ru, zh) |
| `lib/deps.js` | `yaml` and the profile lock, borrowed from the DSH install |
| `lib/client.js` | the settings page (React through DSH's module loader) |
