// Command dsh-model-settings is the program behind the DSH plugins
// dsh-model-defaults and dsh-web-search-extra.
//
//	dsh-model-settings serve                  run as a plugin process (started by the plugins' JavaScript shim)
//	dsh-model-settings read-profile <file>    print what a profile patch says (JSON)
//	dsh-model-settings claude-models [dir]    print the Claude models of this machine's Claude Code login
//	dsh-model-settings search …               one search outside DSH (SearXNG results, or Claude)
//	dsh-model-settings version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/usamoilov/dsh-model-settings/internal/claudecode"
	"github.com/usamoilov/dsh-model-settings/internal/defaults"
	"github.com/usamoilov/dsh-model-settings/internal/guard"
	"github.com/usamoilov/dsh-model-settings/internal/host"
	"github.com/usamoilov/dsh-model-settings/internal/profile"
	"github.com/usamoilov/dsh-model-settings/internal/rpc"
	"github.com/usamoilov/dsh-model-settings/internal/search"
)

// Version of the program; the shim checks it against its own.
const Version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		serve()
	case "read-profile":
		if len(os.Args) != 3 {
			usage()
		}
		data, err := os.ReadFile(os.Args[2])
		if err != nil {
			fail(err)
		}
		info, err := profile.Read(string(data))
		if err != nil {
			fail(err)
		}
		printJSON(info)
	case "claude-models":
		dir := ""
		if len(os.Args) > 2 {
			dir = os.Args[2]
		}
		binary := claudecode.Find(dir)
		cache := claudecode.NewModelCache(claudecode.ProcessProber(binary), binary == "")
		printJSON(map[string]any{"binary": binary, "models": cache.List(context.Background(), true)})
	case "search":
		runSearch(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(Version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: dsh-model-settings serve
       dsh-model-settings read-profile <cordis.patch.yml>
       dsh-model-settings claude-models [profile dir]
       dsh-model-settings search [-searxng URL] [-claude-model MODEL] searxng|claude-code <query>
       dsh-model-settings version`)
	os.Exit(2)
}

// runSearch runs one search outside DSH (SearXNG without the model answer, or Claude).
func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	searxngURL := fs.String("searxng", search.DefaultSearxng, "SearXNG address")
	claudeModel := fs.String("claude-model", search.DefaultClaudeModel, "Claude model alias or id, or auto")
	limit := fs.Int("max", 5, "number of results")
	_ = fs.Parse(args)
	if fs.NArg() < 2 {
		usage()
	}
	query := strings.Join(fs.Args()[1:], " ")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var (
		result search.Result
		err    error
	)
	started := time.Now()
	switch fs.Arg(0) {
	case search.SearxngID:
		sx := &search.Searxng{URL: func() string { return *searxngURL }, Summary: func() bool { return false }, SummaryModel: func(context.Context) *search.Route { return nil }}
		result, err = sx.Search(ctx, query, float64(*limit))
	case search.ClaudeID:
		c := &search.Claude{Binary: claudecode.Find(), Model: func() string { return *claudeModel }, Timeout: 90 * time.Second, Gate: make(chan struct{}, 1)}
		result, err = c.Search(ctx, query, float64(*limit))
	default:
		usage()
	}
	if err != nil {
		fail(err)
	}
	printJSON(map[string]any{"ms": time.Since(started).Milliseconds(), "result": result})
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

var plugins = map[string]struct {
	name  string
	start func(*host.Host) (any, error)
}{
	"model-defaults":   {"dsh-model-defaults", defaults.Start},
	"guard":            {"dsh-model-defaults", guard.Start},
	"web-search-extra": {"dsh-web-search-extra", search.Start},
}

// serve speaks the shim's protocol on stdin/stdout until DSH closes the link.
func serve() {
	conn := rpc.New(os.Stdin, os.Stdout)
	started := make(chan struct{})
	var once sync.Once
	conn.Handle("hello", func(ctx context.Context, params json.RawMessage) (any, error) {
		var hello host.Hello
		if err := json.Unmarshal(params, &hello); err != nil {
			return nil, err
		}
		plugin, ok := plugins[hello.Plugin]
		if !ok {
			return nil, fmt.Errorf("unknown plugin %q", hello.Plugin)
		}
		first := false
		once.Do(func() { first = true; close(started) })
		if !first {
			return nil, errors.New("hello was already sent")
		}
		h := &host.Host{Conn: conn, Hello: hello, Name: plugin.name}
		result, err := plugin.start(h)
		if err != nil {
			return nil, err
		}
		return map[string]any{"version": Version, "plugin": result}, nil
	})
	go func() {
		// a shim that never says hello is not DSH
		select {
		case <-started:
		case <-time.After(30 * time.Second):
			fmt.Fprintln(os.Stderr, "warn [dsh-model-settings] no hello from DSH within 30 s; exiting")
			os.Exit(1)
		}
	}()
	if err := conn.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, "warn [dsh-model-settings]", err)
	}
}
