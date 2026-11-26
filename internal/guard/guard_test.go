package guard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var (
	local = &Route{"local-llm", "local-model"}
	other = &Route{"paid-api", "big-model"}
)

func base(chosen Chosen) Input {
	return Input{Tool: "subagent", Kind: "spawn", Chosen: chosen, Parent: local, DefaultModel: local}
}

func must(t *testing.T, cond bool, what string) {
	t.Helper()
	if !cond {
		t.Fatal(what)
	}
}

func TestClaudeChosen(t *testing.T) {
	claude := Chosen{Kind: "claude"}
	v := Decide(base(claude))
	must(t, !v.Allow, "refused")
	must(t, strings.Contains(v.Message, "call `subagent_claude_code`"), v.Message)
	must(t, strings.Contains(v.Message, "local-llm/local-model"), v.Message)
	must(t, strings.Contains(v.Message, "Only after a run on the chosen model fails"), v.Message)
	in := base(claude)
	in.FallbackOpen = true
	v = Decide(in)
	must(t, v.Allow && v.Fallback, "the default model stands in")
	in.Parent = other
	v = Decide(in)
	must(t, !v.Allow, "the stand-in is the default model only")
	must(t, strings.Contains(v.Message, "would use paid-api/big-model"), v.Message)
	fork := base(claude)
	fork.Kind, fork.Tool = "fork", "subagent_fork"
	must(t, !Decide(fork).Allow, "fork refused")
	wf := base(claude)
	wf.Kind, wf.Tool = "workflow", "workflow"
	must(t, !Decide(wf).Allow, "workflow refused")
}

func TestDSHChosen(t *testing.T) {
	paid := Chosen{Kind: "dsh", Ref: other}
	must(t, Decide(base(paid)).Allow, "spawn carries the chosen model")
	in := base(paid)
	in.Explicit = Explicit{Route: local}
	must(t, !Decide(in).Allow, "an explicit other model is refused")
	fork := base(paid)
	fork.Kind, fork.Tool = "fork", "subagent_fork"
	v := Decide(fork)
	must(t, !v.Allow && strings.Contains(v.Message, "Use `subagent`: it runs on the chosen model"), v.Message)
	fork.Parent = other
	must(t, Decide(fork).Allow, "fork on the chosen model")
	fork.Parent, fork.FallbackOpen = local, true
	must(t, Decide(fork).Fallback, "fork on the default model after a failure")
	must(t, Decide(base(Chosen{Kind: "dsh", Ref: local})).Allow, "chosen = conversation")
}

func TestAuto(t *testing.T) {
	auto := Chosen{Kind: "auto"}
	must(t, Decide(base(auto)).Allow, "follows the conversation")
	in := base(auto)
	in.Explicit = Explicit{Route: local}
	must(t, Decide(in).Allow, "same model asked")
	in.Explicit = Explicit{Route: other}
	v := Decide(in)
	must(t, !v.Allow && strings.Contains(v.Message, "conversation's model (local-llm/local-model)"), v.Message)
}

func TestWorkflowAndRoutes(t *testing.T) {
	must(t, ExplicitRoute("workflow", map[string]any{"script": `await agent("x", { provider: "paid-api", model: "big" })`}).Unknown, "picks models")
	must(t, !ExplicitRoute("workflow", map[string]any{"script": `await agent("x")`}).Unknown, "no model")
	r := ExplicitRoute("spawn", map[string]any{"provider": "p", "model": "m", "prompt": "x"}).Route
	must(t, r != nil && *r == Route{"p", "m"}, "spawn route")
	must(t, ExplicitRoute("spawn", map[string]any{"provider": "p"}).Route == nil, "provider without model is no choice")
	in := base(Chosen{Kind: "auto"})
	in.Kind, in.Tool, in.Explicit = "workflow", "workflow", Explicit{Unknown: true}
	v := Decide(in)
	must(t, !v.Allow && strings.Contains(v.Message, "workflow script may not choose provider/model"), v.Message)
	must(t, RouteOf(map[string]any{"provider": "a", "model": "b", "maxTokens": 5.0}) != nil, "route with extras")
	must(t, RouteOf(nil) == nil, "nil route")
	must(t, StartedID("ok, started subagent child-1 now") == "child-1", "started id")
	refusal := Refusal("no")
	must(t, refusal["isError"] == true, "refusal is an error")
}

func TestFallback(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	fb := NewFallback(60, func() time.Time { return clock })
	must(t, !fb.Open(), "closed")
	fb.Track("a")
	must(t, fb.End("unrelated", "error") == "", "runs on other models do not count")
	must(t, !fb.Open(), "still closed")
	must(t, fb.End("a", "error") == "failed", "failed")
	must(t, fb.Open(), "open")
	clock = clock.Add(59 * time.Minute)
	must(t, fb.Open(), "open within the hour")
	clock = clock.Add(2 * time.Minute)
	must(t, !fb.Open(), "at most an hour")
	fb.Failed()
	fb.Track("b")
	must(t, fb.End("b", "completed") == "completed", "completed")
	must(t, !fb.Open(), "a completed chosen run closes it")
	never := NewFallback(0, nil)
	never.Failed()
	must(t, !never.Open(), "0 = never")
}

func TestConfig(t *testing.T) {
	for raw, want := range map[string]float64{
		``: 60, `{}`: 60, `{"fallbackMinutes":15}`: 15, `{"fallbackMinutes":-1}`: 60, `{"fallbackMinutes":"5"}`: 5, `{"fallbackMinutes":null}`: 0,
	} {
		if got := ReadConfig(json.RawMessage(raw)); got != want {
			t.Errorf("%s → %v", raw, got)
		}
	}
}
