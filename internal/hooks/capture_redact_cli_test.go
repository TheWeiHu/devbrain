package hooks_test

// Go-native port of scripts/test-capture-redact.sh: smoke tests for secret
// redaction in `devbrain hook capture`. Each shape must be redacted (no raw
// token in the log) and prose must pass through untouched.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// secretRe matches the raw secret patterns that must NOT appear in the log.
var secretRe = regexp.MustCompile(
	`sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_|AKIA[0-9A-Z]{16}|xox[baprs]-[0-9]|Bearer [A-Za-z0-9._-]{16,}`,
)

// runCaptureRedact feeds one prompt through hook capture and returns the log content.
func runCaptureRedact(t *testing.T, h *clitest.Harness, workdir, prompt string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"prompt":     prompt,
		"cwd":        workdir,
		"session_id": "testsess",
	})
	h.RunWith(clitest.RunOpts{Stdin: string(b)}, "hook", "capture")
	// Find the written log file.
	files := clitest.Find(t, h.Data, "*.md")
	if len(files) == 0 {
		return ""
	}
	return clitest.Read(t, files[0])
}

func TestCaptureRedact(t *testing.T) {
	secrets := []string{
		"my key is sk-abcdefghijklmnopqrstuvwxyz0123456789",
		"token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		"github_pat_11ABCDEFG0123456789_abcdefghijklmnop",
		"aws AKIAIOSFODNN7EXAMPLE here",
		"slack xoxb-1234567890-abcdefghij",
		"Authorization: Bearer abcdef1234567890ghijkl",
	}

	for i, s := range secrets {
		s := s // capture
		t.Run(fmt.Sprintf("secret_%d", i), func(t *testing.T) {
			// Fresh harness per secret so each test is isolated.
			// Use a workdir that does NOT embed the secret in its path.
			h := clitest.New(t)
			// h.Data is already a temp dir; create a subdir whose name is neutral.
			workdir := filepath.Join(h.Data, "cwd")
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				t.Fatal(err)
			}
			out := runCaptureRedact(t, h, workdir, s)
			if secretRe.MatchString(out) {
				t.Errorf("secret leaked into log for: %q\nlog: %s", s, out)
			} else if !strings.Contains(out, "[REDACTED]") {
				t.Errorf("no redaction marker for: %q\nlog: %s", s, out)
			}
		})
	}

	t.Run("prose untouched", func(t *testing.T) {
		h := clitest.New(t)
		workdir := filepath.Join(h.Data, "cwd")
		if err := os.MkdirAll(workdir, 0o755); err != nil {
			t.Fatal(err)
		}
		out := runCaptureRedact(t, h, workdir, "please refactor the parser and add tests")
		if !strings.Contains(out, "refactor the parser and add tests") || strings.Contains(out, "REDACTED") {
			t.Errorf("prose was altered:\n%s", out)
		}
	})
}

