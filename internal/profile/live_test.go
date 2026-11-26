//go:build live

// Live checks on a machine with DSH: go test -tags live ./internal/profile
// Real profiles are only READ; edits are made in memory (and, with LIVE_OUT set,
// written to that folder for comparison).
package profile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LiveChanges are the change sets the live test applies to every real profile.
func LiveChanges() []Changes {
	on, off := true, false
	auto := AutoChoice
	local := RefChoice("live-provider", "live-model")
	var subDSH, subClaude Changes
	SubagentChanges(&SubagentChoice{Kind: "dsh", Ref: local}, &subDSH)
	SubagentChanges(&SubagentChoice{Kind: "claude", Model: "haiku"}, &subClaude)
	return []Changes{
		{Disable: allToggles(false)},
		{Disable: allToggles(true)},
		{Search: &SearchChange{Provider: "searxng", SummaryModel: &auto, Summary: &off}},
		{Search: &SearchChange{Provider: "auto", ClaudeModel: "sonnet", SummaryModel: &local, Summary: &on}},
		subDSH,
		subClaude,
		{DefaultModel: &Selection{Provider: "live-provider", Model: "live-model", ReasoningEffort: "high"}},
		{DefaultModel: &Selection{Provider: "live-provider", Model: "live-model"}, Disable: allToggles(true), ClaudeSubagentModel: "auto"},
	}
}

func allToggles(off bool) []Toggle {
	var out []Toggle
	for _, id := range append(append([]string{}, DeepseekRows.Models...), DeepseekRows.Search...) {
		out = append(out, Toggle{ID: id, Off: off})
	}
	return out
}

func TestLiveProfiles(t *testing.T) {
	home := DSHHome()
	out := os.Getenv("LIVE_OUT")
	var existing []string
	for _, name := range []string{"web", "headless"} {
		if _, err := os.Stat(filepath.Join(home, "profiles", name, "cordis.patch.yml")); err == nil {
			existing = append(existing, name)
		}
	}
	if len(existing) == 0 {
		t.Skip("no DSH profiles here")
	}
	for _, name := range existing {
		data, err := os.ReadFile(filepath.Join(home, "profiles", name, "cordis.patch.yml"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		info := mustRead(t, text)
		if info.DefaultModel == nil || info.DefaultModel.ProviderID() == "" {
			t.Fatalf("%s: default model not read", name)
		}
		// flipping DeepSeek and restoring each row's flag gives back the same file, byte for byte
		var flip, back []Toggle
		for _, id := range append(append([]string{}, DeepseekRows.Models...), DeepseekRows.Search...) {
			flip = append(flip, Toggle{ID: id, Off: !info.Deepseek.Models})
			back = append(back, Toggle{ID: id, Off: info.Deepseek.Rows[id].IsTrue()})
		}
		flipped := mustApply(t, text, Changes{Disable: flip})
		if mustRead(t, flipped).Deepseek.Models == info.Deepseek.Models {
			t.Fatalf("%s: DeepSeek did not flip", name)
		}
		restored := mustApply(t, flipped, Changes{Disable: back})
		if jsonOf(t, mustRead(t, restored)) != jsonOf(t, info) {
			t.Fatalf("%s: round trip changed the meaning", name)
		}
		// hand-written comments survive every change set; only the edited lines differ
		for i, ch := range LiveChanges() {
			next := mustApply(t, text, ch)
			for _, line := range strings.Split(text, "\n") {
				l := strings.TrimSpace(line)
				if strings.HasPrefix(l, "#") && !strings.Contains(l, "dsh-model-defaults:") && !strings.Contains(next, l) {
					t.Fatalf("%s/%d: comment lost: %s", name, i, l)
				}
			}
			if out != "" {
				if err := os.WriteFile(filepath.Join(out, fmt.Sprintf("%s-%d.yml", name, i)), []byte(next), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		sub := info.Subagents
		t.Logf("%s: default %s / %s; DeepSeek models off=%v, search off=%v; providers %v; subagents %s",
			name, info.DefaultModel.ProviderID(), info.DefaultModel.ModelID(), info.Deepseek.Models, info.Deepseek.Search, info.Providers, sub.Route)
	}

	// the real profile composes (read-only check)
	dsh, err := FindDSH("", "")
	if err != nil {
		t.Fatal(err)
	}
	last := existing[len(existing)-1]
	if r := dsh.CheckComposes(context.Background(), last, nil); !r.OK {
		t.Fatalf("%s does not compose: %s", last, r.Error)
	}
}

func TestLiveEditFile(t *testing.T) {
	dir := t.TempDir()
	p := Dir{Name: "temp", Dir: dir}
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o600)
	_ = os.WriteFile(p.PatchFile(), []byte("# keep me\n- id: agent-default-model\n  config:\n    provider: a\n    model: b\n"), 0o600)
	ok := func(context.Context, string) ComposeResult { return ComposeResult{OK: true} }
	bad := func(context.Context, string) ComposeResult { return ComposeResult{Error: "boom"} }
	r, err := EditProfile(context.Background(), p, nil, func(s string) (string, error) {
		return Apply(s, Changes{DefaultModel: &Selection{Provider: "c", Model: "d"}})
	}, nil, ok, nil)
	if err != nil || !r.Changed || r.Backup == "" {
		t.Fatal(r, err)
	}
	if _, err := EditProfile(context.Background(), p, nil, func(s string) (string, error) {
		return Apply(s, Changes{DefaultModel: &Selection{Provider: "x", Model: "y"}})
	}, nil, bad, nil); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p.PatchFile())
	if mustRead(t, string(data)).DefaultModel.ProviderID() != "c" || !strings.HasPrefix(string(data), "# keep me\n") {
		t.Fatal(string(data))
	}
}
