package defaults

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/usamoilov/dsh-model-settings/internal/profile"
)

// DefaultSearxng is where SearXNG is expected.
const DefaultSearxng = "http://127.0.0.1:8888"

// PrefsFile is the file next to the running profile's patch that keeps where
// the page applies the default model.
const PrefsFile = ".model-defaults.json"

// Overlay is an extra `--patch` file that restates `subagent-claude-code`.
type Overlay struct {
	Path    string   `json:"path"`
	Label   string   `json:"label"`
	Profile string   `json:"profile"`
	Patches []string `json:"patches"`
}

// Config is the plugin config (row `model-defaults`, all optional).
type Config struct {
	Profiles   []string  `json:"profiles"`
	WebUnit    *string   `json:"webUnit"`
	Overlays   []Overlay `json:"overlays"`
	SearxngURL string    `json:"searxngURL"`
}

var (
	profileName = regexp.MustCompile(`^[\w.-]+$`)
	unitName    = regexp.MustCompile(`^[\w@.-]+\.service$`)
)

// ReadConfig normalizes the plugin config; a unit name is never a shell string.
func ReadConfig(raw json.RawMessage) Config {
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	out := Config{Profiles: []string{}, Overlays: []Overlay{}, SearxngURL: DefaultSearxng}
	if list, ok := cfg["profiles"].([]any); ok {
		for _, p := range list {
			if s, ok := p.(string); ok && profileName.MatchString(s) {
				out.Profiles = append(out.Profiles, s)
			}
		}
	}
	if s, ok := cfg["webUnit"].(string); ok && unitName.MatchString(s) {
		out.WebUnit = &s
	}
	if list, ok := cfg["overlays"].([]any); ok {
		for _, item := range list {
			o, _ := item.(map[string]any)
			p, ok := o["path"].(string)
			if !ok {
				continue
			}
			ov := Overlay{Path: profile.ExpandHome(p), Label: filepath.Base(p), Profile: "headless", Patches: []string{}}
			if s, ok := o["label"].(string); ok && s != "" {
				ov.Label = s
			}
			if s, ok := o["profile"].(string); ok && s != "" {
				ov.Profile = s
			}
			if patches, ok := o["patches"].([]any); ok {
				for _, pp := range patches {
					if s, ok := pp.(string); ok {
						ov.Patches = append(ov.Patches, profile.ExpandHome(s))
					}
				}
			}
			out.Overlays = append(out.Overlays, ov)
		}
	}
	if s, ok := cfg["searxngURL"].(string); ok && s != "" {
		out.SearxngURL = s
	}
	return out
}

// ReadApplyPrefs gives `{profile: ticked}` for the managed profiles from the
// prefs file text; a profile is ticked unless saved as false.
func ReadApplyPrefs(text string, names []string) map[string]bool {
	saved := savedApply(text)
	out := map[string]bool{}
	for _, name := range names {
		out[name] = saved[name] != false
	}
	return out
}

func savedApply(text string) map[string]any {
	var data map[string]any
	if json.Unmarshal([]byte(text), &data) != nil {
		return map[string]any{}
	}
	apply, ok := data["apply"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return apply
}

// ParseApplyPrefs turns a posted `remember` object into `{profile: ticked}`, or nil.
func ParseApplyPrefs(raw any, names []string) map[string]bool {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, name := range names {
		out[name] = m[name] != false
	}
	return out
}

var guardRow = regexp.MustCompile(`(?m)^\s*-?\s*id:\s*['"]?model-defaults-guard['"]?\s*$`)

// HasGuard: the subagent guard runs in a profile — through this package's bundle or its own row.
func HasGuard(patchText string, bundles []string) bool {
	for _, b := range bundles {
		if b == "dsh-model-defaults" {
			return true
		}
	}
	return guardRow.MatchString(patchText)
}

// profilePackages: packages linked into a profile (package.json dependencies) and its bundles.
func profilePackages(dir string) (deps, bundles []string) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, nil
	}
	var pkg struct {
		Dependencies map[string]any `json:"dependencies"`
		DSH          struct {
			Profile struct {
				Bundles []any `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil, nil
	}
	for k := range pkg.Dependencies {
		deps = append(deps, k)
	}
	for _, b := range pkg.DSH.Profile.Bundles {
		if s, ok := b.(string); ok {
			bundles = append(bundles, s)
		}
	}
	return deps, bundles
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var escapeRe = regexp.MustCompile(`[.*+?^${}()|\[\]\\]`)

// providerDeclared: the composed tree declares this provider route (a pi-ai
// providers key, or DeepSeek's built-in routes).
func providerDeclared(dump, provider string) bool {
	if profile.IsDeepseekProvider(provider) {
		return true
	}
	re := regexp.MustCompile(`(?m)^\s+` + escapeRe.ReplaceAllString(provider, `\$0`) + `:\s*$`)
	return re.MatchString(dump)
}

var extraRow = regexp.MustCompile(`(?m)^- id: web-search-extra$`)

func trimCR(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
