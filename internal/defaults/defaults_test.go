package defaults

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/usamoilov/dsh-model-settings/internal/profile"
)

func TestReadConfig(t *testing.T) {
	empty := ReadConfig(nil)
	data, _ := json.Marshal(empty)
	if string(data) != `{"profiles":[],"webUnit":null,"overlays":[],"searxngURL":"http://127.0.0.1:8888"}` {
		t.Fatal(string(data))
	}
	cfg := ReadConfig(json.RawMessage(`{
		"profiles": ["web", "bad/name", 7, "headless"],
		"webUnit": "dsh-web.service",
		"overlays": [{"path": "~/svc/extra.patch.yml", "label": "Service", "patches": ["~/svc/base.patch.yml", 3]}, {"path": 5}, {"path": "/x/y.yml"}],
		"searxngURL": "http://search.lan:8080"}`))
	home := profile.Home()
	if !reflect.DeepEqual(cfg.Profiles, []string{"web", "headless"}) || *cfg.WebUnit != "dsh-web.service" || cfg.SearxngURL != "http://search.lan:8080" {
		t.Fatalf("%+v", cfg)
	}
	want := []Overlay{
		{Path: filepath.Join(home, "svc", "extra.patch.yml"), Label: "Service", Profile: "headless", Patches: []string{filepath.Join(home, "svc", "base.patch.yml")}},
		{Path: "/x/y.yml", Label: "y.yml", Profile: "headless", Patches: []string{}},
	}
	if !reflect.DeepEqual(cfg.Overlays, want) {
		t.Fatalf("%+v", cfg.Overlays)
	}
	if ReadConfig(json.RawMessage(`{"webUnit":"rm -rf /; x.service"}`)).WebUnit != nil {
		t.Fatal("a unit name is never a shell string")
	}
	if ReadConfig(json.RawMessage(`{"webUnit":"dsh-web"}`)).WebUnit != nil {
		t.Fatal("not a unit")
	}
}

func TestApplyPrefs(t *testing.T) {
	both := []string{"web", "headless"}
	all := map[string]bool{"web": true, "headless": true}
	for _, text := range []string{"", "not json", `{"apply":[false]}`} {
		if got := ReadApplyPrefs(text, both); !reflect.DeepEqual(got, all) {
			t.Fatal(text, got)
		}
	}
	if got := ReadApplyPrefs(`{"apply":{"headless":false,"other":false}}`, both); !reflect.DeepEqual(got, map[string]bool{"web": true, "headless": false}) {
		t.Fatal(got)
	}
	if ParseApplyPrefs(nil, both) != nil || ParseApplyPrefs([]any{false}, both) != nil {
		t.Fatal("not an object")
	}
	if got := ParseApplyPrefs(map[string]any{"headless": false, "web": "yes", "evil": false}, both); !reflect.DeepEqual(got, map[string]bool{"web": true, "headless": false}) {
		t.Fatal(got)
	}
}

func TestGuardAndProviders(t *testing.T) {
	if !HasGuard("", []string{"dsh-model-defaults"}) || !HasGuard("- insert:\n    - id: model-defaults-guard\n      name: /x/guard.js\n", nil) || HasGuard("- id: model-defaults\n  config: {}\n", []string{"other"}) {
		t.Fatal("hasGuard")
	}
	dump := "- id: llm-pi-ai\n  config:\n    providers:\n      local-llm:\n        api: x\n"
	if !providerDeclared(dump, "local-llm") || providerDeclared(dump, "local") || !providerDeclared(dump, "deepseek-official") {
		t.Fatal("providerDeclared")
	}
	if providerDeclared("    a.b:\n", "a+b") {
		t.Fatal("regexp characters are escaped")
	}
}
