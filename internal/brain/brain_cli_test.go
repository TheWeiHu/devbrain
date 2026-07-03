package brain_test

// Go-native port of scripts/test-brain.sh: the brain CLI's black-box contract
// (offline fallback with gbrain forced off PATH, plus passthrough smoke-check
// when gbrain is actually installed), driven through the built binary via the
// shared clitest harness.

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
			out := b("search", "daemon").Stdout
			if strings.Contains(out, "No results.") {
				t.Errorf(">20 hits triggered false 'No results.':\n%s", out)
			}
		})

		t.Run(">20 hits -> capped at 20 lines", func(t *testing.T) {
			out := b("search", "widget").Stdout
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
			out := b("search", "install").Stdout
			if !strings.Contains(out, "owner__alpha/install") {
				t.Errorf("missing owner__alpha/install in:\n%s", out)
			}
			if !strings.Contains(out, "owner__beta/install") {
				t.Errorf("missing owner__beta/install in:\n%s", out)
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

	t.Run("passthrough when gbrain present", func(t *testing.T) {
		// Re-use a fresh harness without the DEVBRAIN_GBRAIN stub so real gbrain
		// can be resolved from PATH.
		if _, err := exec.LookPath("gbrain"); err != nil {
			t.Skip("skip — gbrain not installed, passthrough path not exercised")
		}
		h2 := clitest.New(t)
		// Retry up to 5 times (real brain is one PGLite DB; concurrent lock is transient).
		ok := false
		for i := 0; i < 5; i++ {
			r := h2.Run("brain", "list")
			if r.Code == 0 {
				ok = true
				break
			}
		}
		if !ok {
			t.Error("passthrough: gbrain handles the call — all 5 retries failed")
		}
	})
}
