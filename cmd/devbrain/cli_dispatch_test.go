package main

// Go-native port of scripts/test-devbrain-cli.sh: the front-door dispatcher
// contract — subcommands route correctly and exit codes survive dispatch.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// versionBin builds a version-stamped devbrain binary (with -ldflags like `make
// build` does), so that `devbrain version` matches the VERSION file — the exact
// contract the bash script tests.
var (
	versionBinOnce sync.Once
	versionBinPath string
	versionBinErr  error
)

func dispatchVersionBin(t testing.TB) string {
	t.Helper()
	versionBinOnce.Do(func() {
		root := clitest.Root(t)
		ver, err := os.ReadFile(filepath.Join(root, "VERSION"))
		if err != nil {
			versionBinErr = err
			return
		}
		dir, err := os.MkdirTemp("", "devbrain-versioned")
		if err != nil {
			versionBinErr = err
			return
		}
		bin := filepath.Join(dir, "devbrain")
		ldflags := "-X github.com/TheWeiHu/devbrain/internal/version.Version=" + strings.TrimSpace(string(ver))
		cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, "./cmd/devbrain")
		cmd.Dir = root
		if out, e := cmd.CombinedOutput(); e != nil {
			versionBinErr = &buildError{msg: string(out), err: e}
			return
		}
		versionBinPath = bin
	})
	if versionBinErr != nil {
		t.Fatalf("dispatchVersionBin: %v", versionBinErr)
	}
	return versionBinPath
}

type buildError struct {
	msg string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.msg }

func TestDevbrainCLI(t *testing.T) {
	h := clitest.New(t)
	run := func(args ...string) clitest.Result { return h.Run(args...) }

	// ── meta subcommands ──────────────────────────────────────────────────────

	t.Run("version matches VERSION file", func(t *testing.T) {
		// Use a version-stamped binary (matching what `make build` / the bash test does).
		want := dispatchReadVersion(t)
		vb := dispatchVersionBin(t)
		data := t.TempDir()
		cmd := exec.Command(vb, "version")
		cmd.Env = append(os.Environ(), "DEVBRAIN_DATA="+data, "DEVBRAIN_PROJECT=testproj")
		out, _ := cmd.Output()
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("version = %q, want %q", got, want)
		}
	})

	t.Run("--version flag works", func(t *testing.T) {
		want := dispatchReadVersion(t)
		vb := dispatchVersionBin(t)
		data := t.TempDir()
		cmd := exec.Command(vb, "--version")
		cmd.Env = append(os.Environ(), "DEVBRAIN_DATA="+data, "DEVBRAIN_PROJECT=testproj")
		out, _ := cmd.Output()
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("--version = %q, want %q", got, want)
		}
	})

	t.Run("help lists subcommands", func(t *testing.T) {
		if !strings.Contains(run("help").Stdout, "devbrain todo") {
			t.Error("help does not mention 'devbrain todo'")
		}
	})

	t.Run("help lists queue subcommand", func(t *testing.T) {
		if !strings.Contains(run("help").Stdout, "devbrain queue") {
			t.Error("help does not mention 'devbrain queue'")
		}
	})

	t.Run("help lists uninstall", func(t *testing.T) {
		if !strings.Contains(run("help").Stdout, "devbrain uninstall") {
			t.Error("help does not mention 'devbrain uninstall'")
		}
	})

	t.Run("no args prints help", func(t *testing.T) {
		r := run()
		if !strings.Contains(r.Stdout, "devbrain todo") {
			t.Error("no-args output does not contain 'devbrain todo'")
		}
	})

	t.Run("queue --help routes to py", func(t *testing.T) {
		r := run("queue", "--help")
		combined := r.Stdout + r.Stderr
		if !strings.Contains(combined, "kanban") {
			t.Errorf("queue --help combined output does not contain 'kanban':\n%s", combined)
		}
	})

	t.Run("unknown command exits 1", func(t *testing.T) {
		if code := run("bogus").Code; code != 1 {
			t.Errorf("unknown command exit code = %d, want 1", code)
		}
	})

	t.Run("nightshift routes to script", func(t *testing.T) {
		r := run("nightshift", "help")
		combined := r.Stdout + r.Stderr
		if !strings.Contains(combined, "autonomous overnight loop") {
			t.Errorf("nightshift help combined output does not contain 'autonomous overnight loop':\n%s", combined)
		}
	})

	// ── devbrain todo routes + exit-code preservation ─────────────────────────

	var taskID string
	t.Run("todo add returns id", func(t *testing.T) {
		taskID = run("todo", "add", "via dispatcher", "-p", "80").Out()
		if taskID == "" {
			t.Fatal("todo add returned empty id")
		}
	})

	t.Run("todo next = the task", func(t *testing.T) {
		if taskID == "" {
			t.Skip("depends on todo-add")
		}
		if got := run("todo", "next").Out(); got != taskID {
			t.Errorf("todo next = %q, want %q", got, taskID)
		}
	})

	t.Run("todo claim -> taken", func(t *testing.T) {
		if taskID == "" {
			t.Skip("depends on todo-add")
		}
		run("todo", "claim", taskID)
		status := dispatchField(run("todo", "show", taskID).Stdout, "status")
		if status != "taken" {
			t.Errorf("status after claim = %q, want taken", status)
		}
	})

	t.Run("todo re-claim exits 2", func(t *testing.T) {
		if taskID == "" {
			t.Skip("depends on todo-add")
		}
		if code := run("todo", "claim", taskID).Code; code != 2 {
			t.Errorf("re-claim exit = %d, want 2 (exact exit code must survive dispatch)", code)
		}
	})

	t.Run("todo done -> done", func(t *testing.T) {
		if taskID == "" {
			t.Skip("depends on todo-add")
		}
		run("todo", "done", taskID)
		status := dispatchField(run("todo", "show", taskID).Stdout, "status")
		if status != "done" {
			t.Errorf("status after done = %q, want done", status)
		}
	})

	// ── devbrain import dry-run ───────────────────────────────────────────────

	t.Run("import dry-run runs", func(t *testing.T) {
		fresh := t.TempDir()
		r := h.RunWith(clitest.RunOpts{}, "import", "--data", fresh)
		// non-zero exit is fine as long as it ran (the script only checks it runs)
		_ = r
	})

	t.Run("import wrote nothing (dry)", func(t *testing.T) {
		fresh := t.TempDir()
		h.RunWith(clitest.RunOpts{}, "import", "--data", fresh)
		mds := clitest.Find(t, fresh, "*.md")
		if len(mds) > 0 {
			t.Errorf("import dry-run wrote %d .md file(s): %v", len(mds), mds)
		}
	})
}

// dispatchReadVersion reads the VERSION file at the repo root.
func dispatchReadVersion(t testing.TB) string {
	t.Helper()
	root := clitest.Root(t)
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// dispatchField extracts the value of a "key: value" line from text.
func dispatchField(text, key string) string {
	for _, ln := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(ln, key+": "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
