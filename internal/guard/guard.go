// Package guard enforces the subagent choice of the settings page: delegated
// work runs only on the chosen subagent model.
//
// Every call of a DSH delegation tool (`subagent`, `subagent_fork`, `workflow`)
// is checked before it starts a child:
//   - Claude Code chosen (the `subagent_claude_code` tool is present): DSH children are refused;
//   - a DSH model chosen: a child on any other model is refused;
//   - Auto (the conversation's model): a child that asks for another model is refused.
//
// When a run on the chosen model ends with an error (a limit, an outage,
// billing — any reason), children on the default model are allowed until the
// chosen model completes a run again, for at most `fallbackMinutes`. Then the
// chosen model is tried first again.
//
// Config (row `model-defaults-guard`, all optional):
//
//	fallbackMinutes   how long a failure of the chosen model opens the default model (default 60, 0 = never)
package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/usamoilov/dsh-model-settings/internal/host"
	"github.com/usamoilov/dsh-model-settings/internal/profile"
)

// DelegationTools are the DSH delegation tools and how each one picks its child's model.
var DelegationTools = map[string]string{"subagent": "spawn", "subagent_fork": "fork", "workflow": "workflow"}

// Tool names and the refusal code.
const (
	ClaudeTool = "subagent_claude_code"
	Code       = "SUBAGENT_MODEL_NOT_CHOSEN"
)

var started = regexp.MustCompile(`started subagent ([\w-]+)`)
var workflowModel = regexp.MustCompile(`\b(provider|model)\s*:`)

// Route is a provider/model pair; the zero Route is "none".
type Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (r *Route) ok() bool { return r != nil && r.Provider != "" && r.Model != "" }

// RouteOf reads a provider/model pair from loose JSON (both must be non-empty strings).
func RouteOf(raw any) *Route {
	m, _ := raw.(map[string]any)
	p, _ := m["provider"].(string)
	model, _ := m["model"].(string)
	if p == "" || model == "" {
		return nil
	}
	return &Route{p, model}
}

func same(a, b *Route) bool { return a.ok() && b.ok() && *a == *b }

func show(r *Route) string {
	if !r.ok() {
		return "an unknown model"
	}
	return r.Provider + "/" + r.Model
}

// Chosen is the subagent choice: "claude", "dsh" (with Ref) or "auto".
type Chosen struct {
	Kind string
	Ref  *Route
}

// Explicit is the child's model as the call asks for it.
type Explicit struct {
	Unknown bool   // a workflow script that sets provider/model for its agents
	Route   *Route // nil: the call names none (the child gets the tool's default)
}

// ExplicitRoute reads the model a delegation call asks for.
func ExplicitRoute(kind string, args map[string]any) Explicit {
	if kind == "workflow" {
		script := ""
		switch s := args["script"].(type) {
		case string:
			script = s
		case nil:
		default:
			script = fmt.Sprint(s)
		}
		return Explicit{Unknown: workflowModel.MatchString(script)}
	}
	return Explicit{Route: RouteOf(args)}
}

// Input of one decision.
type Input struct {
	Tool, Kind   string
	Chosen       Chosen
	Explicit     Explicit
	Parent       *Route
	DefaultModel *Route
	FallbackOpen bool
}

// Verdict of one decision.
type Verdict struct {
	Allow    bool
	Fallback bool
	Child    *Route
	Message  string
}

// Decide allows or refuses one delegation.
func Decide(in Input) Verdict {
	// the spawn tool carries the chosen DSH model as its default; fork and workflow children inherit the conversation
	var pinned *Route
	if in.Kind == "spawn" && in.Chosen.Kind == "dsh" {
		pinned = in.Chosen.Ref
	}
	child := in.Parent
	switch {
	case in.Explicit.Unknown:
		child = nil
	case in.Explicit.Route != nil:
		child = in.Explicit.Route
	case pinned != nil:
		child = pinned
	}
	if in.Explicit.Unknown {
		return Verdict{Child: child, Message: "Refused by the owner's subagent setting: a workflow script may not choose provider/model for its agents. " + chosenText(in.Chosen, in.Parent)}
	}
	if in.Chosen.Kind == "auto" {
		if in.Explicit.Route == nil || same(in.Explicit.Route, in.Parent) {
			return Verdict{Allow: true, Child: child}
		}
		return Verdict{Child: child, Message: fmt.Sprintf("Refused by the owner's subagent setting: subagents run on the conversation's model (%s); this `%s` call asks for %s. Call it without provider/model.", show(in.Parent), in.Tool, show(in.Explicit.Route))}
	}
	if in.Chosen.Kind == "dsh" && same(child, in.Chosen.Ref) {
		return Verdict{Allow: true, Child: child}
	}
	if in.FallbackOpen && same(child, in.DefaultModel) {
		return Verdict{Allow: true, Fallback: true, Child: child}
	}
	fallbackText := fmt.Sprintf(" Only after a run on the chosen model fails (an error, a limit, an outage) are children on the default model (%s) allowed for a while.", show(in.DefaultModel))
	if in.FallbackOpen {
		fallbackText = fmt.Sprintf(" A run on the chosen model failed, so children on the default model (%s) are allowed for now, but this call would use %s.", show(in.DefaultModel), show(child))
	}
	if in.Chosen.Kind == "claude" {
		return Verdict{Child: child, Message: fmt.Sprintf("Refused by the owner's subagent setting: delegated work goes to Claude Code — call `%s`. `%s` would start a DSH child on %s.%s", ClaudeTool, in.Tool, show(child), fallbackText)}
	}
	hint := ""
	if in.Kind != "spawn" {
		hint = " Use `subagent`: it runs on the chosen model."
	}
	return Verdict{Child: child, Message: fmt.Sprintf("Refused by the owner's subagent setting: subagents run on %s. `%s` would start a child on %s.%s%s", show(in.Chosen.Ref), in.Tool, show(child), hint, fallbackText)}
}

func chosenText(c Chosen, parent *Route) string {
	switch c.Kind {
	case "claude":
		return fmt.Sprintf("Delegated work goes to Claude Code — call `%s`.", ClaudeTool)
	case "dsh":
		return fmt.Sprintf("Subagents run on %s — use `subagent`.", show(c.Ref))
	}
	return fmt.Sprintf("Subagents run on the conversation's model (%s).", show(parent))
}

// Refusal is the tool result the model gets instead of a child.
func Refusal(message string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "Error: " + message}},
		"isError": true,
		"error":   map[string]any{"message": message, "info": map[string]any{"name": "SubagentModelGuard", "code": Code}},
	}
}

// Fallback remembers failures of the chosen model; Open says whether the default model may stand in.
type Fallback struct {
	mu       sync.Mutex
	minutes  float64
	now      func() time.Time
	runs     map[string]bool
	failedAt time.Time
}

// NewFallback makes the window; now = nil uses the clock.
func NewFallback(minutes float64, now func() time.Time) *Fallback {
	if now == nil {
		now = time.Now
	}
	return &Fallback{minutes: minutes, now: now, runs: map[string]bool{}}
}

// Track remembers a started run of the chosen model.
func (f *Fallback) Track(id string) {
	if id == "" {
		return
	}
	f.mu.Lock()
	f.runs[id] = true
	f.mu.Unlock()
}

// Failed opens the window now.
func (f *Fallback) Failed() {
	f.mu.Lock()
	f.failedAt = f.now()
	f.mu.Unlock()
}

// End takes the end of a run: "failed", "completed" or "" when it was not one of the chosen model's.
func (f *Fallback) End(id, stopReason string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.runs[id] {
		return ""
	}
	delete(f.runs, id)
	switch stopReason {
	case "error":
		f.failedAt = f.now()
		return "failed"
	case "completed":
		f.failedAt = time.Time{}
		return "completed"
	}
	return ""
}

// Open: a failure within the last fallbackMinutes.
func (f *Fallback) Open() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.minutes > 0 && !f.failedAt.IsZero() && f.now().Sub(f.failedAt) < time.Duration(f.minutes*float64(time.Minute))
}

// ReadConfig reads fallbackMinutes (default 60; a negative or non-number value gives 60).
func ReadConfig(raw json.RawMessage) float64 {
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	v, present := cfg["fallbackMinutes"]
	switch x := v.(type) {
	case float64:
		if x >= 0 {
			return x
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil && f >= 0 {
			return f
		}
	case nil:
		if present {
			return 0 // Number(null) is 0
		}
	}
	return 60
}

// StartedID finds the child id in a tool result text.
func StartedID(text string) string {
	if m := started.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// Start sets up the guard and answers the shim's hello.
func Start(h *host.Host) (any, error) {
	minutes := ReadConfig(h.Hello.Config)
	fallback := NewFallback(minutes, nil)
	running := h.Running()
	file := running.PatchFile()
	var mu sync.Mutex
	var cacheMtime time.Time
	var cache *profile.Info

	// the profile's subagent choice for DSH children (cached by file time)
	dshChoice := func() (Chosen, *profile.Info) {
		mu.Lock()
		defer mu.Unlock()
		st, err := os.Stat(file)
		if err != nil {
			cache, cacheMtime = nil, time.Time{}
		} else if !st.ModTime().Equal(cacheMtime) || cache == nil {
			cacheMtime = st.ModTime()
			cache = nil
			if text, err := os.ReadFile(file); err == nil {
				if info, err := profile.Read(string(text)); err == nil {
					cache = &info
				}
			}
		}
		if cache != nil && !cache.Subagents.DSH.Auto && cache.Subagents.DSH.Ref.Provider != "" {
			ref := cache.Subagents.DSH.Ref
			return Chosen{Kind: "dsh", Ref: &Route{ref.Provider, ref.Model}}, cache
		}
		return Chosen{Kind: "auto"}, cache
	}

	h.Conn.Handle("guard.check", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			Tool         string         `json:"tool"`
			Arguments    map[string]any `json:"arguments"`
			ClaudeTool   bool           `json:"claudeTool"`
			Header       any            `json:"header"`
			Options      any            `json:"options"`
			DefaultModel any            `json:"defaultModel"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		kind, ok := DelegationTools[p.Tool]
		if !ok {
			return map[string]any{"allow": true}, nil
		}
		// read the profile every time: it also refreshes the default model for scopes without the live service
		chosen, info := dshChoice()
		if p.ClaudeTool {
			chosen = Chosen{Kind: "claude"}
		}
		parent := RouteOf(p.Header)
		if parent == nil {
			parent = RouteOf(p.Options)
		}
		def := RouteOf(p.DefaultModel)
		if def == nil && info != nil && info.DefaultModel != nil {
			def = &Route{info.DefaultModel.ProviderID(), info.DefaultModel.ModelID()}
		}
		v := Decide(Input{Tool: p.Tool, Kind: kind, Chosen: chosen, Explicit: ExplicitRoute(kind, p.Arguments), Parent: parent, DefaultModel: def, FallbackOpen: fallback.Open()})
		if !v.Allow {
			c := chosen.Kind
			if chosen.Kind == "dsh" {
				c = show(chosen.Ref)
			}
			h.Info("guard refused %s on %s (profile %s, chosen %s)", p.Tool, show(v.Child), running.Name, c)
			return map[string]any{"allow": false, "result": Refusal(v.Message)}, nil
		}
		if v.Fallback {
			h.Info("guard: %s on the default model %s after a failed run of the chosen model", p.Tool, show(v.Child))
		}
		// runs on the chosen DSH model decide whether the default model may stand in
		return map[string]any{"allow": true, "track": !v.Fallback && chosen.Kind == "dsh"}, nil
	})

	// the result of a Claude Code call, or of a tracked call on the chosen DSH model
	h.Conn.OnNotify("guard.result", func(params json.RawMessage) {
		var p struct {
			IsError bool   `json:"isError"`
			Text    string `json:"text"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		if p.IsError {
			fallback.Failed()
		} else {
			fallback.Track(StartedID(p.Text))
		}
	})
	h.Conn.OnNotify("guard.end", func(params json.RawMessage) {
		var p struct {
			ID         string `json:"id"`
			StopReason string `json:"stopReason"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		if fallback.End(p.ID, p.StopReason) == "failed" {
			h.Info("guard: a run on the chosen subagent model failed; the default model may stand in for %v min", minutes)
		}
	})

	h.Info("subagent guard loaded (profile %s, fallback %v min)", running.Name, minutes)
	return map[string]any{"watch": []string{"subagent", "subagent_fork", "workflow"}, "claudeTool": ClaudeTool}, nil
}
