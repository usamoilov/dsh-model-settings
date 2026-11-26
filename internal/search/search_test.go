package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	reply := "Here:\n```json\n{\"answer\":\"Node.js 25 is current.\",\"sources\":[" +
		`{"url":"https://nodejs.org/en/blog","title":"Node.js Blog","snippet":"  Node.js 25.1.0   released ","publishedAt":"2026-10-28"},` +
		`{"url":"https://nodejs.org/en/blog/","title":"dup"},{"url":"javascript:alert(1)"},{"url":"https://example.com/x","publishedAt":"nope"}]}` + "\n```"
	got := ParseAnswer(reply)
	want := Answer{Answer: "Node.js 25 is current.", Sources: []Source{
		{URL: "https://nodejs.org/en/blog", Title: "Node.js Blog", Snippet: "Node.js 25.1.0 released", PublishedAt: "2026-10-28"},
		{URL: "https://example.com/x"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if s := ParseAnswer("see https://a.example/page").Sources; len(s) != 1 || s[0].URL != "https://a.example/page" {
		t.Fatal(s)
	}
	if s := SourcesFromToolText(`Links: [{"title":"R \"25\"","url":"https://nodejs.org/r"}]`); len(s) != 1 || s[0] != (Source{URL: "https://nodejs.org/r", Title: `R "25"`}) {
		t.Fatal(s)
	}
	if s := NormalizeSources(nil); s == nil || len(s) != 0 {
		t.Fatal("nil sources")
	}
	if !strings.Contains(BuildClaudePrompt("a \"b\"\nc", 5), `Query: "a \"b\"\nc"`) {
		t.Fatal("claude prompt")
	}
	prompt := BuildSummaryPrompt("версия node", []Source{{URL: "https://a", Title: "A", Snippet: "s1"}, {URL: "https://b"}})
	for _, part := range []string{"[1] A\nhttps://a\ns1", "[2] https://b\nhttps://b", `Query: "версия node"`} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("summary prompt lacks %q:\n%s", part, prompt)
		}
	}
}

type fake struct {
	name   string
	calls  *[]string
	result Result
	err    error
}

func (f fake) Search(ctx context.Context, query string, max any) (Result, error) {
	*f.calls = append(*f.calls, f.name)
	return f.result, f.err
}

func TestAuto(t *testing.T) {
	var calls []string
	providers := map[string]Searcher{
		"deepseek-official": fake{"ds", &calls, Result{}, errors.New("quota")},
		"searxng":           fake{"sx", &calls, Result{Sources: []Source{{URL: "https://q"}}}, nil},
		"claude-code":       fake{"claude", &calls, Result{Sources: []Source{{URL: "https://c"}}}, nil},
	}
	lookup := func(id string) (Searcher, error) {
		if p, ok := providers[id]; ok {
			return p, nil
		}
		return nil, errNotInstalled
	}
	r, err := SearchInOrder(context.Background(), DefaultAutoOrder, lookup, "x", nil)
	if err != nil || r.Sources[0].URL != "https://q" || strings.Join(calls, ",") != "ds,sx" {
		t.Fatal(r, err, calls)
	}
	_, err = SearchInOrder(context.Background(), []string{"missing", "deepseek-official"}, lookup, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "missing: not installed; deepseek-official: quota") {
		t.Fatal(err)
	}
	empty := fake{"e", &calls, Result{Sources: []Source{}}, nil}
	_, err = SearchInOrder(context.Background(), []string{"a", "b", "auto"}, func(id string) (Searcher, error) {
		if id == "a" {
			return empty, nil
		}
		return nil, errUnavailable
	}, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "a: empty answer; b: unavailable") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = SearchInOrder(ctx, []string{"x"}, func(string) (Searcher, error) { return fake{"x", &calls, Result{}, errors.New("aborted")}, nil }, "x", nil)
	if err == nil || err.Error() != "aborted" {
		t.Fatal("abort stops:", err)
	}
}

type fakeLLM struct {
	streamed []map[string]any
	err      error
}

func (l *fakeLLM) Efforts(ctx context.Context, r Route) ([]string, error) {
	return []string{"off", "high"}, nil
}
func (l *fakeLLM) Complete(ctx context.Context, r Route, effort string, max int, prompt string) (string, error) {
	l.streamed = append(l.streamed, map[string]any{"provider": r.Provider, "effort": effort, "max": max})
	if l.err != nil {
		return "", l.err
	}
	return " Актуальная LTS — Node.js 26 [1]. ", nil
}

func TestSearxng(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") == "" {
			http.Error(w, "bad", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{
			map[string]any{"url": "https://nodejs.org/en", "title": "Node.js", "content": "Node.js 26 LTS", "publishedDate": "2026-10-28T00:00:00"},
			map[string]any{"url": "https://nodejs.org/en/", "title": "dup"},
			map[string]any{"url": "https://github.com/nodejs/node/releases", "title": "Releases"},
		}})
	}))
	defer srv.Close()
	llm := &fakeLLM{}
	route := &Route{"local-llm", "q"}
	sx := &Searxng{URL: func() string { return srv.URL }, SummaryModel: func(context.Context) *Route { return route }, Summary: func() bool { return true }, SummaryTimeout: time.Second, LLM: llm}
	r, err := sx.Search(context.Background(), "node lts", 5.0)
	if err != nil || r.Content != "Актуальная LTS — Node.js 26 [1]." || len(r.Sources) != 2 || r.Sources[1].URL != "https://github.com/nodejs/node/releases" {
		t.Fatal(r, err)
	}
	if r.Sources[0].PublishedAt != "2026-10-28T00:00:00" {
		t.Fatal("date", r.Sources[0])
	}
	if llm.streamed[0]["effort"] != "off" || llm.streamed[0]["provider"] != "local-llm" || llm.streamed[0]["max"] != 500 {
		t.Fatal(llm.streamed)
	}
	// a failing model still returns the results
	var warned []string
	broken := &Searxng{URL: sx.URL, SummaryModel: sx.SummaryModel, Summary: sx.Summary, SummaryTimeout: time.Second, LLM: &fakeLLM{err: errors.New("busy")},
		Log: func(f string, a ...any) { warned = append(warned, f) }}
	r, err = broken.Search(context.Background(), "x", nil)
	if err != nil || r.Content != "" || len(r.Sources) != 2 || len(warned) != 1 {
		t.Fatal(r, err, warned)
	}
	// summary off: no model call
	quiet := &fakeLLM{}
	off := &Searxng{URL: sx.URL, SummaryModel: sx.SummaryModel, Summary: func() bool { return false }, SummaryTimeout: time.Second, LLM: quiet}
	if r, err = off.Search(context.Background(), "x", nil); err != nil || r.Content != "" || len(quiet.streamed) != 0 {
		t.Fatal(r, err)
	}
	// SearXNG down
	_, err = (&Searxng{URL: func() string { return "http://127.0.0.1:1" }, SummaryModel: sx.SummaryModel, Summary: sx.Summary, LLM: llm}).Search(context.Background(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "SearXNG is unreachable at http://127.0.0.1:1") {
		t.Fatal(err)
	}
}

func TestLimiterGate(t *testing.T) {
	c := &Claude{Gate: make(chan struct{}, 2)}
	var active, peak atomic.Int32
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			c.Gate <- struct{}{}
			n := active.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			active.Add(-1)
			<-c.Gate
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if peak.Load() != 2 {
		t.Fatal("peak", peak.Load())
	}
}

func TestConfig(t *testing.T) {
	d := NormalizeConfig(json.RawMessage(`{}`))
	if d.SummaryModel != nil || !d.Summary || d.ClaudeModel != "haiku" || d.Timeout != 55*time.Second || d.SummaryTimeout != 90*time.Second || d.Concurrency != 2 || d.SearxngURL != DefaultSearxng {
		t.Fatalf("%+v", d)
	}
	c := NormalizeConfig(json.RawMessage(`{"summaryModel":{"provider":"p","model":"m"},"summary":false,"autoOrder":["searxng","claude-code"]}`))
	if c.SummaryModel == nil || *c.SummaryModel != (Route{"p", "m"}) || c.Summary || strings.Join(c.AutoOrder, ",") != "searxng,claude-code" {
		t.Fatalf("%+v", c)
	}
	if NormalizeConfig(json.RawMessage(`{"summaryModel":{"provider":"p"}}`)).SummaryModel != nil {
		t.Fatal("incomplete ref falls back to auto")
	}
}
