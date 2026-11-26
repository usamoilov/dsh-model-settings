package profile

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/usamoilov/dsh-model-settings/internal/i18n"
)

// A web profile patch: comments, an insert block, !!js expressions, an llm-deepseek row.
const webPatch = `# User patch layer for the web profile (managed by hand).
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

func mustRead(t *testing.T, text string) Info {
	t.Helper()
	info, err := Read(text)
	if err != nil {
		t.Fatalf("read: %v\n%s", err, text)
	}
	return info
}

func mustApply(t *testing.T, text string, ch Changes) string {
	t.Helper()
	out, err := Apply(text, ch)
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, text)
	}
	return out
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func eqJSON(t *testing.T, got any, want string, what string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal([]byte(jsonOf(t, got)), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("bad want: %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("%s:\n got  %s\n want %s", what, jsonOf(t, got), want)
	}
}

func has(t *testing.T, text, part, what string) {
	t.Helper()
	if !strings.Contains(text, part) {
		t.Fatalf("%s: missing %q in\n%s", what, part, text)
	}
}

func hasNot(t *testing.T, text, part, what string) {
	t.Helper()
	if strings.Contains(text, part) {
		t.Fatalf("%s: unexpected %q in\n%s", what, part, text)
	}
}

func matches(t *testing.T, text, re, what string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(text) {
		t.Fatalf("%s: no match for %s in\n%s", what, re, text)
	}
}

func allDeepseek(off bool) []Toggle {
	ids := append(append([]string{}, DeepseekRows.Models...), DeepseekRows.Search...)
	out := make([]Toggle, len(ids))
	for i, id := range ids {
		out[i] = Toggle{ID: id, Off: off}
	}
	return out
}

func TestDeepseekAndDefaults(t *testing.T) {
	read := mustRead(t, webPatch)
	eqJSON(t, read.DefaultModel, `{"provider":"local-llm","model":"local-model","reasoningEffort":null}`, "default model")
	eqJSON(t, read.Providers, `["local-llm"]`, "providers")
	if read.Deepseek.Models || read.Deepseek.Search {
		t.Fatal("deepseek is on")
	}

	// turn DeepSeek off: flag on the existing row, new rows for the rest; comments and !!js survive
	off := mustApply(t, webPatch, Changes{Disable: allDeepseek(true), Stamp: "04.11.2026"})
	has(t, off, "# User patch layer for the web profile (managed by hand).", "header comment")
	has(t, off, "# hand-written note: the default is a local model", "hand comment")
	has(t, off, "disabled: !!js process.platform === 'win32'", "!!js kept verbatim")
	matches(t, off, `- id: llm-deepseek\n {2}name: "@deepseek-ai/dsh-llm-deepseek-api-key"\n {2}config:\n[\s\S]*?\n {2}disabled: true`, "flag on the row")
	has(t, off, "# dsh-model-defaults: DeepSeek turned off (04.11.2026)\n- id: llm-deepseek-account\n  disabled: true", "new row")
	offRead := mustRead(t, off)
	if !offRead.Deepseek.Models || !offRead.Deepseek.Search {
		t.Fatal("deepseek not off")
	}
	eqJSON(t, offRead.DefaultModel, jsonOf(t, read.DefaultModel), "default kept")
	if got := mustApply(t, off, Changes{Disable: []Toggle{{"llm-deepseek", true}, {"llm-deepseek-account", true}}}); got != off {
		t.Fatalf("not idempotent:\n%s", got)
	}

	// back on: the flag goes, rows that only carried it disappear, the original row stays
	on := mustApply(t, off, Changes{Disable: allDeepseek(false)})
	hasNot(t, on, "disabled: true", "flags gone")
	hasNot(t, on, "llm-deepseek-account", "flag-only row gone")
	has(t, on, "- id: llm-deepseek\n  name: \"@deepseek-ai/dsh-llm-deepseek-api-key\"", "row kept")
	has(t, on, "disabled: !!js process.platform === 'win32'", "!!js kept")
	hasNot(t, on, "DeepSeek turned off", "marker comments gone")
	if mustRead(t, on).Deepseek.Models {
		t.Fatal("still off")
	}
	if on != webPatch {
		t.Fatalf("round trip differs:\n%s", on)
	}

	// default model: replaced in place (comment above stays), effort added and removed
	withEffort := mustApply(t, webPatch, Changes{DefaultModel: &Selection{"local-llm", "local-model", "off"}})
	has(t, withEffort, "# hand-written note: the default is a local model\n- id: agent-default-model\n  config:\n    provider: local-llm\n    model: local-model\n    reasoningEffort: off", "effort added")
	if e := mustRead(t, withEffort).DefaultModel.Effort(); e != "off" {
		t.Fatalf("effort %q", e)
	}
	noEffort := mustApply(t, withEffort, Changes{DefaultModel: &Selection{Provider: "deepseek-official", Model: "deepseek-flash"}})
	eqJSON(t, mustRead(t, noEffort).DefaultModel, `{"provider":"deepseek-official","model":"deepseek-flash","reasoningEffort":null}`, "effort removed")
	if n := strings.Count(noEffort, "id: agent-default-model"); n != 1 {
		t.Fatalf("rows: %d", n)
	}

	// a profile without the row gets one appended; an empty file becomes a list
	bare := mustApply(t, "", Changes{DefaultModel: &Selection{Provider: "local-llm", Model: "m"}, Stamp: "x"})
	eqJSON(t, mustRead(t, bare).DefaultModel, `{"provider":"local-llm","model":"m","reasoningEffort":null}`, "bare")
	if !strings.HasPrefix(bare, "# dsh-model-defaults: default model (x)\n- id: agent-default-model") {
		t.Fatalf("bare:\n%s", bare)
	}
	if mustRead(t, "[]\n").DefaultModel != nil {
		t.Fatal("[] has a default")
	}

	// a disabled flag written as a !!js expression is reported but never removed
	expr := "- id: llm-deepseek\n  disabled: !!js process.env.X === '1'\n"
	eqJSON(t, mustRead(t, expr).Deepseek.Rows["llm-deepseek"], `"expression"`, "expression flag")
	has(t, mustApply(t, expr, Changes{Disable: []Toggle{{"llm-deepseek", false}}}), "!!js process.env.X === '1'", "expression kept")

	// broken YAML is refused, not overwritten
	if _, err := Apply("- id: [unclosed\n", Changes{Disable: []Toggle{{"llm-deepseek", true}}}); err == nil || !strings.Contains(err.Error(), "YAML") {
		t.Fatalf("broken yaml: %v", err)
	}
	if _, err := Read("id: not-a-list\n"); err == nil || !strings.Contains(err.Error(), "YAML list") {
		t.Fatalf("not a list: %v", err)
	}
}

func TestSearch(t *testing.T) {
	read := mustRead(t, webPatch)
	eqJSON(t, read.Search, `{"provider":"deepseek-official","fetchProvider":"http","claudeModel":null,"summaryModel":null,"summary":true}`, "base search")
	claude := mustApply(t, webPatch, Changes{Search: &SearchChange{Provider: "claude-code", ClaudeModel: "claude-haiku-5-5"}, Stamp: "s"})
	eqJSON(t, mustRead(t, claude).Search, `{"provider":"claude-code","fetchProvider":"http","claudeModel":"claude-haiku-5-5","summaryModel":null,"summary":true}`, "claude search")
	has(t, claude, "# dsh-model-defaults: web search provider (s)\n- id: web\n  config:\n    searchProvider: claude-code\n    fetchProvider: http", "web row")
	has(t, claude, "- id: web-search-extra\n  config:\n    claudeModel: claude-haiku-5-5", "extra row")

	// switching edits the same rows in place; other keys survive
	custom := strings.Replace(claude, "    fetchProvider: http", "    fetchProvider: http\n    extra: 1", 1)
	auto := AutoChoice
	summaryOff := false
	sx := mustApply(t, custom, Changes{Search: &SearchChange{Provider: "searxng", SummaryModel: &auto, Summary: &summaryOff}})
	if n := len(regexp.MustCompile(`(?m)^- id: web$`).FindAllString(sx, -1)); n != 1 {
		t.Fatalf("web rows: %d", n)
	}
	matches(t, sx, `searchProvider: searxng\n {4}fetchProvider: http\n {4}extra: 1`, "in place")
	eqJSON(t, mustRead(t, sx).Search, `{"provider":"searxng","fetchProvider":"http","claudeModel":"claude-haiku-5-5","summaryModel":"auto","summary":false}`, "searxng")
	local := RefChoice("local-llm", "q")
	summaryOn := true
	fixed := mustApply(t, sx, Changes{Search: &SearchChange{Provider: "auto", SummaryModel: &local, Summary: &summaryOn}})
	eqJSON(t, mustRead(t, fixed).Search.SummaryModel, `{"provider":"local-llm","model":"q"}`, "summary model")
	if p := mustRead(t, fixed).Search.Provider; p != "auto" {
		t.Fatal(p)
	}
	if !DeepseekSearchEnabled("auto", false) || DeepseekSearchEnabled("auto", true) || DeepseekSearchEnabled("searxng", false) || !DeepseekSearchEnabled("deepseek-official", true) {
		t.Fatal("deepseekSearchEnabled")
	}
}

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

func sub(kind, model string, ref Choice) *SubagentChoice {
	return &SubagentChoice{Kind: kind, Model: model, Ref: ref}
}

func withSub(choice *SubagentChoice, stamp string) Changes {
	ch := Changes{Stamp: stamp}
	SubagentChanges(choice, &ch)
	return ch
}

func TestSubagents(t *testing.T) {
	eqJSON(t, mustRead(t, webWithSubagents).Subagents, `{"route":"claude","claudeCode":{"model":"claude-opus-5-5","row":true,"tool":true},"dsh":"auto","dshPlace":"preset"}`, "read")
	local := RefChoice("local-llm", "q")
	// nested preset copy gets agentOptions; the fork tool is untouched; Claude model → auto drops `model`
	set := mustApply(t, webWithSubagents, Changes{DSHSubagent: &local, ClaudeSubagentModel: "auto"})
	matches(t, set, `toolName: subagent\n {18}modelSelectionSettings: true\n {18}agentOptions:\n {20}provider: local-llm\n {20}model: q`, "nested agentOptions")
	if regexp.MustCompile(`subagent_fork\n\s+agentOptions`).MatchString(set) {
		t.Fatal("fork touched")
	}
	has(t, set, "- id: subagent-claude-code\n  config:\n    permissionMode: acceptEdits\n", "model dropped")
	hasNot(t, set, "model: claude-opus-5-5", "model dropped")
	eqJSON(t, mustRead(t, set).Subagents, `{"route":"claude","claudeCode":{"model":"auto","row":true,"tool":true},"dsh":{"provider":"local-llm","model":"q"},"dshPlace":"preset"}`, "after set")
	auto := AutoChoice
	back := mustApply(t, set, Changes{DSHSubagent: &auto, ClaudeSubagentModel: "claude-sonnet-5-5"})
	hasNot(t, back, "agentOptions", "agentOptions removed")
	if m := mustRead(t, back).Subagents.ClaudeCode.Model; m != "claude-sonnet-5-5" {
		t.Fatal(m)
	}

	// headless: no preset in the patch → a top-level row restating the base config, removed again on Auto
	headless := "- id: agent-default-model\n  config:\n    provider: local-llm\n    model: q\n"
	if p := mustRead(t, headless).Subagents.DSHPlace; p != "base" {
		t.Fatal(p)
	}
	ds := RefChoice("deepseek-official", "deepseek-flash")
	top := mustApply(t, headless, Changes{DSHSubagent: &ds, Stamp: "t"})
	has(t, top, "# dsh-model-defaults: DSH subagent model (t)\n- id: tool-subagent\n  config:\n    provider: spawn\n    toolName: subagent\n    agentOptions:\n      provider: deepseek-official\n      model: deepseek-flash", "top row")
	eqJSON(t, mustRead(t, top).Subagents.DSH, `{"provider":"deepseek-official","model":"deepseek-flash"}`, "top dsh")
	if p := mustRead(t, top).Subagents.DSHPlace; p != "top" {
		t.Fatal(p)
	}
	if got := mustApply(t, top, Changes{DSHSubagent: &auto}); strings.TrimSpace(got) != strings.TrimSpace(headless) {
		t.Fatalf("auto restores the base row:\n%s", got)
	}
	// Claude Auto on a profile without the row adds nothing
	if got := mustApply(t, headless, Changes{ClaudeSubagentModel: "auto"}); got != headless {
		t.Fatalf("claude auto:\n%s", got)
	}

	// a no-op keeps the file byte for byte; a real edit keeps the file's flow-list style
	tight := "- id: memory\n  config:\n    args: [serve]\n- id: llm-pi-ai\n  config:\n    providers:\n      q:\n        input: [text]\n"
	if got := mustApply(t, tight, Changes{Disable: []Toggle{{"llm-deepseek", false}}}); got != tight {
		t.Fatalf("no-op changed:\n%s", got)
	}
	tightEdited := mustApply(t, tight, Changes{Disable: []Toggle{{"llm-deepseek", true}}})
	has(t, tightEdited, "args: [serve]", "tight")
	has(t, tightEdited, "input: [text]", "tight")
	padded := strings.NewReplacer("[serve]", "[ serve ]", "[text]", "[ text ]").Replace(tight)
	has(t, mustApply(t, padded, Changes{Disable: []Toggle{{"llm-deepseek", true}}}), "args: [ serve ]", "padded")

	// one subagent choice: a DSH model turns the Claude Code tool off and sets the children's route
	toLocal := mustApply(t, webWithSubagents, withSub(sub("dsh", "", local), "q"))
	has(t, toLocal, "# dsh-model-defaults: subagents not on Claude — subagent_claude_code tool turned off (q)\n- id: tool-subagent-claude-code\n  disabled: true", "tool off")
	has(t, toLocal, "    model: claude-opus-5-5", "the Claude model is kept for switching back")
	eqJSON(t, mustRead(t, toLocal).Subagents, `{"route":"dsh","claudeCode":{"model":"claude-opus-5-5","row":true,"tool":false},"dsh":{"provider":"local-llm","model":"q"},"dshPlace":"preset"}`, "to local")
	if got := mustApply(t, toLocal, withSub(sub("dsh", "", local), "")); got != toLocal {
		t.Fatalf("not idempotent:\n%s", got)
	}
	toAuto := mustApply(t, toLocal, withSub(sub("dsh", "", AutoChoice), ""))
	eqJSON(t, mustRead(t, toAuto).Subagents, `{"route":"dsh","claudeCode":{"model":"claude-opus-5-5","row":true,"tool":false},"dsh":"auto","dshPlace":"preset"}`, "to auto")
	toClaude := mustApply(t, toLocal, withSub(sub("claude", "claude-sonnet-5-5", Choice{}), ""))
	hasNot(t, toClaude, "tool-subagent-claude-code", "tool row gone")
	hasNot(t, toClaude, "agentOptions", "options gone")
	hasNot(t, toClaude, "subagent_claude_code tool turned off", "marker gone")
	eqJSON(t, mustRead(t, toClaude).Subagents, `{"route":"claude","claudeCode":{"model":"claude-sonnet-5-5","row":true,"tool":true},"dsh":"auto","dshPlace":"preset"}`, "to claude")

	// a hand-written tool row keeps its other keys; an expression flag is reported and left alone
	handTool := "- id: tool-subagent-claude-code\n  config:\n    maxDepth: provider-managed\n"
	handOff := mustApply(t, handTool, withSub(sub("dsh", "", AutoChoice), ""))
	has(t, handOff, "maxDepth: provider-managed\n  disabled: true", "hand row")
	on := true
	if got := mustApply(t, handOff, Changes{ClaudeTool: &on}); strings.TrimSpace(got) != strings.TrimSpace(handTool) {
		t.Fatalf("hand row back:\n%s", got)
	}
	exprTool := "- id: tool-subagent-claude-code\n  disabled: !!js process.env.X === '1'\n"
	eqJSON(t, mustRead(t, exprTool).Subagents.ClaudeCode.Tool, `"expression"`, "expression tool")
	has(t, mustApply(t, exprTool, Changes{ClaudeTool: &on}), "!!js process.env.X === '1'", "expression kept")
	var none Changes
	SubagentChanges(nil, &none)
	if none.ClaudeTool != nil || none.DSHSubagent != nil {
		t.Fatal("nil choice changed something")
	}

	// an overlay patch keeps its other keys (here bypassPermissions)
	overlay := "# keep\n- id: subagent-claude-code\n  config:\n    permissionMode: bypassPermissions\n    model: claude-opus-5-5\n"
	if got := mustApply(t, overlay, Changes{ClaudeSubagentModel: "claude-haiku-5-5"}); got != strings.Replace(overlay, "claude-opus-5-5", "claude-haiku-5-5", 1) {
		t.Fatalf("overlay:\n%s", got)
	}
	if got := mustApply(t, overlay, Changes{ClaudeSubagentModel: "auto"}); got != "# keep\n- id: subagent-claude-code\n  config:\n    permissionMode: bypassPermissions\n" {
		t.Fatalf("overlay auto:\n%s", got)
	}

	if !IsDeepseekProvider("deepseek-official") || !IsDeepseekProvider("deepseek-account") || IsDeepseekProvider("local-llm") {
		t.Fatal("isDeepseekProvider")
	}
}

func TestCommentsFollowLanguage(t *testing.T) {
	ru := mustApply(t, "", Changes{DefaultModel: &Selection{Provider: "p", Model: "m"}, Stamp: "r", T: i18n.Translator("ru")})
	if !strings.HasPrefix(ru, "# dsh-model-defaults: модель по умолчанию (r)\n") {
		t.Fatal(ru)
	}
	zh := mustApply(t, "", Changes{DefaultModel: &Selection{Provider: "p", Model: "m"}, T: i18n.Translator("zh")})
	if !strings.HasPrefix(zh, "# dsh-model-defaults: 默认模型\n") {
		t.Fatal(zh)
	}
}
