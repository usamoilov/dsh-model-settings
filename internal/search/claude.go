package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/usamoilov/dsh-model-settings/internal/claudecode"
)

// ClaudeAuto lets Claude Code pick its own default model.
const ClaudeAuto = "auto"

// BuildClaudePrompt asks for one to three WebSearch calls and a JSON reply.
func BuildClaudePrompt(query string, maxResults int) string {
	q, _ := json.Marshal(query)
	return strings.Join([]string{
		"You are the web search backend of an AI agent. Research the query below with the WebSearch tool",
		"(one to three searches), then reply with ONLY a JSON object — no prose before or after, no code fences:",
		`{"answer": "<2-6 sentences answering the query with facts from the sources, in the language of the query>",`,
		` "sources": [{"url": "https://...", "title": "...", "snippet": "<1-2 sentences from that page relevant to the query>", "publishedAt": "<ISO date if the result shows one>"}]}`,
		fmt.Sprintf("List at most %d sources, most relevant first, using only URLs that WebSearch returned.", maxResults),
		`If nothing relevant is found, return {"answer": "", "sources": []}.`,
		"",
		"Query: " + jsonText(q),
	}, "\n")
}

// Claude is the `claude-code` provider: each query is one short isolated Claude
// Code run with the WebSearch tool only, in an empty temporary folder.
type Claude struct {
	Binary  string
	Model   func() string
	Timeout time.Duration
	Gate    chan struct{} // at most cap(Gate) runs at once
}

// Search runs one query.
func (c *Claude) Search(ctx context.Context, query string, rawMax any) (Result, error) {
	if c.Binary == "" {
		return Result{}, claudecode.ErrMissing
	}
	limit := maxResults(rawMax)
	select {
	case c.Gate <- struct{}{}:
		defer func() { <-c.Gate }()
	case <-ctx.Done():
		return Result{}, ErrCancelled
	}
	runCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cwd, err := os.MkdirTemp("", "dsh-claude-search-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(cwd)

	model := c.Model()
	if model == ClaudeAuto {
		model = ""
	}
	var toolTexts []string
	resultText, failure := "", ""
	err = func() error {
		p, err := claudecode.Start(runCtx, c.Binary, claudecode.SearchArgs(model, 8), cwd, claudecode.Env())
		if err != nil {
			return err
		}
		defer p.Stop(2 * time.Second)
		if _, err := p.Initialize(runCtx); err != nil {
			return err
		}
		if err := p.SendPrompt(BuildClaudePrompt(query, limit)); err != nil {
			return err
		}
		for {
			m, err := p.Next(runCtx)
			if err != nil {
				return err
			}
			switch m.Type() {
			case "user":
				msg, _ := m["message"].(map[string]any)
				parts, _ := msg["content"].([]any)
				for _, part := range parts {
					pm, _ := part.(map[string]any)
					if pm["type"] != "tool_result" {
						continue
					}
					switch content := pm["content"].(type) {
					case string:
						toolTexts = append(toolTexts, content)
					case []any:
						var texts []string
						for _, item := range content {
							im, _ := item.(map[string]any)
							s, _ := im["text"].(string)
							texts = append(texts, s)
						}
						toolTexts = append(toolTexts, strings.Join(texts, "\n"))
					}
				}
			case "result":
				if m.Str("subtype") == "success" {
					resultText = fmt.Sprint(m["result"])
					if m["result"] == nil {
						resultText = ""
					}
				} else {
					failure = m.Str("subtype")
				}
				p.CloseInput()
				return nil
			}
		}
	}()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return Result{}, ErrCancelled
		case runCtx.Err() != nil:
			return Result{}, fmt.Errorf("Claude did not answer within %d s", int(c.Timeout.Seconds()))
		case errors.Is(err, claudecode.ErrMissing):
			return Result{}, err
		}
		return Result{}, fmt.Errorf("Claude: %v", err)
	}
	parsed := ParseAnswer(resultText)
	sources := parsed.Sources
	if len(sources) == 0 {
		sources = SourcesFromToolText(strings.Join(toolTexts, "\n"))
	}
	if len(sources) == 0 && parsed.Answer == "" {
		if failure != "" {
			return Result{}, fmt.Errorf("Claude returned no results (%s)", failure)
		}
		return Result{}, errors.New("Claude returned no results")
	}
	return Result{Content: parsed.Answer, Sources: sources[:min(len(sources), limit)]}, nil
}
