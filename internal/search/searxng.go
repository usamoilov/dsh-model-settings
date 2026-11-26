package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultSearxng is where SearXNG is expected.
const DefaultSearxng = "http://127.0.0.1:8888"

// ErrCancelled: the caller gave up on the search.
var ErrCancelled = errors.New("search cancelled")

// SearxngSearch asks SearXNG's JSON API; returns normalized sources.
func SearxngSearch(ctx context.Context, client *http.Client, baseURL, query string, timeout time.Duration, limit int) ([]Source, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("SearXNG is unreachable at %s: %v", baseURL, err)
	}
	u := base.ResolveReference(&url.URL{Path: "/search"})
	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("safesearch", "0")
	u.RawQuery = q.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	var body struct {
		Results []map[string]any `json:"results"`
	}
	err = func() error {
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("SearXNG HTTP %d", resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(&body)
	}()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, ErrCancelled
		case reqCtx.Err() != nil:
			return nil, fmt.Errorf("SearXNG did not answer within %d s", int(timeout.Seconds()))
		}
		return nil, fmt.Errorf("SearXNG is unreachable at %s: %v", baseURL, err)
	}
	items := make([]any, 0, len(body.Results))
	for _, r := range body.Results {
		item := map[string]any{"url": r["url"], "title": r["title"], "snippet": r["content"]}
		if p, ok := r["publishedDate"].(string); ok && p != "" {
			item["publishedAt"] = p
		}
		items = append(items, item)
	}
	sources := NormalizeSources(items)
	if len(sources) > limit {
		sources = sources[:limit]
	}
	return sources, nil
}

// BuildSummaryPrompt numbers the sources for the model that writes the answer.
func BuildSummaryPrompt(query string, sources []Source) string {
	parts := make([]string, 0, len(sources))
	for i, s := range sources {
		title := s.Title
		if title == "" {
			title = s.URL
		}
		entry := fmt.Sprintf("[%d] %s\n%s", i+1, title, s.URL)
		if s.Snippet != "" {
			entry += "\n" + s.Snippet
		}
		parts = append(parts, entry)
	}
	q, _ := json.Marshal(query)
	return strings.Join([]string{
		"You are a web search assistant. Answer the query below in 2-5 sentences, in the language of the query,",
		"using only these search results. Cite sources by their numbers in square brackets, e.g. [1].",
		"If the results do not answer the query, say so in one sentence.",
		"",
		"Query: " + jsonText(q),
		"",
		"Search results:",
		strings.Join(parts, "\n\n"),
	}, "\n")
}

// jsonText undoes Go's HTML escaping, so the prompt shows the query as JSON.stringify would.
func jsonText(b []byte) string {
	s := string(b)
	return strings.NewReplacer(`<`, "<", `>`, ">", `&`, "&").Replace(s)
}

// Route is a provider/model pair.
type Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// LLM writes the short answer through DSH's ctx.llm (the same route, key and
// accounting as conversations).
type LLM interface {
	Efforts(ctx context.Context, route Route) ([]string, error)
	Complete(ctx context.Context, route Route, effort string, maxTokens int, prompt string) (string, error)
}

// Searxng is the `searxng` provider.
type Searxng struct {
	Client         *http.Client
	URL            func() string
	SummaryModel   func(ctx context.Context) *Route
	Summary        func() bool
	SummaryTimeout time.Duration
	LLM            LLM
	Log            func(format string, args ...any)
}

func maxResults(raw any) int {
	n := 0
	switch x := raw.(type) {
	case float64:
		n = int(x)
	case string:
		n, _ = strconv.Atoi(x)
	}
	if n == 0 {
		n = 8
	}
	return min(max(n, 1), 20)
}

// Search runs one query; the answer is best effort: a busy, slow or broken
// model never costs the search its results.
func (s *Searxng) Search(ctx context.Context, query string, rawMax any) (Result, error) {
	limit := maxResults(rawMax)
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	sources, err := SearxngSearch(ctx, client, s.URL(), query, 15*time.Second, max(limit, 8))
	if err != nil {
		return Result{}, err
	}
	if len(sources) == 0 {
		return Result{}, errors.New("SearXNG found nothing")
	}
	var content string
	if s.Summary() {
		if route := s.SummaryModel(ctx); route != nil && route.Provider != "" && route.Model != "" {
			text, err := s.summarize(ctx, *route, BuildSummaryPrompt(query, sources[:min(len(sources), 8)]))
			if err != nil {
				if ctx.Err() != nil {
					return Result{}, ErrCancelled
				}
				if s.Log != nil {
					s.Log("answer skipped (%s/%s): %v", route.Provider, route.Model, err)
				}
			}
			content = text
		}
	}
	return Result{Content: content, Sources: sources[:min(len(sources), limit)]}, nil
}

func (s *Searxng) summarize(ctx context.Context, route Route, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.SummaryTimeout)
	defer cancel()
	effort := ""
	if efforts, err := s.LLM.Efforts(ctx, route); err == nil {
		for _, e := range efforts {
			if e == "off" {
				effort = "off"
			}
		}
	}
	text, err := s.LLM.Complete(ctx, route, effort, 500, prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}
