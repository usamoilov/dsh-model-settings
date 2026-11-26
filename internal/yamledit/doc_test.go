package yamledit

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func mustNew(t *testing.T, text string) *Editor {
	t.Helper()
	e, err := New(text)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, text)
	}
	return e
}

func check(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Real profiles have folded and multi-line strings, comments inside presets and
// null values; edits next to them must leave them alone.
const preset = `# header
- insert:
    - id: preset
      config:
        description: Standard agent with delegation to native
          Claude Code via subagent_claude_code
        plugins:
          - id: plan-mode
            config:
              section: >
                You are in plan mode.


                Explore first.
          - id: compaction-basic
            # a note inside a preset
            config:
              modelPolicies:
                - provider: local
                  thresholdRatio: 0.6
          - id: tool-subagent
            config:
              provider: spawn
              toolName: subagent
- id: models
  config:
    reasoningEfforts:
      off:
      high: high
    input: [ text, image ]
- id: last
  name: "@scope/pkg" # trailing
`

func TestEditNearFoldedText(t *testing.T) {
	e := mustNew(t, preset)
	// the nested tool-subagent config gets a key; nothing else changes
	var target Loc = func(ed *Editor) *yaml.Node {
		var found *yaml.Node
		for _, row := range ed.Rows() {
			Walk(row, func(n *yaml.Node) {
				if id := MapValue(n, "id"); id != nil && id.Value == "tool-subagent" {
					found = MapValue(n, "config")
				}
			})
		}
		return found
	}
	if err := e.SetKey(target, "agentOptions", NewMap("provider", "p", "model", "m")); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(preset, "              toolName: subagent\n", "              toolName: subagent\n              agentOptions:\n                provider: p\n                model: m\n", 1)
	check(t, e.Text(), want)
	if err := e.DeleteKey(target, "agentOptions"); err != nil {
		t.Fatal(err)
	}
	check(t, e.Text(), preset)

	// a key appended to a row whose last value is a folded or flow value
	if err := e.SetKey(RowLoc(1), "disabled", true); err != nil {
		t.Fatal(err)
	}
	check(t, e.Text(), strings.Replace(preset, "    input: [ text, image ]\n", "    input: [ text, image ]\n  disabled: true\n", 1))
	if err := e.DeleteKey(RowLoc(1), "disabled"); err != nil {
		t.Fatal(err)
	}
	// a quoted value with a trailing comment: only the value changes
	if err := e.SetKey(RowLoc(2), "name", "@scope/other"); err != nil {
		t.Fatal(err)
	}
	check(t, e.Text(), strings.Replace(preset, `"@scope/pkg" # trailing`, `"@scope/other" # trailing`, 1))
}

func TestRowsAndFlow(t *testing.T) {
	// flow rows are rewritten in block style when edited
	e := mustNew(t, "- {id: a, disabled: true}\n- id: b\n")
	if err := e.DeleteKey(RowLoc(0), "disabled"); err != nil {
		t.Fatal(err)
	}
	check(t, e.Text(), "- id: a\n- id: b\n")
	// a flow root list becomes a block list; [] becomes empty
	e = mustNew(t, "# keep\n[]\n")
	if err := e.AppendRow("note", NewMap("id", "x", "disabled", true)); err != nil {
		t.Fatal(err)
	}
	check(t, e.Text(), "# keep\n# note\n- id: x\n  disabled: true\n")
	e = mustNew(t, "[{id: a}]\n")
	check(t, e.Text(), "- id: a\n")
	// deleting the first key of a row moves the next key onto the dash line
	e = mustNew(t, "- disabled: true\n  id: a\n- id: b\n")
	if err := e.DeleteKey(RowLoc(0), "disabled"); err != nil {
		t.Fatal(err)
	}
	check(t, e.Text(), "- id: a\n- id: b\n")
	// a row is deleted with its marker comment, a hand comment above stays
	e = mustNew(t, "- id: a\n# hand\n# mark: x\n#  more\n- id: b\n  disabled: true\n- id: c\n")
	if err := e.DeleteRow(1, "mark"); err != nil {
		t.Fatal(err)
	}
	check(t, e.Text(), "- id: a\n# hand\n- id: c\n")
	// CRLF files keep CRLF
	e = mustNew(t, "- id: a\r\n  config:\r\n    k: v\r\n")
	if err := e.SetKey(KeyLoc(RowLoc(0), "config"), "k", "w"); err != nil {
		t.Fatal(err)
	}
	check(t, e.Text(), "- id: a\r\n  config:\r\n    k: w\r\n")
	// comment-only files are empty lists
	e = mustNew(t, "# only a comment\n")
	if len(e.Rows()) != 0 {
		t.Fatal("rows in a comment-only file")
	}
	if _, err := New("id: x\n"); err != ErrNotList {
		t.Fatal(err)
	}
}

func TestScalars(t *testing.T) {
	for in, want := range map[string]string{
		"off": "off", "claude-opus-5-5": "claude-opus-5-5", "true": `"true"`, "12": `"12"`, "1.5": `"1.5"`,
		"@scope/x": `"@scope/x"`, "a: b": `"a: b"`, "": `""`, "x #y": `"x #y"`, "line\nnext": `"line\nnext"`,
		"sonnet[1m]": `"sonnet[1m]"`, "~": `"~"`, "~/svc/x.yml": "~/svc/x.yml", "2026-11-08": "2026-11-08",
	} {
		if got := renderScalar(in); got != want {
			t.Errorf("renderScalar(%q) = %s, want %s", in, got, want)
		}
		e := mustNew(t, "- id: "+renderScalar(in)+"\n")
		if got := ToValue(e.Rows()[0]).(*Map).Value("id"); got != in {
			t.Errorf("round trip %q → %v", in, got)
		}
	}
	if renderScalar(JS("process.cwd()")) != "!!js process.cwd()" {
		t.Error("js")
	}
	if renderScalar(float64(60)) != "60" || renderScalar(int64(-3)) != "-3" || renderScalar(nil) != "null" {
		t.Error("numbers")
	}
}
