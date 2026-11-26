package defaults

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/usamoilov/dsh-model-settings/internal/claudecode"
	"github.com/usamoilov/dsh-model-settings/internal/host"
	"github.com/usamoilov/dsh-model-settings/internal/i18n"
	"github.com/usamoilov/dsh-model-settings/internal/profile"
)

// Plugin is the host half of the settings page.
type Plugin struct {
	h        *host.Host
	config   Config
	running  string
	profiles []profile.Dir
	names    []string
	prefs    string
	claude   *claudecode.ModelCache
	http     *http.Client
	saveMu   chan struct{} // one save at a time
	restart  func(unit string) error
}

// ---- catalog (sessionController.modelCatalog(), through the shim)

type effort struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type reasoning struct {
	Efforts       []effort `json:"efforts"`
	DefaultEffort *string  `json:"defaultEffort"`
}

type catalogModel struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Reasoning *reasoning `json:"reasoning"`
}

type catalogGroup struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Deepseek bool           `json:"deepseek"`
	Models   []catalogModel `json:"models"`
}

type catalogFailure struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// Catalog is the live DSH model catalog, as the page shows it.
type Catalog struct {
	Groups   []catalogGroup   `json:"groups"`
	Failures []catalogFailure `json:"failures"`
}

func (p *Plugin) catalog(ctx context.Context, t i18n.T) Catalog {
	var raw Catalog
	if err := p.h.Call(ctx, "catalog", nil, &raw); err != nil {
		return Catalog{Groups: []catalogGroup{}, Failures: []catalogFailure{{ID: "—", Name: t("catalog.name", nil), Message: err.Error()}}}
	}
	out := Catalog{Groups: []catalogGroup{}, Failures: []catalogFailure{}}
	for _, g := range raw.Groups {
		group := catalogGroup{ID: g.ID, Name: g.Name, Deepseek: profile.IsDeepseekProvider(g.ID), Models: []catalogModel{}}
		for _, m := range g.Models {
			model := catalogModel{ID: m.ID, Name: m.Name}
			if model.Name == "" {
				model.Name = m.ID
			}
			if m.Reasoning != nil {
				r := &reasoning{Efforts: []effort{}, DefaultEffort: m.Reasoning.DefaultEffort}
				for _, e := range m.Reasoning.Efforts {
					if e.Name == "" {
						e.Name = e.ID
					}
					r.Efforts = append(r.Efforts, e)
				}
				model.Reasoning = r
			}
			group.Models = append(group.Models, model)
		}
		out.Groups = append(out.Groups, group)
	}
	for _, f := range raw.Failures {
		out.Failures = append(out.Failures, catalogFailure{ID: f.ID, Name: f.Name, Message: f.Message})
	}
	return out
}

// model finds a model of the catalog.
func (c Catalog) model(provider, model string) *catalogModel {
	for _, g := range c.Groups {
		if g.ID != provider {
			continue
		}
		for i := range g.Models {
			if g.Models[i].ID == model {
				return &g.Models[i]
			}
		}
	}
	return nil
}

// ---- profiles

// ProfileState is one profile as the page shows it.
type ProfileState struct {
	Name            string       `json:"name"`
	Path            string       `json:"path"`
	Running         bool         `json:"running"`
	SearchAvailable []string     `json:"searchAvailable"`
	Guard           bool         `json:"guard"`
	Error           string       `json:"error,omitempty"`
	ErrorKey        string       `json:"errorKey,omitempty"`
	ErrorParams     i18n.Params  `json:"errorParams,omitempty"`
	Info            profile.Info `json:"-"`
	failure         *i18n.Failure
}

// MarshalJSON flattens the profile info into the state (as the page expects).
func (s ProfileState) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"name": s.Name, "path": s.Path, "running": s.Running, "searchAvailable": s.SearchAvailable, "guard": s.Guard,
	}
	if s.Error != "" {
		out["error"], out["errorKey"], out["errorParams"] = s.Error, s.ErrorKey, s.ErrorParams
		out["defaultModel"] = s.Info.DefaultModel
		out["deepseek"] = map[string]any{"models": false, "search": false, "rows": map[string]any{}}
		out["providers"], out["search"], out["subagents"] = []string{}, map[string]any{}, map[string]any{}
	} else {
		out["defaultModel"], out["deepseek"], out["providers"], out["search"], out["subagents"] =
			s.Info.DefaultModel, s.Info.Deepseek, s.Info.Providers, s.Info.Search, s.Info.Subagents
	}
	return marshalOrdered(out, []string{"name", "path", "running", "searchAvailable", "guard", "error", "errorKey", "errorParams",
		"defaultModel", "deepseek", "providers", "search", "subagents"})
}

func (p *Plugin) readProfiles(ctx context.Context, t i18n.T) (map[string]*ProfileState, []string) {
	out := map[string]*ProfileState{}
	var order []string
	for _, dir := range p.profiles {
		file := dir.PatchFile()
		data, _ := os.ReadFile(file)
		st := &ProfileState{Name: dir.Name, Path: profile.Tildify(file), Running: dir.Name == p.running}
		info, err := profile.Read(string(data))
		if err != nil {
			var f *i18n.Failure
			if errors.As(err, &f) {
				st.failure, st.ErrorKey, st.ErrorParams = f, f.Key, f.Params
				st.Error = f.Message(t)
			} else {
				st.Error = err.Error()
			}
		} else {
			st.Info = info
		}
		deps, bundles := profilePackages(dir.Dir)
		st.SearchAvailable = []string{}
		for _, sp := range profile.SearchProviders {
			if sp.Plugin == "" || contains(deps, sp.Plugin) {
				st.SearchAvailable = append(st.SearchAvailable, sp.ID)
			}
		}
		st.Guard = HasGuard(string(data), bundles)
		out[dir.Name] = st
		order = append(order, dir.Name)
	}
	if st := out[p.running]; st != nil {
		// the live service is the truth for the running profile
		if live := p.h.CurrentSelection(ctx); live != nil {
			prov, model := live.Provider, live.Model
			st.Info.DefaultModel = &profile.DefaultModel{Provider: &prov, Model: &model, ReasoningEffort: live.ReasoningEffort}
		}
	}
	return out, order
}

type overlayState struct {
	Path        string `json:"path"`
	Label       string `json:"label"`
	ClaudeModel string `json:"claudeModel,omitempty"`
	Row         *bool  `json:"row,omitempty"`
	Error       string `json:"error,omitempty"`
}

func (p *Plugin) readOverlays() []overlayState {
	out := []overlayState{}
	for _, o := range p.config.Overlays {
		data, err := os.ReadFile(o.Path)
		if err != nil {
			continue
		}
		info, err := profile.Read(string(data))
		if err != nil {
			out = append(out, overlayState{Path: profile.Tildify(o.Path), Label: o.Label, Error: err.Error()})
			continue
		}
		row := info.Subagents.ClaudeCode.Row
		out = append(out, overlayState{Path: profile.Tildify(o.Path), Label: o.Label, ClaudeModel: info.Subagents.ClaudeCode.Model, Row: &row})
	}
	return out
}

type searxngState struct {
	URL   string `json:"url"`
	OK    bool   `json:"ok"`
	MS    *int64 `json:"ms,omitempty"`
	Error string `json:"error,omitempty"`
}

func (p *Plugin) searxngHealth(ctx context.Context) searxngState {
	started := time.Now()
	st := searxngState{URL: p.config.SearxngURL}
	base, err := url.Parse(p.config.SearxngURL)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.ResolveReference(&url.URL{Path: "/healthz"}).String(), nil)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	resp, err := p.http.Do(req)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	resp.Body.Close()
	ms := time.Since(started).Milliseconds()
	st.OK, st.MS = resp.StatusCode >= 200 && resp.StatusCode < 300, &ms
	return st
}

// needsRestart: the running profile's file and its live provider list disagree
// about DeepSeek — the file turns it off but a DeepSeek provider is still
// served, or the file turns the API-key provider on but `deepseek-official` is not served.
func needsRestart(st *ProfileState, cat Catalog) bool {
	if st == nil || st.Error != "" {
		return false
	}
	var live []string
	for _, g := range cat.Groups {
		live = append(live, g.ID)
	}
	for _, f := range cat.Failures {
		live = append(live, f.ID)
	}
	if st.Info.Deepseek.Models {
		for _, id := range live {
			if profile.IsDeepseekProvider(id) {
				return true
			}
		}
		return false
	}
	return !st.Info.Deepseek.Rows["llm-deepseek"].IsTrue() && !contains(live, "deepseek-official")
}

func (p *Plugin) readPrefsText() string {
	data, err := os.ReadFile(p.prefs)
	if err != nil {
		return ""
	}
	return string(data)
}

// State is everything the page shows.
type State struct {
	Running         string                    `json:"running"`
	Apply           map[string]bool           `json:"apply"`
	Catalog         Catalog                   `json:"catalog"`
	Profiles        orderedProfiles           `json:"profiles"`
	Overlays        []overlayState            `json:"overlays"`
	Searxng         searxngState              `json:"searxng"`
	RestartNeeded   bool                      `json:"restartNeeded"`
	CanRestart      bool                      `json:"canRestart"`
	DeepseekRows    any                       `json:"deepseekRows"`
	SearchProviders map[string]searchProvMeta `json:"searchProviders"`
	Claude          claudecode.Models         `json:"claude"`

	byName map[string]*ProfileState
}

type searchProvMeta struct {
	Label    string `json:"label"`
	Deepseek *bool  `json:"deepseek"`
}

func (p *Plugin) state(ctx context.Context, t i18n.T, refreshClaude bool) State {
	claudeCh := make(chan claudecode.Models, 1)
	go func() { claudeCh <- p.claude.List(ctx, refreshClaude) }()
	sxCh := make(chan searxngState, 1)
	go func() { sxCh <- p.searxngHealth(ctx) }()
	cat := p.catalog(ctx, t)
	profiles, order := p.readProfiles(ctx, t)
	st := State{
		Running:         p.running,
		Apply:           ReadApplyPrefs(p.readPrefsText(), p.names),
		Catalog:         cat,
		Profiles:        orderedProfiles{order: order, byName: profiles},
		Overlays:        p.readOverlays(),
		RestartNeeded:   needsRestart(profiles[p.running], cat),
		CanRestart:      p.config.WebUnit != nil && p.running == "web",
		DeepseekRows:    profile.DeepseekRows,
		SearchProviders: map[string]searchProvMeta{},
		byName:          profiles,
	}
	for _, sp := range profile.SearchProviders {
		st.SearchProviders[sp.ID] = searchProvMeta{Label: sp.Label, Deepseek: sp.Deepseek}
	}
	st.Searxng = <-sxCh
	st.Claude = <-claudeCh
	return st
}

// orderedProfiles keeps the managed profiles in config order in the JSON object.
type orderedProfiles struct {
	order  []string
	byName map[string]*ProfileState
}

func (o orderedProfiles) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	for _, name := range o.order {
		m[name] = o.byName[name]
	}
	return marshalOrdered(m, o.order)
}

func profileDirs(running profile.Dir, wanted []string) []profile.Dir {
	root := filepath.Dir(running.Dir)
	if len(wanted) == 0 {
		wanted = []string{running.Name, "headless"}
	}
	seen := map[string]bool{}
	var out []profile.Dir
	for _, name := range wanted {
		if seen[name] {
			continue
		}
		seen[name] = true
		dir := filepath.Join(root, name)
		if name == running.Name {
			dir = running.Dir
		}
		if _, err := os.Stat(dir); err == nil {
			out = append(out, profile.Dir{Name: name, Dir: dir})
		}
	}
	return out
}
