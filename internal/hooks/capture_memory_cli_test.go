package hooks_test

// Go-native port of scripts/test-capture-memory.sh: SessionEnd / hook memory
// tests — mirroring, redaction, idempotency, and re-mirror on change.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestCaptureMemory(t *testing.T) {
	h := clitest.New(t)
	workdir := t.TempDir()

	// Build a fake ~/.claude/projects/<slug>/ tree with a sibling memory/ dir.
	projslug := filepath.Join(workdir, "projects", "-some-slug")
	memdir := filepath.Join(projslug, "memory")
	if err := os.MkdirAll(memdir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(projslug, "session.jsonl")
	clitest.WriteFile(t, transcript, `{"type":"user","message":{"content":"hi"}}`+"\n")
	clitest.WriteFile(t, filepath.Join(memdir, "MEMORY.md"),
		"# Memory Index\n- [staging](reference_staging.md) — staging box\n")
	clitest.WriteFile(t, filepath.Join(memdir, "reference_staging.md"),
		"---\nname: staging-box\ntype: reference\n---\nStaging at 18.211.217.170. Token sk-abcdefghijklmnopqrstuvwxyz0123 must not leak.\n")

	memPayload := func(tp string) string {
		b, _ := json.Marshal(map[string]any{
			"transcript_path": tp,
			"cwd":             workdir,
		})
		return string(b)
	}

	dest := filepath.Join(h.Data, "projects", h.Project, "memory")
	runMemory := func(tp string) {
		t.Helper()
		h.RunWith(clitest.RunOpts{Stdin: memPayload(tp)}, "hook", "memory")
	}

	// Guard 1: no memory dir -> no-op (transcript path with no sibling memory/).
	runMemory(filepath.Join(workdir, "none", "session.jsonl"))
	if _, err := os.Stat(dest); err == nil {
		t.Error("no-op when no memory dir: dest was created unexpectedly")
	}

	// Real run.
	runMemory(transcript)
	if _, err := os.Stat(filepath.Join(dest, "reference_staging.md")); err != nil {
		t.Errorf("mirrors memory files: reference_staging.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "MEMORY.md")); err != nil {
		t.Errorf("mirrors memory files: MEMORY.md missing: %v", err)
	}
	stagingContent := clitest.Read(t, filepath.Join(dest, "reference_staging.md"))
	if !strings.Contains(stagingContent, "Staging at 18.211.217.170") {
		t.Error("preserves frontmatter/body: 'Staging at 18.211.217.170' missing")
	}
	if !strings.Contains(stagingContent, "REDACTED") || strings.Contains(stagingContent, "sk-abcdefghijklmnopqrstuvwxyz0123") {
		t.Error("redacts secret in memory: REDACTED missing or raw secret still present")
	}

	// Idempotency: a second run with no source change rewrites nothing (mtime stable).
	st1, err := os.Stat(filepath.Join(dest, "reference_staging.md"))
	if err != nil {
		t.Fatal(err)
	}
	before := st1.ModTime()
	time.Sleep(20 * time.Millisecond)
	runMemory(transcript)
	st2, _ := os.Stat(filepath.Join(dest, "reference_staging.md"))
	after := st2.ModTime()
	if !before.Equal(after) {
		t.Error("idempotent (unchanged file not rewritten): mtime changed on second run")
	}

	// A changed source file gets re-mirrored.
	f, err := os.OpenFile(filepath.Join(memdir, "reference_staging.md"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\nNew fact added.\n")
	f.Close()
	runMemory(transcript)
	updated := clitest.Read(t, filepath.Join(dest, "reference_staging.md"))
	if !strings.Contains(updated, "New fact added.") {
		t.Error("re-mirrors changed file: 'New fact added.' missing")
	}
}
