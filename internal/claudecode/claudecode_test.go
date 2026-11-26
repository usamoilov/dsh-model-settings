package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary plays Claude Code when FAKE_CLAUDE is set: it answers
// `initialize` with a model list and a prompt with one WebSearch result.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_CLAUDE"); mode != "" {
		fakeClaude(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeClaude(mode string) {
	if log := os.Getenv("FAKE_CLAUDE_LOG"); log != "" {
		env := map[string]string{}
		for _, kv := range os.Environ() {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
		data, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": env})
		_ = os.WriteFile(log, data, 0o600)
	}
	out := json.NewEncoder(os.Stdout)
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var msg map[string]any
		if json.Unmarshal(in.Bytes(), &msg) != nil {
			continue
		}
		switch msg["type"] {
		case "control_request":
			if mode == "hang" {
				time.Sleep(time.Hour)
			}
			_ = out.Encode(map[string]any{"type": "system", "subtype": "init"})
			models := []any{
				map[string]any{"value": "default", "displayName": "Default (recommended)", "resolvedModel": "claude-opus-5-5", "description": "Opus 5.5 · Best for everyday tasks"},
				map[string]any{"value": "opus", "displayName": "Opus 5.5", "resolvedModel": "claude-opus-5-5", "description": "For complex work"},
				map[string]any{"value": "haiku", "displayName": "Haiku 5.5", "resolvedModel": "claude-haiku-5-5", "description": "Fastest"},
				map[string]any{"value": "claude-opus-4-8", "displayName": "Opus 4.8"},
				map[string]any{"value": "sonnet[1m]", "displayName": "Sonnet (1M context)"},
				map[string]any{"value": "bad value; rm", "displayName": "Bad"},
				map[string]any{"displayName": "no value"},
				nil,
			}
			if mode == "empty" {
				models = []any{}
			}
			_ = out.Encode(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": msg["request_id"], "response": map[string]any{"models": models}}})
		case "user":
			_ = out.Encode(map[string]any{"type": "control_request", "request_id": "perm-1", "request": map[string]any{"subtype": "can_use_tool"}})
			_ = out.Encode(map[string]any{"type": "user", "message": map[string]any{"content": []any{
				map[string]any{"type": "tool_result", "content": []any{map[string]any{"text": `Links: [{"title":"Node.js","url":"https://nodejs.org/en"}]`}}},
			}}})
			_ = out.Encode(map[string]any{"type": "result", "subtype": "success", "result": `{"answer":"Node.js 26 is LTS.","sources":[{"url":"https://nodejs.org/en/blog","title":"Blog"}]}`})
		}
	}
}

func fakeBinary(t *testing.T, mode string) (string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := t.TempDir() + "/run.json"
	testEnv = []string{"FAKE_CLAUDE=" + mode, "FAKE_CLAUDE_LOG=" + log}
	t.Cleanup(func() { testEnv = nil })
	return exe, log
}

func readLog(t *testing.T, file string) (args []string, env map[string]string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		Args []string          `json:"args"`
		Env  map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return run.Args, run.Env
}

func TestModels(t *testing.T) {
	exe, log := fakeBinary(t, "ok")
	for _, name := range AuthEnv {
		t.Setenv(name, "")
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-test-oauth")
	t.Setenv("SOME_OTHER_TOKEN", "nope")
	var probes atomic.Int32
	real := ProcessProber(exe)
	cache := NewModelCache(func(ctx context.Context) (any, error) { probes.Add(1); return real(ctx) }, false)
	first := cache.List(context.Background(), false)
	if first.Source != "claude-code" || len(first.Models) != 4 || first.Recommended == nil || *first.Recommended.Model != "claude-opus-5-5" {
		t.Fatalf("%+v", first)
	}
	got := []string{}
	for _, m := range first.Models {
		got = append(got, m.ID)
	}
	if strings.Join(got, ",") != "opus,haiku,claude-opus-4-8,sonnet[1m]" || first.Models[2].Model != nil || first.Models[2].Description != "" {
		t.Fatalf("%v %+v", got, first.Models[2])
	}
	args, env := readLog(t, log)
	joined := strings.Join(args, " ")
	for _, flag := range []string{"--input-format stream-json", "--output-format stream-json", "--tools ", "--setting-sources=", "--strict-mcp-config", "--no-session-persistence"} {
		if !strings.Contains(joined+" ", flag) {
			t.Fatalf("missing %s in %v", flag, args)
		}
	}
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-test-oauth" || env["SOME_OTHER_TOKEN"] != "" || env["CLAUDE_CODE_ENTRYPOINT"] != "sdk-ts" {
		t.Fatalf("env: auth passed, nothing else: %v", env)
	}
	for k := range env {
		if strings.Contains(strings.ToUpper(k), "TOKEN") && k != "CLAUDE_CODE_OAUTH_TOKEN" && k != "ANTHROPIC_AUTH_TOKEN" {
			t.Fatalf("secret-like variable %s passed", k)
		}
	}
	cache.List(context.Background(), false)
	if probes.Load() != 1 {
		t.Fatal("not cached")
	}
	cache.List(context.Background(), true)
	if probes.Load() != 2 {
		t.Fatal("refresh probes again")
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); cache.List(context.Background(), true) }()
	}
	wg.Wait()
	if n := probes.Load(); n != 3 && n != 4 {
		t.Fatal("probes", n)
	}
}

func TestModelFallbacks(t *testing.T) {
	missing := NewModelCache(ProcessProber(""), true).List(context.Background(), false)
	if missing.Source != "fallback" || missing.Reason != "sdk-missing" || len(missing.Models) != 3 || missing.Recommended != nil {
		t.Fatalf("%+v", missing)
	}
	broken := NewModelCache(func(context.Context) (any, error) { return nil, errors.New("not logged in") }, false).List(context.Background(), false)
	if broken.Reason != "not logged in" {
		t.Fatal(broken.Reason)
	}
	exe, _ := fakeBinary(t, "empty")
	if r := NewModelCache(ProcessProber(exe), false).List(context.Background(), false); r.Reason != "empty" {
		t.Fatal(r.Reason)
	}
	exe, _ = fakeBinary(t, "hang")
	slow := NewModelCache(ProcessProber(exe), false)
	slow.Timeout = 300 * time.Millisecond
	started := time.Now()
	if r := slow.List(context.Background(), false); r.Source != "fallback" || time.Since(started) > 5*time.Second {
		t.Fatalf("a hanging probe times out: %+v", r)
	}
	if !ModelPattern.MatchString("claude-haiku-4-5-20251001") || ModelPattern.MatchString("-x") {
		t.Fatal("pattern")
	}
	if a := AuthFrom(func(k string) string {
		return map[string]string{"ANTHROPIC_API_KEY": "k", "ANTHROPIC_BASE_URL": "", "PATH": "/bin"}[k]
	}); len(a) != 1 || a["ANTHROPIC_API_KEY"] != "k" {
		t.Fatal(a)
	}
}

func TestSearchArgs(t *testing.T) {
	args := strings.Join(SearchArgs("haiku", 8), " ")
	for _, part := range []string{"--model haiku", "--allowedTools WebSearch", "--tools WebSearch", "--permission-mode dontAsk", "--max-turns 8"} {
		if !strings.Contains(args, part) {
			t.Fatal(args)
		}
	}
	if strings.Contains(strings.Join(SearchArgs("", 8), " "), "--model") {
		t.Fatal("auto has no --model")
	}
}

// RunSearch is exported for the search package test through the same fake.
func TestProcessConversation(t *testing.T) {
	exe, _ := fakeBinary(t, "ok")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := Start(ctx, exe, SearchArgs("haiku", 8), t.TempDir(), Env())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(time.Second)
	if _, err := p.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.SendPrompt("q"); err != nil {
		t.Fatal(err)
	}
	var types []string
	for {
		m, err := p.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		types = append(types, m.Type())
		if m.Type() == "result" {
			break
		}
	}
	if strings.Join(types, ",") != "user,result" {
		t.Fatal("control requests are answered, not passed on:", types)
	}
}
