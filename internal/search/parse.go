package search

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Source is one search result.
type Source struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

// Result is what a provider returns to `web_search`.
type Result struct {
	Content   string   `json:"content,omitempty"`
	Sources   []Source `json:"sources"`
	Truncated bool     `json:"truncated"`
}

var (
	httpRe   = regexp.MustCompile(`(?i)^https?://[^\s"'<>]+$`)
	spaceRe  = regexp.MustCompile(`\s+`)
	urlInRe  = regexp.MustCompile(`https?://[^\s)"'<>\]]+`)
	hashRe   = regexp.MustCompile(`#.*$`)
	pairRe   = regexp.MustCompile(`"title"\s*:\s*"((?:[^"\\]|\\.)*)"\s*,\s*"url"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	fenceRe1 = regexp.MustCompile("(?i)^```(?:json)?\\s*")
	fenceRe2 = regexp.MustCompile("\\s*```$")
)

func clip(v any, max int) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	text := strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
	if r := []rune(text); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return text
}

var dateLayouts = []string{
	time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02T15:04",
	"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006-01", "2006",
	time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC850, time.ANSIC,
	"Jan 2, 2006", "January 2, 2006", "2 Jan 2006", "2 January 2006", "Mon, 2 Jan 2006 15:04:05 MST",
}

// validDate: the value reads as a date (as JavaScript's Date.parse would accept it).
func validDate(s string) bool {
	for _, layout := range dateLayouts {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// NormalizeSources keeps valid http(s) sources, drops duplicates and trims fields.
func NormalizeSources(list any) []Source {
	items, _ := list.([]any)
	seen := map[string]bool{}
	out := []Source{}
	for _, item := range items {
		var url string
		m, _ := item.(map[string]any)
		switch x := item.(type) {
		case string:
			url = x
		case map[string]any:
			url, _ = x["url"].(string)
		}
		if !httpRe.MatchString(strings.TrimSpace(url)) {
			continue
		}
		url = strings.TrimSpace(url)
		key := strings.TrimSuffix(hashRe.ReplaceAllString(url, ""), "/")
		if seen[key] {
			continue
		}
		seen[key] = true
		s := Source{URL: url, Title: clip(m["title"], 200), Snippet: clip(m["snippet"], 600)}
		if p := clip(m["publishedAt"], 40); p != "" && validDate(p) {
			s.PublishedAt = p
		}
		out = append(out, s)
	}
	return out
}

// Answer is Claude's reply: the short answer and its sources.
type Answer struct {
	Answer  string
	Sources []Source
}

// ParseAnswer reads Claude's JSON reply; code fences and text around the object are tolerated.
func ParseAnswer(text string) Answer {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return Answer{Sources: []Source{}}
	}
	unfenced := fenceRe2.ReplaceAllString(fenceRe1.ReplaceAllString(raw, ""), "")
	start, end := strings.Index(unfenced, "{"), strings.LastIndex(unfenced, "}")
	if start >= 0 && end > start {
		var value map[string]any
		if json.Unmarshal([]byte(unfenced[start:end+1]), &value) == nil {
			return Answer{Answer: clip(value["answer"], 4000), Sources: NormalizeSources(value["sources"])}
		}
	}
	var urls []any
	for _, u := range urlInRe.FindAllString(raw, -1) {
		urls = append(urls, map[string]any{"url": u})
	}
	return Answer{Answer: clip(raw, 4000), Sources: NormalizeSources(urls)}
}

// SourcesFromToolText is the fallback: `{"title":"…","url":"…"}` pairs from WebSearch tool output.
func SourcesFromToolText(text string) []Source {
	var out []any
	for _, m := range pairRe.FindAllStringSubmatch(text, -1) {
		title, url := m[1], m[2]
		var t, u string
		if json.Unmarshal([]byte(`"`+title+`"`), &t) == nil {
			title = t
		}
		if json.Unmarshal([]byte(`"`+url+`"`), &u) == nil {
			url = u
		}
		out = append(out, map[string]any{"url": url, "title": title})
	}
	return NormalizeSources(out)
}
