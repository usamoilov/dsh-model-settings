package profile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/usamoilov/dsh-model-settings/internal/fsutil"
	"github.com/usamoilov/dsh-model-settings/internal/i18n"
)

// KeepBackups is how many `<file>.bak-model-defaults-*` copies are kept.
const KeepBackups = 5

// DSH is the installed DSH that runs `--dump-config` checks.
type DSH struct {
	Node  string // node executable (the one running DSH)
	Entry string // <@deepseek-ai/dsh>/lib/bin.js
}

// FindDSH locates DSH: DSH_ROOT, else the package of the running DSH
// (argv1 is its lib/bin.js, possibly through a symlink), else `dsh` on PATH.
func FindDSH(node, argv1 string) (DSH, error) {
	var roots []string
	if r := os.Getenv("DSH_ROOT"); r != "" {
		roots = append(roots, r)
	}
	bins := []string{argv1}
	if p, err := exec.LookPath("dsh"); err == nil {
		bins = append(bins, p)
	}
	for _, bin := range bins {
		if bin == "" {
			continue
		}
		if real, err := filepath.EvalSymlinks(bin); err == nil {
			roots = append(roots, filepath.Dir(filepath.Dir(real)))
		}
	}
	for _, root := range roots {
		entry := filepath.Join(root, "lib", "bin.js")
		if _, err := os.Stat(entry); err == nil {
			if node == "" {
				if p, err := exec.LookPath("node"); err == nil {
					node = p
				}
			}
			if node == "" {
				return DSH{}, errors.New("node not found to run DSH")
			}
			return DSH{Node: node, Entry: entry}, nil
		}
	}
	return DSH{}, errors.New("DSH installation not found: run inside DSH, put `dsh` on PATH, or set DSH_ROOT to the @deepseek-ai/dsh package folder")
}

// ComposeResult is the outcome of a `--dump-config` run.
type ComposeResult struct {
	OK     bool
	Output string
	Error  string
}

// CheckComposes runs `dsh --profile <name> [--patch …] --dump-config`: the composition must still load.
func (d DSH) CheckComposes(ctx context.Context, profile string, patches []string) ComposeResult {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	args := []string{d.Entry, "--profile", profile}
	for _, p := range patches {
		args = append(args, "--patch", p)
	}
	args = append(args, "--dump-config")
	cmd := exec.CommandContext(ctx, d.Node, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if r := []rune(msg); len(r) > 800 {
			msg = string(r[len(r)-800:])
		}
		return ComposeResult{Error: msg}
	}
	return ComposeResult{OK: true, Output: stdout.String()}
}

// Target is one YAML patch file to change.
type Target struct {
	File    string
	Lock    string // the file whose lock serializes writers
	Label   string
	Compose func(ctx context.Context) ComposeResult
}

// Edited is the outcome of EditPatchFile.
type Edited struct {
	Changed bool
	Backup  string
}

// EditPatchFile changes one patch file: under the lock it backs up and writes
// atomically; then, with the lock released (`dsh` takes the profile lock while
// booting), it checks that the composition still loads and rolls back if it
// does not. verify returns a translated problem with the composed config, or "".
func EditPatchFile(ctx context.Context, target Target, change func(string) (string, error), verify func(dump string) string, t i18n.T) (Edited, error) {
	if t == nil {
		t = i18n.English
	}
	var before, after, backup string
	written := false
	err := fsutil.WithFileLock(ctx, target.Lock, 0, func() error {
		data, err := os.ReadFile(target.File)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		before = string(data)
		after, err = change(before)
		if err != nil {
			return err
		}
		if after == before {
			return nil
		}
		if before != "" {
			backup = fmt.Sprintf("%s.bak-model-defaults-%s", target.File, time.Now().Format("20060102-150405"))
			if err := os.WriteFile(backup, []byte(before), 0o600); err != nil {
				return err
			}
		}
		if err := fsutil.WriteFileAtomic(target.File, []byte(after), 0o600); err != nil {
			return err
		}
		written = true
		return nil
	})
	if err != nil {
		return Edited{}, err
	}
	if !written {
		return Edited{}, nil
	}
	result := target.Compose(ctx)
	problem := ""
	if !result.OK {
		problem = t("err.composeFailed", i18n.Params{"error": result.Error})
	} else if verify != nil {
		problem = verify(result.Output)
	}
	if problem != "" {
		_ = fsutil.WithFileLock(context.Background(), target.Lock, 0, func() error {
			// restore only if nobody wrote the file after us
			current, err := os.ReadFile(target.File)
			if err != nil || string(current) != after {
				return nil
			}
			restore := before
			if restore == "" {
				restore = "[]\n"
			}
			return fsutil.WriteFileAtomic(target.File, []byte(restore), 0o600)
		})
		return Edited{}, i18n.Fail(422, "err.compose", i18n.Params{"target": target.Label, "problem": problem})
	}
	pruneBackups(target.File)
	return Edited{Changed: true, Backup: backup}, nil
}

func pruneBackups(file string) {
	dir := filepath.Dir(file)
	prefix := filepath.Base(file) + ".bak-model-defaults-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i := 0; i < len(names)-KeepBackups; i++ {
		_ = os.Remove(filepath.Join(dir, names[i]))
	}
}

// Dir is a profile folder.
type Dir struct {
	Name string
	Dir  string
}

// PatchFile is the profile's own patch.
func (p Dir) PatchFile() string { return filepath.Join(p.Dir, "cordis.patch.yml") }

// EditProfile changes a profile's own patch (`<dir>/cordis.patch.yml`, lock on
// `<dir>/package.json`); compose defaults to the DSH check of this profile.
func EditProfile(ctx context.Context, p Dir, dsh *DSH, change func(string) (string, error), verify func(string) string, compose func(ctx context.Context, name string) ComposeResult, t i18n.T) (Edited, error) {
	if t == nil {
		t = i18n.English
	}
	if compose == nil {
		compose = func(ctx context.Context, name string) ComposeResult {
			if dsh == nil {
				return ComposeResult{Error: "DSH installation not found"}
			}
			return dsh.CheckComposes(ctx, name, nil)
		}
	}
	return EditPatchFile(ctx, Target{
		File:    p.PatchFile(),
		Lock:    filepath.Join(p.Dir, "package.json"),
		Label:   t("target.profile", i18n.Params{"profile": p.Name}),
		Compose: func(ctx context.Context) ComposeResult { return compose(ctx, p.Name) },
	}, change, verify, t)
}

// Home is the user's home folder.
func Home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// ExpandHome turns a leading `~` into the home folder.
func ExpandHome(p string) string {
	if p == "~" {
		return Home()
	}
	if strings.HasPrefix(p, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(p, `~\`)) {
		return filepath.Join(Home(), p[2:])
	}
	return p
}

// Tildify writes a path under the home folder as `~/…`.
func Tildify(p string) string {
	home := Home()
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(p[len(home)+1:])
	}
	return p
}

// DSHHome is $DSH_HOME or ~/.dsh.
func DSHHome() string {
	if h := os.Getenv("DSH_HOME"); h != "" {
		return h
	}
	return filepath.Join(Home(), ".dsh")
}
