# dsh-web-search-extra

Extra search providers for DSH's `web_search` tool (`ctx.web`). The conversation model
never goes online itself: `web_search` hands the query to a search provider. This plugin
registers three of them; the profile's `web` row (`searchProvider`) decides which one is
used. The choice is on the settings page of
[`dsh-model-defaults`](../dsh-model-defaults) → Web search, or by hand:

```yaml
- id: web
  config:
    searchProvider: searxng   # or claude-code / auto / deepseek-official
    fetchProvider: http       # a profile row replaces the whole base config, so restate it
```

| Provider | How it searches | Typical time |
| --- | --- | --- |
| `searxng` | results of your SearXNG + an optional short answer by a DSH model | ~2 s without the answer; with a local model up to a minute |
| `claude-code` | Claude through the Claude Code subscription, WebSearch tool only | ~20–30 s |
| `auto` | `deepseek-official` → `searxng` → `claude-code`, the first that answers | as fast as the first one that answers |

DSH's built-in `deepseek-official` stays a DSH provider.

## searxng

* Results come from the SearXNG JSON API (`<searxngURL>/search?format=json`).
* The short answer is written through `ctx.llm` — the same route, key and accounting as
  conversations — by `summaryModel` (`auto` = the DSH default model at that moment). Reasoning is
  off, at most 500 tokens, 90 s timeout. The prompt asks for an answer in the language of the query,
  with `[n]` references to the sources.
* If the model is busy, slow or down, the search still returns the SearXNG links — `web_search`
  never fails because of the model.
* `summary: false` returns the results only; the conversation model reads them.

## claude-code

Each query is a short isolated Claude Code run through the Claude Agent SDK (the one
`subagent_claude_code` uses, from `@deepseek-ai/dsh-subagent-claude-code`): the WebSearch tool only,
no settings, hooks, CLAUDE.md or MCP servers (`settingSources: []`, `strictMcpConfig`), an empty
temporary folder, no saved session. The query goes into the prompt as a JSON string; the answer is
JSON `answer` + `sources`. At most `concurrency` runs at once, `timeoutMs` each. Uses your Claude
subscription limits. `claudeModel: auto` lets Claude Code pick its own default.

## auto

Providers are tried in `autoOrder`; an error or an empty answer moves on to the next one. The
built-in DeepSeek provider is taken from the `ctx.web` registry (if DSH does not expose it, it is
skipped). When DeepSeek's search rows are disabled, Auto starts with SearXNG.

## Configuration

Row `web-search-extra` in each profile, all optional:

| Key | Default | Meaning |
| --- | --- | --- |
| `searxngURL` | `http://127.0.0.1:8888` | SearXNG address |
| `summary` | `true` | write a short answer from the results |
| `summaryModel` | `auto` | `{ provider, model }` that writes the answer, or `auto` = the DSH default model |
| `claudeModel` | `haiku` | Claude Code model alias or id, or `auto` = Claude Code's default |
| `autoOrder` | `[deepseek-official, searxng, claude-code]` | order for `auto` |
| `timeoutMs` / `summaryTimeoutMs` | `55000` / `90000` | limits for a Claude search / for the answer |
| `concurrency` | `2` | Claude runs at once |

## Tests

```bash
node test/unit.test.mjs           # parsing, Auto order, the model answer, the queue (Node.js only)
node test/live.test.mjs           # a real SearXNG query; Auto without DeepSeek
node test/live.test.mjs --claude  # plus one Claude search (uses your Claude limits)
```
