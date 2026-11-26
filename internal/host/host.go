// Package host is what every plugin of this process shares: the `hello` the
// JavaScript shim sends first, the link back into DSH and the log.
//
// The shim runs inside DSH (Node.js) and owns everything only DSH can do:
// registering routes, hooks and search providers, and calling DSH services
// (the model catalog, the default model, ctx.llm, ctx.web). This process does
// the rest. Logs go to stderr as `info …` / `warn …` lines; the shim prints them.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/usamoilov/dsh-model-settings/internal/profile"
	"github.com/usamoilov/dsh-model-settings/internal/rpc"
)

// Hello is the first request of the shim.
type Hello struct {
	Plugin  string          `json:"plugin"`
	Config  json.RawMessage `json:"config"`
	Profile struct {
		Name string `json:"name"`
		Dir  string `json:"dir"`
	} `json:"profile"`
	Node  string `json:"node"`  // process.execPath of DSH
	Argv1 string `json:"argv1"` // DSH's lib/bin.js
}

// Host is one plugin's view of DSH.
type Host struct {
	Conn  *rpc.Conn
	Hello Hello
	Name  string // log prefix, e.g. dsh-model-defaults

	dshOnce sync.Once
	dsh     *profile.DSH
}

// Running is the running profile (name and folder; `web` under $DSH_HOME by default).
func (h *Host) Running() profile.Dir {
	name := h.Hello.Profile.Name
	if name == "" {
		name = "web"
	}
	dir := h.Hello.Profile.Dir
	if dir == "" {
		dir = filepath.Join(profile.DSHHome(), "profiles", name)
	}
	return profile.Dir{Name: name, Dir: dir}
}

// DSH is the installed DSH for `--dump-config` checks (nil when not found).
func (h *Host) DSH() *profile.DSH {
	h.dshOnce.Do(func() {
		if d, err := profile.FindDSH(h.Hello.Node, h.Hello.Argv1); err == nil {
			h.dsh = &d
		} else {
			h.Warn("%v", err)
		}
	})
	return h.dsh
}

// Call asks the shim (DSH) for something.
func (h *Host) Call(ctx context.Context, method string, params, out any) error {
	return h.Conn.Call(ctx, method, params, out)
}

func (h *Host) log(level, format string, args ...any) {
	msg := strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", " ")
	fmt.Fprintf(os.Stderr, "%s [%s] %s\n", level, h.Name, msg)
}

// Info logs a line.
func (h *Host) Info(format string, args ...any) { h.log("info", format, args...) }

// Warn logs a warning.
func (h *Host) Warn(format string, args ...any) { h.log("warn", format, args...) }

// Selection is DSH's live default model (agentDefaultModel.currentSelection()).
type Selection struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoningEffort"`
}

// CurrentSelection asks DSH for the live default model; nil when this scope has no such service.
func (h *Host) CurrentSelection(ctx context.Context) *Selection {
	var s *Selection
	if err := h.Call(ctx, "currentSelection", nil, &s); err != nil || s == nil || s.Provider == "" {
		return nil
	}
	return s
}
