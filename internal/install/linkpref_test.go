package install_test

// Go-native port of scripts/test-link-preferences.sh: wires a throwaway
// ~/.claude/CLAUDE.md to import a global preferences page, all offline.
// Exercises: wire into fresh file, idempotency, preserve existing content, unlink.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestLinkPreferences(t *testing.T) {
	h := clitest.New(t)

	// One temp HOME/DATA for every assertion — mirrors the shell script.
	tmp := t.TempDir()
	dataDir := filepath.Join(tmp, "data")
	claudeDir := filepath.Join(tmp, "claude")
	mem := filepath.Join(claudeDir, "CLAUDE.md")

	// workDir outside the repo so git-gate skips.
	workDir := t.TempDir()

	// Shared env for every invocation.
	env := func(extra ...string) map[string]string {
		m := map[string]string{
			"HOME":              tmp,
			"DEVBRAIN_DATA":     dataDir,
			"CLAUDE_CONFIG_DIR": claudeDir,
			// Clear harness defaults.
			"DEVBRAIN_PROJECT": "",
		}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i]] = extra[i+1]
		}
		return m
	}

	run := func(args ...string) clitest.Result {
		return h.RunWith(clitest.RunOpts{Dir: workDir, Env: env()}, args...)
	}

	// ── wire into a fresh (nonexistent) user memory ───────────────────────────
	run("link-preferences")

	t.Run("creates user memory", func(t *testing.T) {
		if _, err := os.Stat(mem); err != nil {
			t.Fatalf("CLAUDE.md not created: %v", err)
		}
	})

	t.Run("adds the import line", func(t *testing.T) {
		got := clitest.Read(t, mem)
		if !strings.Contains(got, "/preferences/global.md") {
			t.Errorf("CLAUDE.md missing preferences import:\n%s", got)
		}
	})

	t.Run("import uses @ syntax", func(t *testing.T) {
		got := clitest.Read(t, mem)
		if !instHasImportLine(got, "/preferences/global.md") {
			t.Errorf("CLAUDE.md missing @ import line:\n%s", got)
		}
	})

	t.Run("adds the managed marker", func(t *testing.T) {
		got := clitest.Read(t, mem)
		if !strings.Contains(got, "devbrain: global preferences") {
			t.Errorf("CLAUDE.md missing managed marker:\n%s", got)
		}
	})

	// ── idempotent: re-run adds nothing ───────────────────────────────────────
	run("link-preferences")

	t.Run("single import after re-run", func(t *testing.T) {
		got := clitest.Read(t, mem)
		count := strings.Count(got, "/preferences/global.md")
		if count != 1 {
			t.Errorf("import line appears %d times after re-run, want 1:\n%s", count, got)
		}
	})

	// ── preserves existing user-memory content ────────────────────────────────
	clitest.WriteFile(t, mem, "# My rules\n\nKeep this line.\n")
	run("link-preferences")

	t.Run("preserves hand-written memory", func(t *testing.T) {
		got := clitest.Read(t, mem)
		if !strings.Contains(got, "Keep this line.") {
			t.Errorf("hand-written content was lost:\n%s", got)
		}
	})

	t.Run("appends import after existing content", func(t *testing.T) {
		got := clitest.Read(t, mem)
		if !strings.Contains(got, "/preferences/global.md") {
			t.Errorf("import line missing after writing existing content:\n%s", got)
		}
	})

	// ── unlink removes the managed lines, keeps the rest ─────────────────────
	h.RunWith(clitest.RunOpts{Dir: workDir, Env: env()}, "link-preferences", "--unlink")

	t.Run("unlink drops the import", func(t *testing.T) {
		got := clitest.Read(t, mem)
		if strings.Contains(got, "/preferences/global.md") {
			t.Errorf("import line survived unlink:\n%s", got)
		}
	})

	t.Run("unlink drops the marker", func(t *testing.T) {
		got := clitest.Read(t, mem)
		if strings.Contains(got, "devbrain: global preferences") {
			t.Errorf("managed marker survived unlink:\n%s", got)
		}
	})

	t.Run("unlink keeps user content", func(t *testing.T) {
		got := clitest.Read(t, mem)
		if !strings.Contains(got, "Keep this line.") {
			t.Errorf("user content lost after unlink:\n%s", got)
		}
	})

	// ── unlink that empties the file must NOT leave it wired ─────────────────
	// Start fresh: only the 2 managed lines.
	if err := os.Remove(mem); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	run("link-preferences") // file = only the 2 managed lines
	h.RunWith(clitest.RunOpts{Dir: workDir, Env: env()}, "link-preferences", "--unlink")

	t.Run("unlink fully clears managed-only file", func(t *testing.T) {
		// File may be absent or empty; either way the import must not be present.
		b, err := os.ReadFile(mem)
		if err != nil {
			return // file gone — that's fine
		}
		if strings.Contains(string(b), "/preferences/global.md") {
			t.Errorf("import line survived unlink of managed-only file:\n%s", b)
		}
	})
}
