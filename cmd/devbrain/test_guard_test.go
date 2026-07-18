package main

// Drives scripts/test-guard.sh inside a throwaway repo with a STUB `go`, checking
// that the git sandbox is exported to the suite and that the config canary catches
// a test escaping its tempdir instead of silently bricking the real repo.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestTestGuard(t *testing.T) {
	guard := filepath.Join(clitest.Root(t), "scripts", "test-guard.sh")
	if _, err := os.Stat(guard); err != nil {
		t.Skipf("test-guard.sh not found: %v", err)
	}

	// run the guard in a throwaway repo with `go` stubbed by the given script.
	run := func(t *testing.T, stub string) (int, string, string) {
		t.Helper()
		tmp := t.TempDir()
		repo := filepath.Join(tmp, "repo")
		clitest.Git(t, "", "init", "-q", repo)

		fakeGo := filepath.Join(tmp, "go")
		clitest.WriteFile(t, fakeGo, "#!/bin/sh\n"+stub+"\n")
		if err := os.Chmod(fakeGo, 0o755); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command("sh", guard)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GO="+fakeGo)
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("guard: %v\n%s", err, out)
			}
			code = ee.ExitCode()
		}
		cfg := filepath.Join(repo, ".git", "config")
		return code, string(out), cfg
	}

	t.Run("clean suite passes and sees the sandbox", func(t *testing.T) {
		// the stub asserts the sandbox env reached the suite process.
		code, out, _ := run(t, `[ "$GIT_CONFIG_SYSTEM" = /dev/null ] || { echo "no GIT_CONFIG_SYSTEM"; exit 9; }
case "$GIT_CONFIG_GLOBAL" in ?*) ;; *) echo "no GIT_CONFIG_GLOBAL"; exit 9;; esac
case "$GIT_CEILING_DIRECTORIES" in ?*) ;; *) echo "no GIT_CEILING_DIRECTORIES"; exit 9;; esac
exit 0`)
		if code != 0 {
			t.Errorf("clean run: code=%d, want 0\n%s", code, out)
		}
	})

	t.Run("suite failure propagates", func(t *testing.T) {
		if code, out, _ := run(t, `[ "$1" = vet ] && exit 0; exit 3`); code != 3 {
			t.Errorf("failing suite: code=%d, want 3\n%s", code, out)
		}
	})

	t.Run("canary catches a config escape", func(t *testing.T) {
		// a test that forgets `-C <tmpdir>` — git resolves to the repo it runs in.
		code, out, cfg := run(t, `[ "$1" = vet ] && exit 0
git config core.bare true
exit 0`)
		if code == 0 {
			t.Errorf("escaped config write must fail the run\n%s", out)
		}
		if !strings.Contains(out, "escaped its sandbox") {
			t.Errorf("canary must name the escape, got:\n%s", out)
		}
		if _, err := os.Stat(cfg + ".canary-mutated"); err != nil {
			t.Errorf("canary must keep the mutated config for diagnosis: %v", err)
		}
		// The repo must be left USABLE: the mutation rolled back, not kept live.
		if b, err := os.ReadFile(cfg); err != nil || strings.Contains(string(b), "bare = true") {
			t.Errorf("canary must restore the pre-suite config (err=%v):\n%s", err, b)
		}
	})

	t.Run("global config writes land in the sandbox", func(t *testing.T) {
		code, out, _ := run(t, `[ "$1" = vet ] && exit 0
git config --global user.name escapee
exit 0`)
		if code != 0 {
			t.Errorf("a --global write must be sandboxed, not flagged: code=%d\n%s", code, out)
		}
	})
}
