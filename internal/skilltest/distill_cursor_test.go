// Package skilltest golden-tests the deterministic bash inside the LLM-executed
// SKILL.md protocols. /distill and /continue are markdown, not Go, but their
// highest-churn core — the ledger-cursor "which files are new" computation — is
// pure shell with subtle gotchas (em-dash-safe ledger parsing, sort-based
// timestamp compare, cksum memory detection). This runs that block VERBATIM from
// the SKILL against a fixed fixture and diffs stdout, so the protocol can be
// refactored without silently regressing the cursor logic. /continue's fold-in
// defers to this same block, so covering it covers both skills.
package skilltest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoPath(t *testing.T, rel string) string {
	t.Helper()
	return filepath.Join("..", "..", rel)
}

// extractMarkedBlock returns the ```bash fenced block that follows the HTML
// comment line containing marker, so the test runs the SKILL's real text.
func extractMarkedBlock(t *testing.T, mdPath, marker string) string {
	t.Helper()
	src, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	i := 0
	for i < len(lines) && !strings.Contains(lines[i], marker) {
		i++
	}
	if i == len(lines) {
		t.Fatalf("marker %q not found in %s", marker, mdPath)
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) != "```bash" {
		i++
	}
	if i == len(lines) {
		t.Fatalf("no ```bash block after marker %q in %s", marker, mdPath)
	}
	i++ // past ```bash
	var body []string
	for i < len(lines) && strings.TrimSpace(lines[i]) != "```" {
		body = append(body, lines[i])
		i++
	}
	if i == len(lines) {
		t.Fatalf("unterminated ```bash block after marker %q in %s", marker, mdPath)
	}
	return strings.Join(body, "\n")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDistillCursorGolden pins /distill Step 2. The fixture exercises every
// branch of the cursor logic: newer-than-cursor (new), equal (skip),
// earlier (skip), no-cursor (new), non-UTF8 content (new), plus cksum memory
// match (skip) vs changed/new (fold), plus a nested memory file sharing a
// top-level basename. The ledger uses a real em-dash to prove the parsing keys
// off the filename, not a naive split on `—`.
func TestDistillCursorGolden(t *testing.T) {
	t.Parallel()
	script := extractMarkedBlock(t, repoPath(t, "assets/skills/distill/SKILL.md"), "golden:cursor-detect")

	dir := t.TempDir()
	logdir := filepath.Join(dir, "log")
	memdir := filepath.Join(dir, "memory")
	ledger := filepath.Join(dir, "distilled.md")

	// log files: rel path → newest `## HH:MM:SS` entry
	write(t, filepath.Join(logdir, "2026-07-01/newer.md"), "## 08:00:00\nfirst\n\n## 09:30:00\nlater\n")
	write(t, filepath.Join(logdir, "2026-07-01/equal.md"), "## 10:00:00\nonly\n")
	write(t, filepath.Join(logdir, "2026-07-02/earlier.md"), "## 05:00:00\nonly\n")
	write(t, filepath.Join(logdir, "2026-07-02/fresh.md"), "## 07:15:00\nbrand new\n")
	// NUL byte makes grep classify the file as binary and print "Binary file … matches"
	// instead of the timestamps, mangling both `newest` and `rec` — hence `grep -a`.
	write(t, filepath.Join(logdir, "2026-07-02/binary.md"), "## 06:00:00\nraw \x00\xff bytes\n\n## 11:45:00\nlater\n")

	// memory files — cksum is deterministic (CRC), so the golden is stable.
	write(t, filepath.Join(memdir, "kept.md"), "unchanged memory\n")
	write(t, filepath.Join(memdir, "changed.md"), "edited memory\n")
	write(t, filepath.Join(memdir, "brandnew.md"), "never folded\n")
	write(t, filepath.Join(memdir, "MEMORY.md"), "index — must be ignored\n")
	// same basename as a top-level file: keys must be $MEMDIR-relative paths, or
	// this inherits kept.md's cursor and is never folded.
	write(t, filepath.Join(memdir, "nested/kept.md"), "different content, same basename\n")

	// Ledger: em-dash lines. newer is behind (→ new), equal matches (→ skip),
	// earlier is ahead (→ skip), fresh absent (→ new). kept cksum matches its
	// content; changed cksum is stale; brandnew absent.
	keptCksum := cksum(t, filepath.Join(memdir, "kept.md"))
	write(t, ledger, "# distilled — /distill cursor\n\n"+
		"- 2026-07-01/newer.md — through 08:00:00\n"+
		"- 2026-07-01/equal.md — through 10:00:00\n"+
		"- 2026-07-02/earlier.md — through 06:00:00\n"+
		"- 2026-07-02/binary.md — through 06:00:00\n"+
		"- memory/kept.md — cksum "+keptCksum+"\n"+
		"- memory/changed.md — cksum 1\n")

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"LOGDIR="+logdir, "LEDGER="+ledger, "MEMDIR="+memdir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running cursor block: %v\n%s", err, out)
	}

	goldPath := repoPath(t, "testdata/golden/distill-cursor.txt")
	// Refresh the golden after a deliberate SKILL edit: DEVBRAIN_GEN_GOLDEN=1 go test ./internal/skilltest/
	if os.Getenv("DEVBRAIN_GEN_GOLDEN") != "" {
		if err := os.WriteFile(goldPath, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != string(want) {
		t.Errorf("cursor output mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func cksum(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("cksum", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(out))[0]
}
