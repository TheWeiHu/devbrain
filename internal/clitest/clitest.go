// Package clitest builds the devbrain binary once per test process and drives it
// as a subprocess — the Go-native replacement for the scripts/test-*.sh black-box
// suite. Tests assert on stdout/stderr, exit code, and on-disk state, exactly as
// the bash did, but under `go test`: typed, subtest-scoped, and cross-platform.
package clitest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// SkipIfExcluded skips the test when DEVBRAIN_TEST_SKIP (a regexp, e.g.
// "docker") matches keyword. The nightshift merge gate sets it to drop the slow
// docker clean-room test from the fast per-turn pass; CI leaves it unset and
// runs everything. An empty value runs every test. A keyword that is BOTH
// excluded and required (see RequiredToRun) is a misconfiguration — fail loudly
// rather than silently pick one.
func SkipIfExcluded(t testing.TB, keyword string) {
	pat := os.Getenv("DEVBRAIN_TEST_SKIP")
	if pat == "" {
		return
	}
	if ok, _ := regexp.MatchString(pat, keyword); ok {
		if RequiredToRun(keyword) {
			t.Fatalf("DEVBRAIN_TEST_SKIP=%q and DEVBRAIN_TEST_REQUIRE both match %q — pick one", pat, keyword)
		}
		t.Skipf("DEVBRAIN_TEST_SKIP=%q excludes %q", pat, keyword)
	}
}

// RequiredToRun reports whether keyword matches DEVBRAIN_TEST_REQUIRE (a regexp),
// naming tests that MUST actually run — not skip — on this runner. CI sets it (e.g.
// "docker") on the runner that has the dependency, so a clean-room test that would
// silently skip turns the build RED instead of a false GREEN. Empty ⇒ nothing required.
func RequiredToRun(keyword string) bool {
	pat := os.Getenv("DEVBRAIN_TEST_REQUIRE")
	if pat == "" {
		return false
	}
	ok, _ := regexp.MatchString(pat, keyword)
	return ok
}

// SkipUnlessRequired skips the test with the given reason — UNLESS keyword is listed
// in DEVBRAIN_TEST_REQUIRE, in which case the skip is upgraded to a failure. Use it at
// every "dependency missing, bail out" site of a test CI is configured to guarantee runs.
func SkipUnlessRequired(t testing.TB, keyword, format string, args ...any) {
	t.Helper()
	if RequiredToRun(keyword) {
		t.Fatalf("DEVBRAIN_TEST_REQUIRE=%q requires %q to run, but it would skip: "+format,
			append([]any{os.Getenv("DEVBRAIN_TEST_REQUIRE"), keyword}, args...)...)
	}
	t.Skipf(format, args...)
}

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// Bin builds ./cmd/devbrain once per test process and returns the binary path.
func Bin(t testing.TB) string {
	t.Helper()
	buildOnce.Do(func() {
		root, err := moduleRoot()
		if err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "devbrain-clitest")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "devbrain")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/devbrain")
		cmd.Dir = root
		if out, e := cmd.CombinedOutput(); e != nil {
			buildErr = fmt.Errorf("build devbrain: %v\n%s", e, out)
		}
	})
	if buildErr != nil {
		t.Fatalf("clitest.Bin: %v", buildErr)
	}
	return binPath
}

// Root returns the repo checkout root (the dir holding go.mod) — for tests that
// read committed files (assets/, testdata/golden/, scripts/git-hooks/).
func Root(t testing.TB) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found walking up from cwd")
		}
		dir = parent
	}
}

// Harness drives the built binary against a throwaway DEVBRAIN_DATA.
type Harness struct {
	T       testing.TB
	Bin     string
	Data    string            // DEVBRAIN_DATA (a t.TempDir by default)
	Project string            // DEVBRAIN_PROJECT (default "testproj")
	Env     map[string]string // extra env applied to every Run
}

// New returns a harness with a fresh temp DEVBRAIN_DATA and project "testproj".
func New(t testing.TB) *Harness {
	return &Harness{T: t, Bin: Bin(t), Data: t.TempDir(), Project: "testproj", Env: map[string]string{}}
}

// Result is one invocation's outcome.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// Out returns trimmed stdout — the common case (an id, a field value).
func (r Result) Out() string { return strings.TrimRight(r.Stdout, "\n") }

// RunOpts customizes a single invocation. Env here overrides Harness.Env and the
// DEVBRAIN_DATA/PROJECT defaults for this call only (e.g. DEVBRAIN_PROJECT=other).
//
// CleanEnv replicates the shell `env -i`: the child sees ONLY the Env map (no
// os.Environ, no DEVBRAIN_DATA/PROJECT defaults). Install tests need this so an
// inherited XDG_CONFIG_HOME (set on CI runners) can't redirect where the config
// and data repo land — the exact leak that passed on macOS but failed on Linux.
type RunOpts struct {
	Stdin    string
	Dir      string
	Env      map[string]string
	CleanEnv bool
}

// Run invokes `devbrain <args>` with the harness defaults.
func (h *Harness) Run(args ...string) Result { return h.RunWith(RunOpts{}, args...) }

// RunWith invokes `devbrain <args>` with per-call stdin / cwd / env overrides.
func (h *Harness) RunWith(o RunOpts, args ...string) Result {
	h.T.Helper()
	cmd := exec.Command(h.Bin, args...)
	if o.Dir != "" {
		cmd.Dir = o.Dir
	}
	var env []string
	if !o.CleanEnv {
		// Scrub inherited DEVBRAIN_* runtime config so a runner's environment can't
		// poison these hermetic black-box tests — a nightshift worker shell exports
		// DEVBRAIN_TODO_ONLY (the fixed-set fence), which would filter the queue the
		// binary reads and false-red the todo suite. The harness sets what the child
		// should see; explicit h.Env / o.Env overrides below still win.
		for _, kv := range os.Environ() {
			if strings.HasPrefix(kv, "DEVBRAIN_") {
				continue
			}
			env = append(env, kv)
		}
		env = append(env, "DEVBRAIN_DATA="+h.Data, "DEVBRAIN_PROJECT="+h.Project)
		for k, v := range h.Env {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range o.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	if o.Stdin != "" {
		cmd.Stdin = strings.NewReader(o.Stdin)
	}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			h.T.Fatalf("run %v: %v", args, err)
		}
	}
	return Result{Stdout: out.String(), Stderr: errb.String(), Code: code}
}

// TaskFile returns the on-disk path of a todo task in the harness project.
func (h *Harness) TaskFile(id string) string {
	return filepath.Join(h.Data, "projects", h.Project, "todo", id+".md")
}

// ── shared file / git / text helpers ──

// Git runs a git command in dir (or cwd if dir==""), failing the test on error.
func Git(t testing.TB, dir string, args ...string) {
	t.Helper()
	full := []string{"-c", "user.email=a@b.c", "-c", "user.name=t", "-c", "commit.gpgsign=false"}
	if dir != "" {
		full = append([]string{"-C", dir}, full...)
	}
	cmd := exec.Command("git", append(full, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// WriteFile writes content, creating parent dirs.
func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// WriteExec writes an executable script (a PATH stub, a fake oracle, …).
func WriteExec(t testing.TB, path, content string) {
	t.Helper()
	WriteFile(t, path, content)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// Read returns a file's contents as a string, failing the test on error.
func Read(t testing.TB, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Find walks root and returns paths whose base name matches the glob pattern.
func Find(t testing.TB, root, nameGlob string) []string {
	t.Helper()
	var hits []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ok, _ := filepath.Match(nameGlob, d.Name()); ok {
			hits = append(hits, p)
		}
		return nil
	})
	return hits
}

// Field extracts the value of a `key: value` line (like `sed -n s/^key: //p`).
func Field(text, key string) string {
	for _, ln := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(ln, key+": "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// HasLineWith reports whether some line contains all of the substrings (order-free,
// the semantics of a `grep a | grep b` chain).
func HasLineWith(text string, subs ...string) bool {
	for _, ln := range strings.Split(text, "\n") {
		all := true
		for _, sub := range subs {
			if !strings.Contains(ln, sub) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
