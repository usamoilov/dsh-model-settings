package defaults

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/usamoilov/dsh-model-settings/internal/claudecode"
	"github.com/usamoilov/dsh-model-settings/internal/fsutil"
	"github.com/usamoilov/dsh-model-settings/internal/i18n"
	"github.com/usamoilov/dsh-model-settings/internal/profile"
)

const auto = profile.Auto

var effortRe = regexp.MustCompile(`^[\w-]{1,32}$`)

func label(t i18n.T, provider, model, effort string) string {
	if provider == "" {
		return "—"
	}
	s := provider + " / " + model
	if effort != "" {
		s += " (" + t("effort", i18n.Params{"effort": effort}) + ")"
	}
	return s
}

func choiceLabel(t i18n.T, c profile.Choice) string {
	if c.Auto {
		return t("auto", nil)
	}
	return label(t, c.Ref.Provider, c.Ref.Model, "")
}

func parseSelection(raw any) (*profile.Selection, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	provider, _ := m["provider"].(string)
	model, _ := m["model"].(string)
	if !ok || provider == "" || model == "" {
		return nil, i18n.Fail(400, "err.noModel", nil)
	}
	sel := &profile.Selection{Provider: provider, Model: model}
	switch e := m["reasoningEffort"].(type) {
	case nil:
	case string:
		if !effortRe.MatchString(e) {
			return nil, i18n.Fail(400, "err.effort", nil)
		}
		sel.ReasoningEffort = e
	default:
		return nil, i18n.Fail(400, "err.effort", nil)
	}
	return sel, nil
}

// parseRef reads 'auto' or { provider, model }.
func parseRef(raw any, whatKey string) (profile.Choice, error) {
	if raw == auto {
		return profile.AutoChoice, nil
	}
	if m, ok := raw.(map[string]any); ok {
		p, _ := m["provider"].(string)
		model, _ := m["model"].(string)
		if p != "" && model != "" {
			return profile.RefChoice(p, model), nil
		}
	}
	return profile.Choice{}, &i18n.Failure{Status: 400, Key: "err.noRef", WhatKey: whatKey}
}

func parseClaudeModel(raw any) (string, error) {
	s, ok := raw.(string)
	if !ok || (s != auto && !claudecode.ModelPattern.MatchString(s)) {
		return "", i18n.Fail(400, "err.claudeModel", nil)
	}
	return s, nil
}

type searchChoice struct {
	Provider     string
	ClaudeModel  string
	SummaryModel *profile.Choice
	Summary      *bool
}

func parseSearch(raw any) (*searchChoice, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	provider, _ := m["provider"].(string)
	if _, known := profile.FindSearchProvider(provider); !ok || !known {
		return nil, i18n.Fail(400, "err.searchProvider", nil)
	}
	out := &searchChoice{Provider: provider}
	if v, ok := m["claudeModel"]; ok && v != nil {
		model, err := parseClaudeModel(v)
		if err != nil {
			return nil, err
		}
		out.ClaudeModel = model
	}
	if v, ok := m["summaryModel"]; ok && v != nil {
		ref, err := parseRef(v, "what.answer")
		if err != nil {
			return nil, err
		}
		out.SummaryModel = &ref
	}
	if v, ok := m["summary"]; ok && v != nil {
		b := v == true
		out.Summary = &b
	}
	return out, nil
}

// parseSubagents reads one choice for every delegated task: { kind: 'claude', model } or { kind: 'dsh', ref }.
func parseSubagents(raw any) (*profile.SubagentChoice, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, i18n.Fail(400, "err.subagents", nil)
	}
	switch m["kind"] {
	case "claude":
		model, err := parseClaudeModel(m["model"])
		if err != nil {
			return nil, err
		}
		return &profile.SubagentChoice{Kind: "claude", Model: model}, nil
	case "dsh":
		ref, err := parseRef(m["ref"], "what.subagent")
		if err != nil {
			return nil, err
		}
		return &profile.SubagentChoice{Kind: "dsh", Ref: ref}, nil
	}
	return nil, &i18n.Failure{Status: 400, Key: "err.noRef", WhatKey: "what.subagent"}
}

func (p *Plugin) claudeName(ctx context.Context, model string) string {
	if model == "" || model == auto {
		return ""
	}
	for _, m := range p.claude.List(ctx, false).Models {
		if m.ID == model || (m.Model != nil && *m.Model == model) {
			if m.Name != "" {
				return m.Name
			}
			break
		}
	}
	return model
}

func stamp() string { return time.Now().Format("2006-01-02 15:04") }

// SaveResult is the answer of a save.
type SaveResult struct {
	State State    `json:"state"`
	Notes []string `json:"notes"`
}

func (p *Plugin) save(ctx context.Context, body map[string]any, t i18n.T) (*SaveResult, error) {
	selection, err := parseSelection(body["selection"])
	if err != nil {
		return nil, err
	}
	search, err := parseSearch(body["search"])
	if err != nil {
		return nil, err
	}
	subagents, err := parseSubagents(body["subagents"])
	if err != nil {
		return nil, err
	}
	applyMap, _ := body["apply"].(map[string]any)
	var targets []profile.Dir
	for _, dir := range p.profiles {
		if applyMap[dir.Name] == true {
			targets = append(targets, dir)
		}
	}
	isTarget := func(name string) bool {
		for _, d := range targets {
			if d.Name == name {
				return true
			}
		}
		return false
	}
	remember := ParseApplyPrefs(body["remember"], p.names)
	deepseekMap, _ := body["deepseek"].(map[string]any)
	deepseekOff := deepseekMap["models"] == true
	before, _ := p.readProfiles(ctx, t)
	cat := p.catalog(ctx, t)

	// ---- validate everything before writing anything
	if selection != nil && len(targets) == 0 {
		return nil, i18n.Fail(400, "err.applyWhere", nil)
	}
	if selection != nil {
		if deepseekOff && profile.IsDeepseekProvider(selection.Provider) {
			return nil, i18n.Fail(400, "err.deepseekBoth", nil)
		}
		model := cat.model(selection.Provider, selection.Model)
		if isTarget(p.running) && model == nil {
			return nil, i18n.Fail(400, "err.notInWebCatalog", i18n.Params{"model": selection.Provider + " / " + selection.Model})
		}
		if selection.ReasoningEffort != "" && model != nil && model.Reasoning != nil {
			found := false
			for _, e := range model.Reasoning.Efforts {
				found = found || e.ID == selection.ReasoningEffort
			}
			if !found {
				return nil, i18n.Fail(400, "err.effortUnsupported", i18n.Params{"effort": selection.ReasoningEffort})
			}
		}
	}
	if search != nil {
		meta, _ := profile.FindSearchProvider(search.Provider)
		for _, dir := range p.profiles {
			if st := before[dir.Name]; st == nil || !contains(st.SearchAvailable, search.Provider) {
				return nil, i18n.Fail(400, "err.searchNotInstalled", i18n.Params{"label": meta.Label, "profile": dir.Name, "plugin": meta.Plugin})
			}
		}
		if sm := search.SummaryModel; sm != nil && !sm.Auto {
			if cat.model(sm.Ref.Provider, sm.Ref.Model) == nil {
				return nil, i18n.Fail(400, "err.notInCatalog", i18n.Params{"model": choiceLabel(t, *sm)})
			}
			if deepseekOff && profile.IsDeepseekProvider(sm.Ref.Provider) {
				return nil, i18n.Fail(400, "err.answerDeepseek", nil)
			}
		}
	}
	var subRef *profile.Ref
	if subagents != nil && subagents.Kind == "dsh" && !subagents.Ref.Auto {
		subRef = &subagents.Ref.Ref
		if cat.model(subRef.Provider, subRef.Model) == nil {
			return nil, i18n.Fail(400, "err.notInCatalog", i18n.Params{"model": label(t, subRef.Provider, subRef.Model, "")})
		}
		if deepseekOff && profile.IsDeepseekProvider(subRef.Provider) {
			return nil, i18n.Fail(400, "err.subDeepseek", nil)
		}
	}
	for _, dir := range p.profiles {
		willSet := selection != nil && isTarget(dir.Name)
		current := before[dir.Name]
		if current != nil && current.Error != "" {
			msg := current.Error
			if current.failure != nil {
				msg = current.failure.Message(t)
			}
			return nil, i18n.Fail(422, "err.profile", i18n.Params{"profile": dir.Name, "error": msg})
		}
		if current == nil {
			continue
		}
		dm := current.Info.DefaultModel
		if deepseekOff && !willSet && profile.IsDeepseekProvider(dm.ProviderID()) {
			return nil, i18n.Fail(400, "err.defaultDeepseek", i18n.Params{"profile": dir.Name, "model": label(t, dm.ProviderID(), dm.ModelID(), dm.Effort())})
		}
		sub := current.Info.Subagents
		if deepseekOff && subagents == nil && sub.Route == "dsh" && !sub.DSH.Auto && profile.IsDeepseekProvider(sub.DSH.Ref.Provider) {
			return nil, i18n.Fail(400, "err.profileSubDeepseek", i18n.Params{"profile": dir.Name})
		}
	}

	// ---- write
	searchProvider := profile.BaseSearchProvider
	if search != nil {
		searchProvider = search.Provider
	} else if st := before[p.running]; st != nil && st.Error == "" && st.Info.Search.Provider != "" {
		searchProvider = st.Info.Search.Provider
	}
	searchRowsOff := !profile.DeepseekSearchEnabled(searchProvider, deepseekOff)
	var disable []profile.Toggle
	for _, id := range profile.DeepseekRows.Models {
		disable = append(disable, profile.Toggle{ID: id, Off: deepseekOff})
	}
	for _, id := range profile.DeepseekRows.Search {
		disable = append(disable, profile.Toggle{ID: id, Off: searchRowsOff})
	}
	when := stamp()
	notes := []string{}
	changes := profile.Changes{Disable: disable, Stamp: when, T: t}
	if search != nil {
		changes.Search = &profile.SearchChange{Provider: search.Provider, ClaudeModel: search.ClaudeModel, SummaryModel: search.SummaryModel, Summary: search.Summary}
	}
	profile.SubagentChanges(subagents, &changes)
	dsh := p.h.DSH()

	for _, dir := range p.profiles {
		willSet := selection != nil && isTarget(dir.Name)
		prior := before[dir.Name]
		if dir.Name == p.running {
			if _, err := profile.EditProfile(ctx, dir, dsh, func(text string) (string, error) { return profile.Apply(text, changes) }, nil, nil, t); err != nil {
				return nil, err
			}
			// DSH's own writer: persists the default and re-applies the profile file to the live tree
			next := map[string]any{}
			if willSet {
				next["provider"], next["model"] = selection.Provider, selection.Model
				if selection.ReasoningEffort != "" {
					next["reasoningEffort"] = selection.ReasoningEffort
				}
			} else {
				live := p.h.CurrentSelection(ctx)
				if live == nil {
					return nil, fmt.Errorf("the default model service is not available")
				}
				next["provider"], next["model"] = live.Provider, live.Model
				if live.ReasoningEffort != nil && *live.ReasoningEffort != "" {
					next["reasoningEffort"] = *live.ReasoningEffort
				}
			}
			if err := p.h.Call(ctx, "saveSelection", next, nil); err != nil {
				return nil, err
			}
			if willSet {
				notes = append(notes, t("note.default", i18n.Params{"profile": dir.Name, "model": label(t, selection.Provider, selection.Model, selection.ReasoningEffort)}))
			}
		} else {
			verify := func(dump string) string {
				dump = trimCR(dump)
				if search != nil {
					if meta, _ := profile.FindSearchProvider(search.Provider); meta.Plugin != "" && !extraRow.MatchString(dump) {
						return t("verify.noExtra", nil)
					}
				}
				if willSet && !providerDeclared(dump, selection.Provider) {
					return t("verify.noProvider", i18n.Params{"provider": selection.Provider})
				}
				if subRef != nil && !providerDeclared(dump, subRef.Provider) {
					return t("verify.noProviderSub", i18n.Params{"provider": subRef.Provider})
				}
				if search != nil && search.SummaryModel != nil && !search.SummaryModel.Auto && !providerDeclared(dump, search.SummaryModel.Ref.Provider) {
					return t("verify.noProviderAnswer", i18n.Params{"provider": search.SummaryModel.Ref.Provider})
				}
				return ""
			}
			ch := changes
			if willSet {
				ch.DefaultModel = selection
			}
			result, err := profile.EditProfile(ctx, dir, dsh, func(text string) (string, error) { return profile.Apply(text, ch) }, verify, nil, t)
			if err != nil {
				return nil, err
			}
			if result.Changed && willSet {
				notes = append(notes, t("note.defaultNext", i18n.Params{"profile": dir.Name, "model": label(t, selection.Provider, selection.Model, selection.ReasoningEffort)}))
			}
		}
		if prior != nil && prior.Error == "" && prior.Info.Deepseek.Models != deepseekOff {
			key := "note.deepseekOn"
			if deepseekOff {
				key = "note.deepseekOff"
			}
			notes = append(notes, t(key, i18n.Params{"profile": dir.Name}))
		}
	}

	var updatedOverlays []string
	if subagents != nil && subagents.Kind == "claude" {
		// overlays restate the provider row (model next to e.g. bypassPermissions); the tool row stays in the profile
		for _, o := range p.config.Overlays {
			if _, err := os.Stat(o.Path); err != nil {
				continue
			}
			o := o
			var patches []string
			for _, patch := range o.Patches {
				if _, err := os.Stat(patch); err == nil {
					patches = append(patches, patch)
				}
			}
			patches = append(patches, o.Path)
			target := profile.Target{
				File: o.Path, Lock: o.Path, Label: o.Label,
				Compose: func(ctx context.Context) profile.ComposeResult {
					if dsh == nil {
						return profile.ComposeResult{Error: "DSH installation not found"}
					}
					return dsh.CheckComposes(ctx, o.Profile, patches)
				},
			}
			overlayChanges := profile.Changes{ClaudeSubagentModel: subagents.Model, Stamp: when, T: t}
			if _, err := profile.EditPatchFile(ctx, target, func(text string) (string, error) { return profile.Apply(text, overlayChanges) }, nil, t); err != nil {
				return nil, err
			}
			updatedOverlays = append(updatedOverlays, o.Label)
		}
	}

	if search != nil {
		meta, _ := profile.FindSearchProvider(search.Provider)
		extra := ""
		running := before[p.running]
		switch search.Provider {
		case "claude-code":
			model := search.ClaudeModel
			if model == "" && running != nil && running.Info.Search.ClaudeModel != nil {
				model = *running.Info.Search.ClaudeModel
			}
			name := p.claudeName(ctx, model)
			if name == "" {
				name = t("auto", nil)
			}
			extra = t("note.search.claude", i18n.Params{"model": name})
		case "searxng":
			ref := profile.AutoChoice
			if search.SummaryModel != nil {
				ref = *search.SummaryModel
			} else if running != nil && running.Info.Search.SummaryModel != nil {
				ref = *running.Info.Search.SummaryModel
			}
			if search.Summary != nil && !*search.Summary {
				extra = t("note.search.noAnswer", nil)
			} else {
				extra = t("note.search.answer", i18n.Params{"model": choiceLabel(t, ref)})
			}
		case auto:
			extra = t("note.search.auto", nil)
		}
		name := meta.Label
		if search.Provider == auto {
			name = t("auto", nil)
		}
		deepseek := ""
		if searchRowsOff {
			deepseek = t("note.search.deepseekOff", nil)
		}
		notes = append(notes, t("note.search", i18n.Params{"label": name, "extra": extra, "deepseek": deepseek}))
	}
	if subagents != nil && subagents.Kind == "claude" {
		lbl := t("sub.claudeAuto", nil)
		if name := p.claudeName(ctx, subagents.Model); name != "" {
			lbl = t("sub.claude", i18n.Params{"model": name})
		}
		overlays := ""
		if len(updatedOverlays) > 0 {
			overlays = t("note.overlays", i18n.Params{"list": strings.Join(updatedOverlays, ", ")})
		}
		notes = append(notes, t("note.subClaude", i18n.Params{"label": lbl, "overlays": overlays}))
	} else if subagents != nil {
		lbl := t("sub.auto", nil)
		if !subagents.Ref.Auto {
			lbl = choiceLabel(t, subagents.Ref)
		}
		notes = append(notes, t("note.subDsh", i18n.Params{"label": lbl}))
	}
	if subagents != nil {
		notes = append(notes, t("note.subNext", nil))
	}

	if remember != nil {
		prior := ReadApplyPrefs(p.readPrefsText(), p.names)
		changed := false
		for _, name := range p.names {
			changed = changed || prior[name] != remember[name]
		}
		if changed {
			if err := p.writePrefs(remember); err != nil {
				return nil, err
			}
			var on, own []string
			for _, name := range p.names {
				if remember[name] {
					on = append(on, name)
				} else {
					own = append(own, name)
				}
			}
			if len(on) > 0 {
				notes = append(notes, t("note.applyTo", i18n.Params{"list": strings.Join(on, ", ")}))
			}
			if len(own) > 0 {
				notes = append(notes, t("note.applyOwn", i18n.Params{"list": strings.Join(own, ", ")}))
			}
		}
	}

	after := p.state(ctx, t, false)
	if after.RestartNeeded {
		notes = append(notes, t("note.restart", nil))
	}
	if len(notes) == 0 {
		notes = append(notes, t("note.none", nil))
	}
	return &SaveResult{State: after, Notes: notes}, nil
}

// writePrefs saves the ticks; profiles not managed right now keep what the file says about them.
func (p *Plugin) writePrefs(apply map[string]bool) error {
	data := map[string]any{}
	if err := json.Unmarshal([]byte(p.readPrefsText()), &data); err != nil || data == nil {
		data = map[string]any{}
	}
	merged := map[string]any{}
	if before, ok := data["apply"].(map[string]any); ok {
		for k, v := range before {
			merged[k] = v
		}
	}
	for k, v := range apply {
		merged[k] = v
	}
	data["apply"] = merged
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(p.prefs, append(out, '\n'), 0o600)
}
