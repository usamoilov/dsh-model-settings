// Package search is dsh-web-search-extra: extra providers for DSH's
// `web_search` tool (`ctx.web`):
//
//	searxng       self-hosted SearXNG results + an optional short answer by a DSH model
//	claude-code   Claude through the Claude Code subscription (WebSearch tool only)
//	auto          the first provider that answers, in order DeepSeek → SearXNG → Claude
//
// Providers only register; the `web` row's `searchProvider` decides which one
// `web_search` uses (the settings page of dsh-model-defaults writes it).
//
// Config (row `web-search-extra`):
//
//	searxngURL      default http://127.0.0.1:8888
//	summary         false → SearXNG results only (faster; the conversation model reads them)
//	summaryModel    { provider, model } that writes the answer, or 'auto' = the DSH default model (default)
//	claudeModel     a Claude Code model id or alias, or 'auto' = Claude Code's own default
//	autoOrder       provider ids tried by `auto`
//	timeoutMs / summaryTimeoutMs / concurrency
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/usamoilov/dsh-model-settings/internal/claudecode"
	"github.com/usamoilov/dsh-model-settings/internal/host"
	"github.com/usamoilov/dsh-model-settings/internal/rpc"
)

// Provider ids.
const (
	SearxngID = "searxng"
	ClaudeID  = "claude-code"
	AutoID    = "auto"
)

// DefaultClaudeModel is used when the row sets no claudeModel.
const DefaultClaudeModel = "haiku"

// DefaultAutoOrder is the order of `auto`.
var DefaultAutoOrder = []string{"deepseek-official", SearxngID, ClaudeID}

// Config of the row, with defaults.
type Config struct {
	SearxngURL     string
	Summary        bool
	SummaryModel   *Route // nil = auto (the DSH default model)
	ClaudeModel    string
	AutoOrder      []string
	Timeout        time.Duration
	SummaryTimeout time.Duration
	Concurrency    int
}

func positive(v any, fallback float64) float64 {
	switch x := v.(type) {
	case float64:
		if x > 0 {
			return x
		}
	case string:
		var f float64
		if _, err := fmt.Sscan(x, &f); err == nil && f > 0 {
			return f
		}
	}
	return fallback
}

// NormalizeConfig reads the plugin config.
func NormalizeConfig(raw json.RawMessage) Config {
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	out := Config{
		SearxngURL:     DefaultSearxng,
		Summary:        cfg["summary"] != false,
		ClaudeModel:    DefaultClaudeModel,
		AutoOrder:      DefaultAutoOrder,
		Timeout:        time.Duration(positive(cfg["timeoutMs"], 55000)) * time.Millisecond,
		SummaryTimeout: time.Duration(positive(cfg["summaryTimeoutMs"], 90000)) * time.Millisecond,
		Concurrency:    int(positive(cfg["concurrency"], 2)),
	}
	if s, ok := cfg["searxngURL"].(string); ok && s != "" {
		out.SearxngURL = s
	}
	if m, ok := cfg["summaryModel"].(map[string]any); ok {
		p, _ := m["provider"].(string)
		model, _ := m["model"].(string)
		if p != "" && model != "" {
			out.SummaryModel = &Route{Provider: p, Model: model}
		}
	}
	if s, ok := cfg["claudeModel"].(string); ok && s != "" {
		out.ClaudeModel = s
	}
	if list, ok := cfg["autoOrder"].([]any); ok && len(list) > 0 {
		var order []string
		for _, id := range list {
			if s, ok := id.(string); ok {
				order = append(order, s)
			}
		}
		if len(order) > 0 {
			out.AutoOrder = order
		}
	}
	return out
}

// Searcher is one provider.
type Searcher interface {
	Search(ctx context.Context, query string, maxResults any) (Result, error)
}

// Lookup finds a provider for `auto`: errNotInstalled / errUnavailable when it cannot run.
type Lookup func(id string) (Searcher, error)

var (
	errNotInstalled = errors.New("not installed")
	errUnavailable  = errors.New("unavailable")
)

// SearchInOrder tries providers in order; the first non-empty result wins.
func SearchInOrder(ctx context.Context, order []string, lookup Lookup, query string, maxResults any) (Result, error) {
	var errs []string
	for _, id := range order {
		if id == AutoID {
			continue
		}
		p, err := lookup(id)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		result, err := p.Search(ctx, query, maxResults)
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			errs = append(errs, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if len(result.Sources) > 0 || result.Content != "" {
			return result, nil
		}
		errs = append(errs, id+": empty answer")
	}
	return Result{}, fmt.Errorf("auto: no search provider answered — %s", strings.Join(errs, "; "))
}

// dshProvider is a provider of DSH's own registry (DeepSeek), reached through the shim.
type dshProvider struct {
	h  *host.Host
	id string
}

func (d dshProvider) Search(ctx context.Context, query string, maxResults any) (Result, error) {
	var r Result
	err := d.h.Call(ctx, "providerSearch", map[string]any{"id": d.id, "request": map[string]any{"query": query, "maxResults": maxResults}}, &r)
	var re *rpc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case "NOT_INSTALLED":
			return Result{}, errNotInstalled
		case "UNAVAILABLE":
			return Result{}, errUnavailable
		}
	}
	return r, err
}

// dshLLM is ctx.llm, through the shim.
type dshLLM struct{ h *host.Host }

func (l dshLLM) Efforts(ctx context.Context, route Route) ([]string, error) {
	var out struct {
		Efforts []string `json:"efforts"`
	}
	err := l.h.Call(ctx, "llmModelInfo", route, &out)
	return out.Efforts, err
}

func (l dshLLM) Complete(ctx context.Context, route Route, effort string, maxTokens int, prompt string) (string, error) {
	params := map[string]any{"provider": route.Provider, "model": route.Model, "maxTokens": maxTokens, "prompt": prompt}
	if effort != "" {
		params["reasoningEffort"] = effort
	}
	var out struct {
		Text string `json:"text"`
	}
	err := l.h.Call(ctx, "llmComplete", params, &out)
	return out.Text, err
}

type provider struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Ready     bool   `json:"ready"`
}

// Start sets up the plugin and answers the shim's hello.
func Start(h *host.Host) (any, error) {
	cfg := NormalizeConfig(h.Hello.Config)
	binary := claudecode.Find(h.Running().Dir)
	sx := &Searxng{
		Client:         &http.Client{},
		URL:            func() string { return cfg.SearxngURL },
		Summary:        func() bool { return cfg.Summary },
		SummaryTimeout: cfg.SummaryTimeout,
		LLM:            dshLLM{h},
		Log:            h.Warn,
		SummaryModel: func(ctx context.Context) *Route {
			if cfg.SummaryModel != nil {
				return cfg.SummaryModel
			}
			if s := h.CurrentSelection(ctx); s != nil {
				return &Route{Provider: s.Provider, Model: s.Model}
			}
			return nil
		},
	}
	claude := &Claude{Binary: binary, Model: func() string { return cfg.ClaudeModel }, Timeout: cfg.Timeout, Gate: make(chan struct{}, max(cfg.Concurrency, 1))}
	claudeAvailable := func() bool { return binary != "" && claudecode.HasCredentials() }
	own := map[string]Searcher{SearxngID: sx, ClaudeID: claude}
	lookup := func(id string) (Searcher, error) {
		switch id {
		case SearxngID:
			return sx, nil
		case ClaudeID:
			if !claudeAvailable() {
				return nil, errUnavailable
			}
			return claude, nil
		}
		return dshProvider{h, id}, nil
	}
	autoAvailable := func() bool {
		for _, id := range cfg.AutoOrder {
			if id == AutoID {
				continue
			}
			if _, mine := own[id]; !mine || id == SearxngID || claudeAvailable() {
				return true
			}
		}
		return false
	}
	availability := func() map[string]bool {
		return map[string]bool{SearxngID: true, ClaudeID: claudeAvailable(), AutoID: autoAvailable()}
	}

	h.Conn.Handle("search", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct {
			ID      string `json:"id"`
			Request struct {
				Query      string `json:"query"`
				MaxResults any    `json:"maxResults"`
			} `json:"request"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		switch p.ID {
		case SearxngID:
			return sx.Search(ctx, p.Request.Query, p.Request.MaxResults)
		case ClaudeID:
			return claude.Search(ctx, p.Request.Query, p.Request.MaxResults)
		case AutoID:
			return SearchInOrder(ctx, cfg.AutoOrder, lookup, p.Request.Query, p.Request.MaxResults)
		}
		return nil, fmt.Errorf("unknown provider %q", p.ID)
	})
	h.Conn.Handle("ready", func(ctx context.Context, params json.RawMessage) (any, error) {
		return claudecode.HasCredentials(), nil
	})

	// availability must not touch the network; a Claude login may appear or go away later
	var mu sync.Mutex
	last := availability()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-h.Conn.Context().Done():
				return
			case <-ticker.C:
				now := availability()
				mu.Lock()
				changed := now[ClaudeID] != last[ClaudeID] || now[AutoID] != last[AutoID]
				last = now
				mu.Unlock()
				if changed {
					_ = h.Conn.Notify("providers", map[string]any{"available": now})
				}
			}
		}
	}()

	answer := "off"
	if cfg.Summary {
		answer = "default model"
		if cfg.SummaryModel != nil {
			answer = cfg.SummaryModel.Provider + "/" + cfg.SummaryModel.Model
		}
	}
	found := "missing"
	if binary != "" {
		found = "found"
	}
	h.Info("providers: searxng, claude-code, auto (searxng answer: %s; claude: %s, claude code %s; auto: %s)",
		answer, cfg.ClaudeModel, found, strings.Join(cfg.AutoOrder, " → "))
	return map[string]any{"providers": []provider{
		{ID: SearxngID, Available: last[SearxngID]},
		{ID: ClaudeID, Available: last[ClaudeID], Ready: true},
		{ID: AutoID, Available: last[AutoID]},
	}}, nil
}
