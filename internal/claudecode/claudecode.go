// Package claudecode runs Claude Code the way the Claude Agent SDK does: the
// native `claude` binary the SDK ships (or `claude` on PATH) with
// `--input-format stream-json --output-format stream-json`, an `initialize`
// control request first, then (for a query) one user message.
//
// Every run is isolated: no user or project settings, hooks or CLAUDE.md
// (`--setting-sources=`), no MCP servers (`--strict-mcp-config`), nothing
// persisted (`--no-session-persistence`), a minimal environment.
package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AuthEnv are the Claude Code auth variables that live in the environment
// instead of `~/.claude` (for example a long-lived `claude setup-token`
// token). Runs get a minimal environment, so these are passed on as is.
var AuthEnv = []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CONFIG_DIR"}

// AuthFrom returns the set members of AuthEnv from getenv.
func AuthFrom(getenv func(string) string) map[string]string {
	out := map[string]string{}
	for _, name := range AuthEnv {
		if v := getenv(name); v != "" {
			out[name] = v
		}
	}
	return out
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// HasCredentials: Claude Code can authenticate — a stored login or an explicit token or key.
func HasCredentials() bool {
	auth := AuthFrom(os.Getenv)
	if auth["CLAUDE_CODE_OAUTH_TOKEN"] != "" || auth["ANTHROPIC_API_KEY"] != "" || auth["ANTHROPIC_AUTH_TOKEN"] != "" {
		return true
	}
	dirs := []string{filepath.Join(home(), ".claude")}
	if d := auth["CLAUDE_CONFIG_DIR"]; d != "" {
		dirs = append([]string{d}, dirs...)
	}
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, ".credentials.json")); err == nil {
			return true
		}
	}
	return false
}

// Env is the environment of a run: HOME, PATH, LANG, USER, TMPDIR and the auth variables.
func Env() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "C.UTF-8"
	}
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	env := []string{"HOME=" + home(), "PATH=" + path, "LANG=" + lang, "USER=" + user, "TMPDIR=" + os.TempDir()}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "TEMP", "TMP", "COMSPEC"} {
			if v := os.Getenv(name); v != "" {
				env = append(env, name+"="+v)
			}
		}
	}
	for name, v := range AuthFrom(os.Getenv) {
		env = append(env, name+"="+v)
	}
	env = append(env, testEnv...)
	return append(env, "CLAUDE_CODE_ENTRYPOINT=sdk-ts")
}

// testEnv is set by this package's tests only (the fake Claude Code reads it).
var testEnv []string

func platform() (string, string) {
	osName := runtime.GOOS
	if osName == "windows" {
		osName = "win32"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return osName, arch
}

// Find returns the Claude Code binary: the native one of the Claude Agent SDK
// in a DSH profile (the given folders first, then the web and headless
// profiles), else `claude` on PATH; "" when there is none.
func Find(profileDirs ...string) string {
	dshHome := os.Getenv("DSH_HOME")
	if dshHome == "" {
		dshHome = filepath.Join(home(), ".dsh")
	}
	dirs := append(append([]string{}, profileDirs...), filepath.Join(dshHome, "profiles", "web"), filepath.Join(dshHome, "profiles", "headless"))
	osName, arch := platform()
	exe := "claude"
	if runtime.GOOS == "windows" {
		exe = "claude.exe"
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		for _, variant := range []string{"", "-musl"} {
			file := filepath.Join(dir, "node_modules", "@anthropic-ai", fmt.Sprintf("claude-agent-sdk-%s-%s%s", osName, arch, variant), exe)
			if st, err := os.Stat(file); err == nil && !st.IsDir() {
				return file
			}
		}
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return ""
}

// Args of a run: the SDK's streaming flags plus the isolation flags.
func baseArgs() []string {
	return []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json"}
}

// ProbeArgs start Claude Code with no tools (the model list needs no prompt).
func ProbeArgs() []string {
	return append(baseArgs(), "--tools", "", "--setting-sources=", "--strict-mcp-config", "--no-session-persistence")
}

// SearchArgs allow the WebSearch tool only.
func SearchArgs(model string, maxTurns int) []string {
	args := append(baseArgs(), "--max-turns", fmt.Sprint(maxTurns))
	if model != "" {
		args = append(args, "--model", model)
	}
	return append(args, "--allowedTools", "WebSearch", "--tools", "WebSearch", "--setting-sources=", "--strict-mcp-config",
		"--permission-mode", "dontAsk", "--no-session-persistence")
}

// Message is one stream-json message.
type Message map[string]any

// Type is the message type.
func (m Message) Type() string { s, _ := m["type"].(string); return s }

// Str returns a string field.
func (m Message) Str(key string) string { s, _ := m[key].(string); return s }

// Process is one running Claude Code.
type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	msgs   chan Message
	done   chan struct{}
	quit   chan struct{} // closed by Stop: later messages are dropped
	stderr *tail
	err    error
	nextID atomic.Int64
	once   sync.Once
	stop   sync.Once
	wmu    sync.Mutex
}

type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// Start runs the binary; ctx ending kills it.
func Start(ctx context.Context, binary string, args []string, cwd string, env []string) (*Process, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &Process{cmd: cmd, stdin: stdin, msgs: make(chan Message, 64), done: make(chan struct{}), quit: make(chan struct{}), stderr: &tail{}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		reader := bufio.NewReaderSize(stdout, 1<<16)
		for {
			line, err := reader.ReadBytes('\n')
			if line = bytes.TrimSpace(line); len(line) > 0 {
				var m Message
				if json.Unmarshal(line, &m) == nil {
					if m.Type() == "control_request" {
						p.refuse(m)
					} else {
						select {
						case p.msgs <- m:
						case <-p.quit:
						}
					}
				}
			}
			if err != nil {
				break
			}
		}
		p.err = cmd.Wait()
		close(p.msgs)
		close(p.done)
	}()
	return p, nil
}

// refuse answers a control request from Claude Code (permission prompts, hooks): none are offered.
func (p *Process) refuse(m Message) {
	id := m.Str("request_id")
	_ = p.Send(Message{"type": "control_response", "response": Message{"subtype": "error", "request_id": id, "error": "not supported by this client"}})
}

// Send writes one message.
func (p *Process) Send(m any) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

// Next is the next message; io.EOF with the exit reason when Claude Code ended.
func (p *Process) Next(ctx context.Context) (Message, error) {
	select {
	case m, ok := <-p.msgs:
		if ok {
			return m, nil
		}
		reason := p.stderr.String()
		if reason == "" && p.err != nil {
			reason = p.err.Error()
		}
		if reason == "" {
			return nil, io.EOF
		}
		if r := []rune(reason); len(r) > 300 {
			reason = string(r[len(r)-300:])
		}
		return nil, fmt.Errorf("Claude Code exited: %s", reason)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Initialize sends the `initialize` control request and returns its response.
func (p *Process) Initialize(ctx context.Context) (Message, error) {
	id := fmt.Sprintf("init-%d", p.nextID.Add(1))
	if err := p.Send(Message{"request_id": id, "type": "control_request", "request": Message{"subtype": "initialize"}}); err != nil {
		return nil, err
	}
	for {
		m, err := p.Next(ctx)
		if err != nil {
			return nil, err
		}
		if m.Type() != "control_response" {
			continue
		}
		resp, _ := m["response"].(map[string]any)
		if resp == nil || resp["request_id"] != id {
			continue
		}
		if resp["subtype"] == "error" {
			return nil, fmt.Errorf("initialize: %v", resp["error"])
		}
		inner, _ := resp["response"].(map[string]any)
		return Message(inner), nil
	}
}

// SendPrompt writes one user message (what the SDK sends for a string prompt).
func (p *Process) SendPrompt(text string) error {
	return p.Send(Message{
		"type":               "user",
		"session_id":         "",
		"message":            Message{"role": "user", "content": []any{Message{"type": "text", "text": text}}},
		"parent_tool_use_id": nil,
	})
}

// CloseInput ends stdin: Claude Code finishes and exits.
func (p *Process) CloseInput() { p.once.Do(func() { _ = p.stdin.Close() }) }

// Stop ends the process: stdin closed, then killed after a grace period.
func (p *Process) Stop(grace time.Duration) {
	p.CloseInput()
	p.stop.Do(func() { close(p.quit) })
	select {
	case <-p.done:
	case <-time.After(grace):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
	}
}

// ErrMissing: no Claude Code binary was found.
var ErrMissing = errors.New("Claude Code not found: install @deepseek-ai/dsh-subagent-claude-code in the DSH profile or put `claude` on PATH")
