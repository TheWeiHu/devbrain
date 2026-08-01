package brain_test

// Go-native port of scripts/test-brain.sh: the brain CLI's black-box contract
// (offline fallback plus deterministic engine-backed routing), driven through
// the built binary via the shared clitest harness.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestBrainCLI(t *testing.T) {
	h := clitest.New(t)
	h.Project = "owner__alpha"

	// Seed two projects' brain pages on disk (the source of truth gbrain indexes FROM).
	alphaDir := filepath.Join(h.Data, "projects", "owner__alpha", "brain")
	betaDir := filepath.Join(h.Data, "projects", "owner__beta", "brain")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	clitest.WriteFile(t, filepath.Join(alphaDir, "install.md"),
		"# Install\nHow to install the widget and configure the daemon.\n")
	clitest.WriteFile(t, filepath.Join(alphaDir, "concurrency.md"),
		"# Concurrency\nThe daemon uses a lockfile to avoid races.\n")
	clitest.WriteFile(t, filepath.Join(betaDir, "install.md"),
		"# Install\nBeta install notes — totally different widget.\n")

	// Force gbrain off PATH by pointing DEVBRAIN_GBRAIN at a nonexistent name so
	// exec.LookPath("gbrain-offline-stub-not-a-real-binary") always fails.
	h.Env["DEVBRAIN_GBRAIN"] = "gbrain-offline-stub-not-a-real-binary"

	// Verify that our stub name is genuinely absent so the offline path triggers.
	if _, err := exec.LookPath("gbrain-offline-stub-not-a-real-binary"); err == nil {
		t.Skip("skip: could not remove gbrain from PATH for offline test")
	}

	b := func(args ...string) clitest.Result {
		return h.Run(append([]string{"brain"}, args...)...)
	}

	t.Run("offline search", func(t *testing.T) {
		t.Run("finds matching page", func(t *testing.T) {
			out := b("search", "daemon").Stdout
			if !strings.Contains(out, "owner__alpha/concurrency") {
				t.Errorf("search daemon: want owner__alpha/concurrency in output\n%s", out)
			}
		})

		t.Run("ranks by term coverage", func(t *testing.T) {
			out := b("search", "daemon", "lockfile", "races").Stdout
			first := strings.SplitN(out, "\n", 2)[0]
			if !strings.Contains(first, "owner__alpha/concurrency") {
				t.Errorf("first hit not owner__alpha/concurrency:\n%s", first)
			}
		})

		t.Run("output is gbrain-shaped", func(t *testing.T) {
			out := b("search", "install").Stdout
			first := strings.SplitN(out, "\n", 2)[0]
			// Pattern: [N.NNNN] owner__(alpha|beta)/install --
			if !strings.Contains(first, "/install -- ") || !strings.Contains(first, "owner__") {
				t.Errorf("first line not gbrain-shaped: %q", first)
			}
			// Must start with [digit.digit]
			if !strings.HasPrefix(first, "[") {
				t.Errorf("first line missing [ prefix: %q", first)
			}
		})

		t.Run("no match -> No results", func(t *testing.T) {
			out := b("search", "zzzznotapage").Stdout
			if !strings.Contains(out, "No results.") {
				t.Errorf("want 'No results.' for unknown term, got:\n%s", out)
			}
		})

		t.Run(">20 hits -> no false No results", func(t *testing.T) {
			// Add 30 pages all containing "daemon"
			manyDir := filepath.Join(h.Data, "projects", "owner__many", "brain")
			if err := os.MkdirAll(manyDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 30; i++ {
				clitest.WriteFile(t, filepath.Join(manyDir, fmt.Sprintf("page%d.md", i)),
					fmt.Sprintf("# P%d\nthe daemon widget appears here too\n", i))
			}
			out := b("search", "daemon", "--global").Stdout
			if strings.Contains(out, "No results.") {
				t.Errorf(">20 hits triggered false 'No results.':\n%s", out)
			}
		})

		t.Run(">20 hits -> capped at 20 lines", func(t *testing.T) {
			out := b("search", "widget", "--global").Stdout
			count := 0
			for _, ln := range strings.Split(out, "\n") {
				if strings.HasPrefix(ln, "[") {
					count++
				}
			}
			if count != 20 {
				t.Errorf(">20-hit search: got %d result lines, want 20\n%s", count, out)
			}
		})

		t.Run("search spans projects", func(t *testing.T) {
			out := b("search", "install", "--global").Stdout
			if !strings.Contains(out, "owner__alpha/install") {
				t.Errorf("missing owner__alpha/install in:\n%s", out)
			}
			if !strings.Contains(out, "owner__beta/install") {
				t.Errorf("missing owner__beta/install in:\n%s", out)
			}
		})

		t.Run("current project ranks before cross-project results", func(t *testing.T) {
			out := b("search", "install").Stdout
			first := strings.SplitN(out, "\n", 2)[0]
			if !strings.Contains(first, "owner__alpha/install") {
				t.Errorf("first hit not current project:\n%s", out)
			}
			if !strings.Contains(out, "owner__beta/install") {
				t.Errorf("cross-project tail missing:\n%s", out)
			}
		})
	})

	t.Run("offline get", func(t *testing.T) {
		t.Run("exact slug reads page", func(t *testing.T) {
			out := b("get", "owner__alpha/concurrency").Stdout
			if !strings.Contains(out, "lockfile") {
				t.Errorf("get exact slug: want 'lockfile' in output\n%s", out)
			}
		})

		t.Run("missing slug -> page_not_found", func(t *testing.T) {
			out := b("get", "owner__alpha/nope").Stdout
			if !strings.Contains(out, "page_not_found") {
				t.Errorf("get missing slug: want 'page_not_found' in output\n%s", out)
			}
		})

		t.Run("fuzzy unique basename resolves", func(t *testing.T) {
			// only alpha has concurrency
			out := b("get", "concurrency", "--fuzzy").Stdout
			if !strings.Contains(out, "lockfile") {
				t.Errorf("fuzzy get concurrency: want 'lockfile' in output\n%s", out)
			}
		})

		t.Run("fuzzy ambiguous -> Did you mean", func(t *testing.T) {
			// both alpha and beta have install.md
			out := b("get", "install", "--fuzzy").Stdout
			if !strings.Contains(out, "Did you mean") {
				t.Errorf("fuzzy ambiguous: want 'Did you mean' in output\n%s", out)
			}
			if !strings.Contains(out, "owner__beta/install") {
				t.Errorf("fuzzy ambiguous: want 'owner__beta/install' in output\n%s", out)
			}
		})
	})

	t.Run("list and index no-ops", func(t *testing.T) {
		t.Run("list emits slugs", func(t *testing.T) {
			out := b("list").Stdout
			if !strings.Contains(out, "owner__alpha/install") {
				t.Errorf("list: want owner__alpha/install in output\n%s", out)
			}
		})

		t.Run("put is a clean no-op offline", func(t *testing.T) {
			r := h.RunWith(clitest.RunOpts{Stdin: ""}, "brain", "put", "owner__alpha/install")
			if r.Code != 0 {
				t.Errorf("put offline: exit %d (want 0)\nstderr: %s", r.Code, r.Stderr)
			}
		})
	})

	t.Run("gbrain-backed project-first search", func(t *testing.T) {
		h2 := clitest.New(t)
		h2.Project = "owner__alpha"
		bin := t.TempDir()
		stub := filepath.Join(bin, "gbrain-stub")
		clitest.WriteExec(t, stub, `#!/bin/sh
case "$1" in
  search|query|ask)
	case "$*" in
	  *no-local*)
	    printf '%s\n' \
	      '[0.9900] projects/owner__beta/brain/guide -- beta curated' \
	      '[0.9800] projects/owner__gamma/brain/guide -- gamma curated' \
	      '[0.9700] projects/owner__delta/brain/guide -- delta curated'
	    ;;
	  *unstructured*)
	    echo 'engine warning without result records'
	    exit 7
	    ;;
	  *)
	    printf '%s\n' \
	      '[0.9900] projects/owner__beta/todo/beta -- beta curated' \
	      '[0.9800] projects/owner__alpha/log/2026-01-01/session -- alpha raw log' \
	      '[0.9700] projects/owner__alpha/brain/guide -- alpha curated' \
	      '   > alpha continuation detail' \
	      '[0.9600] projects/owner__alpha/brain/guide -- duplicate alpha chunk' \
	      '[0.9500] projects/owner__gamma/brain/guide -- gamma curated' \
	      '[0.9400] projects/owner__delta/brain/guide -- delta curated'
	    ;;
	esac
    ;;
  *) printf 'stub:%s\n' "$*" ;;
esac
`)
		h2.Env["DEVBRAIN_GBRAIN"] = stub

		out := h2.Run("brain", "search", "guide").Stdout
		lines := resultLines(out)
		if len(lines) != 4 {
			t.Fatalf("project-first result count = %d, want 4:\n%s", len(lines), out)
		}
		if !strings.Contains(lines[0], "owner__alpha/brain/guide") {
			t.Errorf("curated local hit not first:\n%s", out)
		}
		if !strings.Contains(lines[1], "owner__alpha/log/") {
			t.Errorf("local log not after curated local hit:\n%s", out)
		}
		if strings.Count(out, "owner__alpha/brain/guide") != 1 {
			t.Errorf("duplicate slug was not collapsed:\n%s", out)
		}
		if !strings.Contains(out, "alpha continuation detail") {
			t.Errorf("multiline result detail was dropped:\n%s", out)
		}
		if strings.Contains(out, "owner__delta/") {
			t.Errorf("more than two cross-project hits survived:\n%s", out)
		}

		limited := h2.Run("brain", "search", "guide", "--limit", "1").Stdout
		if lines := resultLines(limited); len(lines) != 1 || !strings.Contains(lines[0], "owner__alpha/") {
			t.Errorf("--limit 1 did not reserve the result for the current project:\n%s", limited)
		}

		noLocal := h2.Run("brain", "search", "no-local").Stdout
		if lines := resultLines(noLocal); len(lines) != 2 {
			t.Errorf("no-local search returned %d cross-project results, want 2:\n%s", len(lines), noLocal)
		}

		query := h2.Run("brain", "query", "guide").Stdout
		if first := resultLines(query)[0]; !strings.Contains(first, "owner__alpha/") {
			t.Errorf("query alias did not use project-first routing:\n%s", query)
		}

		global := h2.Run("brain", "search", "guide", "--global").Stdout
		if first := resultLines(global)[0]; !strings.Contains(first, "owner__beta/") {
			t.Errorf("--global did not preserve engine order:\n%s", global)
		}

		other := h2.Run("brain", "list").Stdout
		if !strings.Contains(other, "stub:list") {
			t.Errorf("non-retrieval command did not pass through: %q", other)
		}

		failed := h2.Run("brain", "search", "unstructured")
		if failed.Code != 7 || !strings.Contains(failed.Stdout, "engine warning") {
			t.Errorf("unstructured engine failure was not preserved: code=%d stdout=%q", failed.Code, failed.Stdout)
		}
	})
}

func resultLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "[") {
			lines = append(lines, line)
		}
	}
	return lines
}
