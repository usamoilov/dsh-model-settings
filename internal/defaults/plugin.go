// Package defaults is the host half of dsh-model-defaults: one settings page
// for the models DSH uses — the default model of every profile (the web UI's
// and headless ones), the web search provider, the subagent model — and a
// switch that turns DeepSeek off everywhere.
//
// Routes (authenticated DSH `/api` routes, the web UI's session cookie):
//
//	GET  /api/model-defaults.state        catalog and everything the page shows
//	POST /api/model-defaults.save         apply changes (only the parts sent)
//	POST /api/model-defaults.restart      restart the web UI unit (option webUnit)
//	POST /api/model-defaults.test-search  one real query through the live `web` service
//
// POSTs require the `x-dsh-model-defaults: 1` header and a JSON body, so a
// cross-site form cannot trigger them. Messages follow the page's locale
// (`x-dsh-locale`: en, ru, zh).
//
// The running profile's default goes through DSH's own `agentDefaultModel`
// service, which also re-applies the profile file to the live tree; other
// profiles (and overlays) are edited on disk and apply to the next
// `dsh --profile <name>` run. The ticks "where to apply the default model" are
// kept in `.model-defaults.json` next to the running profile's patch.
package defaults

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/usamoilov/dsh-model-settings/internal/claudecode"
	"github.com/usamoilov/dsh-model-settings/internal/host"
	"github.com/usamoilov/dsh-model-settings/internal/i18n"
	"github.com/usamoilov/dsh-model-settings/internal/rpc"
)

// Routes of the page.
const (
	StateRoute      = "/api/model-defaults.state"
	SaveRoute       = "/api/model-defaults.save"
	RestartRoute    = "/api/model-defaults.restart"
	TestSearchRoute = "/api/model-defaults.test-search"
)

type route struct {
	Path    string   `json:"path"`
	Methods []string `json:"methods"`
}

// Start sets up the plugin and answers the shim's hello.
func Start(h *host.Host) (any, error) {
	running := h.Running()
	config := ReadConfig(h.Hello.Config)
	profiles := profileDirs(running, config.Profiles)
	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name
	}
	binary := claudecode.Find(running.Dir)
	p := &Plugin{
		h: h, config: config, running: running.Name, profiles: profiles, names: names,
		prefs:  filepath.Join(running.Dir, PrefsFile),
		claude: claudecode.NewModelCache(claudecode.ProcessProber(binary), binary == ""),
		http:   &http.Client{},
		saveMu: make(chan struct{}, 1),
		restart: func(unit string) error {
			// --no-block: systemd restarts the unit on its own; this process will be stopped
			return exec.Command("systemctl", "--user", "--no-block", "restart", unit).Start()
		},
	}
	h.Conn.Handle("http", p.serveHTTP)
	unit := "off"
	if config.WebUnit != nil {
		unit = *config.WebUnit
	}
	h.Info("host plugin loaded (profile %s; manages %s; overlays %d, restart %s)", running.Name, strings.Join(names, ", "), len(config.Overlays), unit)
	return map[string]any{"routes": []route{
		{Path: StateRoute, Methods: []string{"GET"}},
		{Path: SaveRoute, Methods: []string{"POST"}},
		{Path: TestSearchRoute, Methods: []string{"POST"}},
		{Path: RestartRoute, Methods: []string{"POST"}},
	}}, nil
}

// Request is one HTTP request the shim forwards.
type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
}

func (r Request) header(name string) string {
	for k, v := range r.Headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// Response goes back to the shim.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func jsonResponse(status int, v any) (Response, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return Response{}, err
	}
	return Response{Status: status, Headers: map[string]string{"content-type": "application/json", "cache-control": "no-store"}, Body: strings.TrimSuffix(buf.String(), "\n")}, nil
}

func (p *Plugin) serveHTTP(ctx context.Context, params json.RawMessage) (any, error) {
	var req Request
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, err
	}
	t := i18n.Translator(i18n.LocaleOf(req.header("x-dsh-locale")))
	u, _ := url.Parse(req.URL)
	path := ""
	if u != nil {
		path = u.Path
	}
	switch path {
	case StateRoute:
		refresh := u.Query().Get("refreshClaude") == "1"
		return jsonResponse(200, p.state(ctx, t, refresh))
	case SaveRoute:
		body, err := guardPost(req)
		if err != nil {
			return p.respondError(t, err, "err.save")
		}
		select {
		case p.saveMu <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		result, err := func() (*SaveResult, error) {
			defer func() { <-p.saveMu }()
			return p.save(context.WithoutCancel(ctx), body, t)
		}()
		if err != nil {
			return p.respondError(t, err, "err.save")
		}
		return jsonResponse(200, result)
	case TestSearchRoute:
		return p.testSearch(ctx, req, t)
	case RestartRoute:
		if _, err := guardPost(req); err != nil {
			return p.respondError(t, err, "err.restart")
		}
		if p.running != "web" {
			return p.respondError(t, i18n.Fail(409, "err.restartWebOnly", nil), "err.restart")
		}
		if p.config.WebUnit == nil {
			return p.respondError(t, i18n.Fail(409, "err.restartOff", nil), "err.restart")
		}
		unit := *p.config.WebUnit
		time.AfterFunc(500*time.Millisecond, func() {
			if err := p.restart(unit); err != nil {
				p.h.Warn("restart failed: %v", err)
			}
		})
		return jsonResponse(200, map[string]any{"ok": true})
	}
	return jsonResponse(404, map[string]any{"error": "not found"})
}

// guardPost: the page's header and a JSON body.
func guardPost(req Request) (map[string]any, error) {
	if req.header("x-dsh-model-defaults") != "1" {
		return nil, i18n.Fail(403, "err.header", nil)
	}
	if !strings.Contains(req.header("content-type"), "application/json") {
		return nil, i18n.Fail(415, "err.json", nil)
	}
	var body any
	if req.Body == nil || json.Unmarshal([]byte(*req.Body), &body) != nil {
		return nil, i18n.Fail(400, "err.badJson", nil)
	}
	m, _ := body.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func (p *Plugin) respondError(t i18n.T, err error, fallbackKey string) (Response, error) {
	var f *i18n.Failure
	if errors.As(err, &f) {
		return jsonResponse(f.Status, map[string]any{"error": f.Message(t)})
	}
	p.h.Warn("%s: %v", fallbackKey, err)
	return jsonResponse(500, map[string]any{"error": t(fallbackKey, nil) + ": " + err.Error()})
}

func (p *Plugin) testSearch(ctx context.Context, req Request, t i18n.T) (any, error) {
	body, err := guardPost(req)
	if err != nil {
		return p.respondError(t, err, "err.searchService")
	}
	query := "latest stable Node.js version"
	if q, ok := body["query"].(string); ok && strings.TrimSpace(q) != "" {
		query = strings.TrimSpace(q)
		if r := []rune(query); len(r) > 200 {
			query = string(r[:200])
		}
	}
	started := time.Now()
	var result struct {
		Content any `json:"content"`
		Sources []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"sources"`
	}
	err = p.h.Call(ctx, "webSearch", map[string]any{"query": query, "maxResults": 5, "timeoutMs": 150000}, &result)
	if err != nil {
		var re *rpc.Error
		if errors.As(err, &re) && re.Code == "NO_SERVICE" {
			return p.respondError(t, i18n.Fail(409, "err.searchService", nil), "err.searchService")
		}
		status := 502
		msg := err.Error()
		if errors.As(err, &re) {
			if re.Status >= 400 && re.Status <= 599 {
				status = re.Status
			}
			if re.Code != "" {
				msg = re.Code + ": " + re.Message
			}
		}
		return jsonResponse(status, map[string]any{"error": t("err.search", i18n.Params{"error": msg})})
	}
	var content any
	if s, ok := result.Content.(string); ok {
		if r := []rune(s); len(r) > 800 {
			s = string(r[:800])
		}
		content = s
	}
	sources := []map[string]any{}
	for i, s := range result.Sources {
		if i >= 5 {
			break
		}
		var title any
		if s.Title != "" {
			title = s.Title
		}
		sources = append(sources, map[string]any{"url": s.URL, "title": title})
	}
	return jsonResponse(200, map[string]any{"query": query, "ms": time.Since(started).Milliseconds(), "content": content, "sources": sources})
}

// marshalOrdered writes a JSON object with the keys in this order (missing keys are skipped).
func marshalOrdered(m map[string]any, order []string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for _, k := range order {
		v, ok := m[k]
		if !ok {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		key, _ := json.Marshal(k)
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
