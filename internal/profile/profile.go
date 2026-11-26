// Package profile reads and edits DSH profile patches
// (`~/.dsh/profiles/<name>/cordis.patch.yml`) and overlays (extra `--patch`
// files a service runs with) that restate the Claude Code subagent row.
//
// Only these places are touched:
//   - `agent-default-model` → config (provider, model, reasoningEffort);
//   - DeepSeek rows (`llm-deepseek`, `llm-deepseek-account`, `web-search-deepseek`,
//     `web-deepseek-api`) → a `disabled: true` flag, added or removed;
//   - `web` → searchProvider (+ fetchProvider restated), `web-search-extra` → its config;
//   - `subagent-claude-code` → model (removed for "auto");
//   - `tool-subagent-claude-code` (the global `subagent_claude_code` tool) → a
//     `disabled: true` flag when the subagents run on a DSH model instead of Claude;
//   - the spawn `tool-subagent` (top level, or nested in a preset of the patch) → agentOptions.
//
// A profile row's `config` replaces the base row's whole config, so every edit
// keeps the keys already in the row and restates what the base row had.
package profile

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/usamoilov/dsh-model-settings/internal/i18n"
	"github.com/usamoilov/dsh-model-settings/internal/yamledit"
	"go.yaml.in/yaml/v3"
)

// Row ids and names.
const (
	DefaultModelID   = "agent-default-model"
	WebID            = "web"
	ExtraSearchID    = "web-search-extra"
	ClaudeSubagentID = "subagent-claude-code"
	ClaudeToolID     = "tool-subagent-claude-code"
	ToolSubagentID   = "tool-subagent"
	Auto             = "auto"
	Mark             = "dsh-model-defaults"
)

// DeepseekRows are the profile rows that bring DeepSeek in: model providers,
// and web search through the DeepSeek API.
var DeepseekRows = struct {
	Models []string `json:"models"`
	Search []string `json:"search"`
}{
	Models: []string{"llm-deepseek", "llm-deepseek-account"},
	Search: []string{"web-search-deepseek", "web-deepseek-api"},
}

// SearchProvider is a web search provider the settings page offers.
type SearchProvider struct {
	ID       string
	Label    string
	Deepseek *bool // nil: depends (auto)
	Plugin   string
}

var (
	yes = true
	no  = false
)

// SearchProviders in page order.
var SearchProviders = []SearchProvider{
	{ID: "deepseek-official", Label: "DeepSeek", Deepseek: &yes},
	{ID: "searxng", Label: "SearXNG", Deepseek: &no, Plugin: "dsh-web-search-extra"},
	{ID: "claude-code", Label: "Claude", Deepseek: &no, Plugin: "dsh-web-search-extra"},
	{ID: "auto", Label: "Auto", Plugin: "dsh-web-search-extra"},
}

// FindSearchProvider looks a provider up by id.
func FindSearchProvider(id string) (SearchProvider, bool) {
	for _, p := range SearchProviders {
		if p.ID == id {
			return p, true
		}
	}
	return SearchProvider{}, false
}

// Base composition of the `web` row (dsh-base): search through DeepSeek, fetch over plain HTTP.
const (
	BaseSearchProvider = "deepseek-official"
	BaseFetchProvider  = "http"
)

// toolSubagentBase is the base config of the top-level spawn subagent tool (dsh-base).
func toolSubagentBase() *yamledit.Map {
	return yamledit.NewMap("provider", "spawn", "toolName", "subagent")
}

var deepseekRe = regexp.MustCompile(`(?i)^deepseek`)

// IsDeepseekProvider: a provider route served by DeepSeek itself (API key and account).
func IsDeepseekProvider(id string) bool { return deepseekRe.MatchString(id) }

// DeepseekSearchEnabled: DeepSeek's own search rows stay enabled for this search choice.
func DeepseekSearchEnabled(provider string, deepseekModelsOff bool) bool {
	switch provider {
	case "deepseek-official":
		return true
	case Auto:
		return !deepseekModelsOff
	}
	return false
}

// ---- values

// Ref is a provider/model pair.
type Ref struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Choice is "auto" or a provider/model pair.
type Choice struct {
	Auto bool
	Ref  Ref
}

// AutoChoice is "auto".
var AutoChoice = Choice{Auto: true}

// RefChoice wraps a pair.
func RefChoice(provider, model string) Choice { return Choice{Ref: Ref{provider, model}} }

// MarshalJSON writes "auto" or {provider, model}.
func (c Choice) MarshalJSON() ([]byte, error) {
	if c.Auto {
		return json.Marshal(Auto)
	}
	return json.Marshal(c.Ref)
}

// Same compares two choices.
func (c Choice) Same(o Choice) bool { return c.Auto == o.Auto && (c.Auto || c.Ref == o.Ref) }

func (c Choice) yaml() any {
	if c.Auto {
		return Auto
	}
	return yamledit.NewMap("provider", c.Ref.Provider, "model", c.Ref.Model)
}

// Selection is a default model with an optional reasoning effort.
type Selection struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// DefaultModel is the `agent-default-model` row as read (null fields stay null).
type DefaultModel struct {
	Provider        *string `json:"provider"`
	Model           *string `json:"model"`
	ReasoningEffort *string `json:"reasoningEffort"`
}

// ProviderID is the provider or "".
func (d *DefaultModel) ProviderID() string {
	if d == nil || d.Provider == nil {
		return ""
	}
	return *d.Provider
}

// ModelID is the model or "".
func (d *DefaultModel) ModelID() string {
	if d == nil || d.Model == nil {
		return ""
	}
	return *d.Model
}

// Effort is the reasoning effort or "".
func (d *DefaultModel) Effort() string {
	if d == nil || d.ReasoningEffort == nil {
		return ""
	}
	return *d.ReasoningEffort
}

// Flag is a tri-state `disabled` reading: true, false, or "expression" (a `!!js` or other string).
type Flag struct{ v any }

// MarshalJSON writes true, false or "expression".
func (f Flag) MarshalJSON() ([]byte, error) { return json.Marshal(f.v) }

// IsTrue: the flag is a literal true.
func (f Flag) IsTrue() bool { b, ok := f.v.(bool); return ok && b }

// IsFalse: the flag is a literal false.
func (f Flag) IsFalse() bool { b, ok := f.v.(bool); return ok && !b }

// Info is what a profile patch says.
type Info struct {
	DefaultModel *DefaultModel `json:"defaultModel"`
	Deepseek     Deepseek      `json:"deepseek"`
	Providers    []string      `json:"providers"`
	Search       Search        `json:"search"`
	Subagents    Subagents     `json:"subagents"`
}

// Deepseek: models off, search off, and every DeepSeek row's flag.
type Deepseek struct {
	Models bool            `json:"models"`
	Search bool            `json:"search"`
	Rows   map[string]Flag `json:"rows"`
}

// Search is the web search setting.
type Search struct {
	Provider      string  `json:"provider"`
	FetchProvider string  `json:"fetchProvider"`
	ClaudeModel   *string `json:"claudeModel"`
	SummaryModel  *Choice `json:"summaryModel"`
	Summary       bool    `json:"summary"`
}

// ClaudeCode is the Claude Code subagent state.
type ClaudeCode struct {
	Model string `json:"model"`
	Row   bool   `json:"row"`
	Tool  Flag   `json:"tool"` // false: the subagent_claude_code tool is disabled
}

// Subagents is the subagent choice.
type Subagents struct {
	Route      string     `json:"route"` // "claude" or "dsh"
	ClaudeCode ClaudeCode `json:"claudeCode"`
	DSH        Choice     `json:"dsh"`
	DSHPlace   string     `json:"dshPlace"` // preset, top or base
}

// ---- reading

// Error turns an editor error into a translatable failure.
func Error(err error) error {
	var syn *yamledit.SyntaxError
	switch {
	case errors.As(err, &syn):
		return i18n.Fail(422, "err.yaml", i18n.Params{"error": syn.Msg})
	case errors.Is(err, yamledit.ErrNotList):
		return i18n.Fail(422, "err.notList", nil)
	}
	return err
}

func parse(text string) (*yamledit.Editor, error) {
	e, err := yamledit.New(text)
	if err != nil {
		return nil, Error(err)
	}
	return e, nil
}

func strPtr(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	if j, ok := v.(yamledit.JS); ok {
		s := string(j)
		return &s
	}
	s := strings.TrimSpace(strings.Trim(jsonString(v), `"`))
	return &s
}

func jsonString(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func flagOf(v any) Flag {
	switch x := v.(type) {
	case bool:
		return Flag{x}
	case string, yamledit.JS:
		return Flag{"expression"}
	}
	return Flag{false}
}

// patchRows are the top-level rows that patch an id (rows with `insert:` are not).
func patchRows(e *yamledit.Editor) []*yamledit.Map {
	var out []*yamledit.Map
	for _, row := range e.Rows() {
		if m, ok := yamledit.ToValue(row).(*yamledit.Map); ok && !m.Has("insert") {
			out = append(out, m)
		}
	}
	return out
}

// isSpawnSubagent: a `tool-subagent` map whose config has provider: spawn.
func isSpawnSubagent(n *yaml.Node) bool {
	if n.Kind != yaml.MappingNode {
		return false
	}
	id := yamledit.MapValue(n, "id")
	if id == nil || id.Kind != yaml.ScalarNode || id.Value != ToolSubagentID {
		return false
	}
	config := yamledit.MapValue(n, "config")
	p := yamledit.MapValue(config, "provider")
	return p != nil && p.Kind == yaml.ScalarNode && p.Value == "spawn"
}

// nestedSpawn: spawn subagent maps below the top-level rows (inside presets), in document order.
func nestedSpawn(e *yamledit.Editor) []*yaml.Node {
	var out []*yaml.Node
	for _, row := range e.Rows() {
		for _, c := range row.Content {
			yamledit.Walk(c, func(n *yaml.Node) {
				if isSpawnSubagent(n) {
					out = append(out, n)
				}
			})
		}
	}
	return out
}

// Read says what a profile patch holds; a later row for the same id wins.
func Read(text string) (Info, error) {
	e, err := parse(text)
	if err != nil {
		return Info{}, err
	}
	info := Info{Deepseek: Deepseek{Rows: map[string]Flag{}}, Providers: []string{}}
	var web, extra, claudeSubagent, topSubagent *yamledit.Map
	claudeTool := Flag{true}
	seen := map[string]bool{}
	allDeepseek := append(append([]string{}, DeepseekRows.Models...), DeepseekRows.Search...)
	for _, row := range patchRows(e) {
		id := row.String("id")
		config := row.Map("config")
		if id == DefaultModelID && config != nil {
			info.DefaultModel = &DefaultModel{
				Provider:        strPtr(config.Value("provider")),
				Model:           strPtr(config.Value("model")),
				ReasoningEffort: strPtr(config.Value("reasoningEffort")),
			}
		}
		if contains(allDeepseek, id) && row.Has("disabled") {
			info.Deepseek.Rows[id] = flagOf(row.Value("disabled"))
		}
		if id == "llm-pi-ai" && config != nil {
			if providers := config.Map("providers"); providers != nil {
				for _, k := range providers.Keys() {
					if !seen[k] {
						seen[k] = true
						info.Providers = append(info.Providers, k)
					}
				}
			}
		}
		switch {
		case id == WebID && config != nil:
			web = config
		case id == ExtraSearchID && config != nil:
			extra = config
		case id == ClaudeSubagentID && config != nil:
			claudeSubagent = config
		case id == ToolSubagentID && config != nil && config.String("provider") == "spawn":
			topSubagent = config
		}
		if id == ClaudeToolID && row.Has("disabled") {
			switch f := flagOf(row.Value("disabled")); {
			case f.IsTrue():
				claudeTool = Flag{false}
			case f.IsFalse():
				claudeTool = Flag{true}
			default:
				claudeTool = Flag{"expression"}
			}
		}
	}
	all := func(ids []string) bool {
		for _, id := range ids {
			if !info.Deepseek.Rows[id].IsTrue() {
				return false
			}
		}
		return true
	}
	info.Deepseek.Models = all(DeepseekRows.Models)
	info.Deepseek.Search = all(DeepseekRows.Search)

	// search: the answer model is `summaryModel`; absent = the DSH default model
	info.Search = Search{Provider: BaseSearchProvider, FetchProvider: BaseFetchProvider, Summary: true}
	if s := web.String("searchProvider"); s != "" {
		info.Search.Provider = s
	}
	if s := web.String("fetchProvider"); s != "" {
		info.Search.FetchProvider = s
	}
	if extra != nil {
		if s, ok := extra.Value("claudeModel").(string); ok {
			info.Search.ClaudeModel = &s
		}
		switch raw := extra.Value("summaryModel").(type) {
		case string:
			if raw == Auto {
				c := AutoChoice
				info.Search.SummaryModel = &c
			}
		case *yamledit.Map:
			if p := raw.String("provider"); p != "" {
				c := RefChoice(p, raw.String("model"))
				info.Search.SummaryModel = &c
			}
		}
		if b, ok := extra.Value("summary").(bool); ok && !b {
			info.Search.Summary = false
		}
	}

	// the preset copy (web profile) wins over a top-level row: it is what the active preset runs
	nested := nestedSpawn(e)
	subagentConfig := topSubagent
	if len(nested) > 0 {
		subagentConfig, _ = yamledit.ToValue(yamledit.MapValue(nested[len(nested)-1], "config")).(*yamledit.Map)
	}
	dsh := AutoChoice
	if opts := subagentConfig.Map("agentOptions"); opts != nil && yamledit.Truthy(opts.Value("provider")) && yamledit.Truthy(opts.Value("model")) {
		dsh = RefChoice(opts.String("provider"), opts.String("model"))
	}
	place := "base"
	if len(nested) > 0 {
		place = "preset"
	} else if topSubagent != nil {
		place = "top"
	}
	route := "claude"
	if claudeTool.IsFalse() {
		route = "dsh"
	}
	model := Auto
	if s := claudeSubagent.String("model"); s != "" {
		model = s
	}
	info.Subagents = Subagents{
		Route:      route,
		ClaudeCode: ClaudeCode{Model: model, Row: claudeSubagent != nil, Tool: claudeTool},
		DSH:        dsh,
		DSHPlace:   place,
	}
	return info, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---- changes

// SearchChange is a web search choice.
type SearchChange struct {
	Provider     string
	ClaudeModel  string  // "" = unchanged
	SummaryModel *Choice // nil = unchanged
	Summary      *bool   // nil = unchanged
}

// Toggle turns one row's `disabled: true` on or off.
type Toggle struct {
	ID  string
	Off bool
}

// Changes to apply to a patch; zero fields are left alone.
type Changes struct {
	DefaultModel        *Selection
	Disable             []Toggle
	Search              *SearchChange
	ClaudeSubagentModel string // "auto" removes `model`
	ClaudeTool          *bool  // false disables the global subagent_claude_code tool
	DSHSubagent         *Choice
	Stamp               string
	T                   i18n.T // translator for the comments on new rows (default English)
}

// SubagentChoice is one choice for all delegated work.
type SubagentChoice struct {
	Kind  string // "claude" or "dsh"
	Model string // claude: a model or "auto"
	Ref   Choice // dsh: "auto" or a model
}

// SubagentChanges turns one subagent choice into profile changes:
//
//	claude → Claude Code tool on with that model; DSH children follow the conversation;
//	dsh    → Claude Code tool off; DSH children on the ref ("auto" = the conversation's model).
func SubagentChanges(choice *SubagentChoice, ch *Changes) {
	if choice == nil {
		return
	}
	if choice.Kind == "claude" {
		on := true
		auto := AutoChoice
		ch.ClaudeTool, ch.ClaudeSubagentModel, ch.DSHSubagent = &on, choice.Model, &auto
		return
	}
	off := false
	ref := choice.Ref
	ch.ClaudeTool, ch.DSHSubagent = &off, &ref
}

// Apply changes a patch and returns the new text. When nothing changes in
// meaning the original text is returned byte for byte.
func Apply(text string, ch Changes) (string, error) {
	original, err := parse(text)
	if err != nil {
		return "", err
	}
	e, _ := parse(text)
	if err := apply(e, ch); err != nil {
		return "", Error(err)
	}
	if yamledit.Equal(e.Value(), original.Value()) {
		return text, nil
	}
	return e.Text(), nil
}

// rowIndexes are the top-level rows with this id (rows with `insert:` are not patches of an id).
func rowIndexes(e *yamledit.Editor, id string) []int {
	var out []int
	for i, row := range e.Rows() {
		if row.Kind != yaml.MappingNode || yamledit.MapValue(row, "insert") != nil {
			continue
		}
		if v := yamledit.MapValue(row, "id"); v != nil && v.Kind == yaml.ScalarNode && yamledit.ToValue(v) == id {
			out = append(out, i)
		}
	}
	return out
}

func rowValue(e *yamledit.Editor, i int) *yamledit.Map {
	m, _ := yamledit.ToValue(e.Rows()[i]).(*yamledit.Map)
	return m
}

// onlyIDName: nothing but `id` (and `name`) is left in the row.
func onlyIDName(m *yamledit.Map) bool {
	for _, k := range m.Keys() {
		if k != "id" && k != "name" {
			return false
		}
	}
	return true
}

// editRowConfig edits the config of the last top-level row with this id that
// has one: change(current) returns the new config, or nil to drop the config.
// A missing row is created from create (nil = leave it missing).
func editRowConfig(e *yamledit.Editor, id string, change func(current *yamledit.Map) *yamledit.Map, create *yamledit.Map, comment string) error {
	var withConfig []int
	for _, i := range rowIndexes(e, id) {
		if rowValue(e, i).Has("config") {
			withConfig = append(withConfig, i)
		}
	}
	if len(withConfig) > 0 {
		i := withConfig[len(withConfig)-1]
		current := rowValue(e, i).Map("config")
		next := change(current.Clone())
		if next == nil {
			if err := e.DeleteKey(yamledit.RowLoc(i), "config"); err != nil {
				return err
			}
			if onlyIDName(rowValue(e, i)) {
				return e.DeleteRow(i, Mark)
			}
			return nil
		}
		return e.SetKey(yamledit.RowLoc(i), "config", next)
	}
	if create == nil {
		return nil
	}
	return e.AppendRow(comment, yamledit.NewMap("id", id, "config", create))
}

// setDisabled adds or removes a `disabled: true` flag on a top-level row: a
// missing row is created with the comment; a row left with nothing but its id
// (and name) is dropped again. A flag written as an expression is never removed.
func setDisabled(e *yamledit.Editor, id string, off bool, comment string) error {
	indexes := rowIndexes(e, id)
	if off {
		for _, i := range indexes {
			if flagOf(rowValue(e, i).Value("disabled")).IsTrue() && rowValue(e, i).Has("disabled") {
				return nil
			}
		}
		if len(indexes) > 0 {
			return e.SetKey(yamledit.RowLoc(indexes[len(indexes)-1]), "disabled", true)
		}
		return e.AppendRow(comment, yamledit.NewMap("id", id, "disabled", true))
	}
	for j := len(indexes) - 1; j >= 0; j-- {
		i := indexes[j]
		if v, ok := rowValue(e, i).Value("disabled").(bool); !ok || !v {
			continue
		}
		if err := e.DeleteKey(yamledit.RowLoc(i), "disabled"); err != nil {
			return err
		}
		if onlyIDName(rowValue(e, i)) {
			if err := e.DeleteRow(i, Mark); err != nil {
				return err
			}
		}
	}
	return nil
}

func apply(e *yamledit.Editor, ch Changes) error {
	t := ch.T
	if t == nil {
		t = i18n.English
	}
	note := func(key string) string {
		s := Mark + ": " + t(key, nil)
		if ch.Stamp != "" {
			s += " (" + ch.Stamp + ")"
		}
		return s
	}

	if dm := ch.DefaultModel; dm != nil {
		config := yamledit.NewMap("provider", dm.Provider, "model", dm.Model)
		if dm.ReasoningEffort != "" {
			config.Set("reasoningEffort", dm.ReasoningEffort)
		}
		if err := editRowConfig(e, DefaultModelID, func(*yamledit.Map) *yamledit.Map { return config.Clone() }, config, note("comment.default")); err != nil {
			return err
		}
	}

	if s := ch.Search; s != nil && s.Provider != "" {
		err := editRowConfig(e, WebID, func(cur *yamledit.Map) *yamledit.Map {
			fetch := cur.Value("fetchProvider")
			cur.Set("searchProvider", s.Provider)
			if !yamledit.Truthy(fetch) {
				fetch = BaseFetchProvider
			}
			cur.Set("fetchProvider", fetch)
			return cur
		}, yamledit.NewMap("searchProvider", s.Provider, "fetchProvider", BaseFetchProvider), note("comment.search"))
		if err != nil {
			return err
		}
		extra := yamledit.NewMap()
		if s.ClaudeModel != "" {
			extra.Set("claudeModel", s.ClaudeModel)
		}
		if s.SummaryModel != nil {
			extra.Set("summaryModel", s.SummaryModel.yaml())
		}
		if s.Summary != nil {
			extra.Set("summary", *s.Summary)
		}
		if extra.Len() > 0 {
			err := editRowConfig(e, ExtraSearchID, func(cur *yamledit.Map) *yamledit.Map {
				for _, k := range extra.Keys() {
					cur.Set(k, extra.Value(k))
				}
				return cur
			}, extra, note("comment.searchExtra"))
			if err != nil {
				return err
			}
		}
	}

	if model := ch.ClaudeSubagentModel; model != "" {
		set := model != Auto
		var create *yamledit.Map
		if set {
			create = yamledit.NewMap("model", model)
		}
		err := editRowConfig(e, ClaudeSubagentID, func(cur *yamledit.Map) *yamledit.Map {
			if set {
				cur.Set("model", model)
			} else {
				cur.Delete("model")
			}
			if cur.Len() == 0 {
				return nil
			}
			return cur
		}, create, note("comment.claudeModel"))
		if err != nil {
			return err
		}
	}

	if sub := ch.DSHSubagent; sub != nil {
		var options *yamledit.Map
		if !sub.Auto {
			options = yamledit.NewMap("provider", sub.Ref.Provider, "model", sub.Ref.Model)
		}
		if count := len(nestedSpawn(e)); count > 0 {
			for k := 0; k < count; k++ {
				k := k
				loc := func(ed *yamledit.Editor) *yaml.Node {
					list := nestedSpawn(ed)
					if k >= len(list) {
						return nil
					}
					return yamledit.MapValue(list[k], "config")
				}
				var err error
				if options != nil {
					err = e.SetKey(loc, "agentOptions", options)
				} else {
					err = e.DeleteKey(loc, "agentOptions")
				}
				if err != nil {
					return err
				}
			}
		} else {
			var create *yamledit.Map
			if options != nil {
				create = toolSubagentBase()
				create.Set("agentOptions", options)
			}
			err := editRowConfig(e, ToolSubagentID, func(cur *yamledit.Map) *yamledit.Map {
				next := toolSubagentBase()
				for _, k := range cur.Keys() {
					next.Set(k, cur.Value(k))
				}
				if options != nil {
					next.Set("agentOptions", options)
				} else {
					next.Delete("agentOptions")
				}
				if yamledit.Equal(next, toolSubagentBase()) {
					return nil
				}
				return next
			}, create, note("comment.dshSubagent"))
			if err != nil {
				return err
			}
		}
	}

	for _, toggle := range ch.Disable {
		if err := setDisabled(e, toggle.ID, toggle.Off, note("comment.deepseekOff")); err != nil {
			return err
		}
	}
	if ch.ClaudeTool != nil {
		if err := setDisabled(e, ClaudeToolID, !*ch.ClaudeTool, note("comment.claudeToolOff")); err != nil {
			return err
		}
	}
	return nil
}
