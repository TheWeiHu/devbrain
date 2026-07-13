package importer_test

// Incremental (--newer-mtime) sweep gates: history fallback stays off, and a
// fresh subagent transcript makes its stale parent fresh.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func impIncrementalFixture(t *testing.T) (home, data, claude string) {
	t.Helper()
	home = t.TempDir()
	data = filepath.Join(home, "data")
	claude = filepath.Join(home, ".claude")
	os.MkdirAll(filepath.Join(claude, "projects"), 0o755)
	os.MkdirAll(data, 0o755)
	return home, data, claude
}

func impRunIncr(t *testing.T, data, claude string, extra ...string) string {
	t.Helper()
	args := append([]string{"import", "--apply", "--data", data, "--claude", claude,
		"--codex", filepath.Join(claude, "nocodex")}, extra...)
	r := clitest.New(t).RunWith(clitest.RunOpts{}, args...)
	if r.Code != 0 {
		t.Fatalf("import rc=%d:\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	return r.Stdout
}

// The history fallback must not run on an incremental sweep: history.jsonl is
// always fresh (it grows with every prompt), and replaying it would re-import
// old sessions as prompt-only entries, clobbering richer transcript logs.
func TestNewerMtimeSkipsHistoryFallback(t *testing.T) {
	t.Parallel()
	_, data, claude := impIncrementalFixture(t)
	hist, _ := json.Marshal(map[string]any{
		"display": "an old prompt only history remembers", "sessionId": "histsess",
		"project": "/histrepo", "timestamp": 1750000000000,
	})
	clitest.WriteFile(t, filepath.Join(claude, "history.jsonl"), string(hist)+"\n")

	impRunIncr(t, data, claude, "--newer-mtime", "1")
	if hits := clitest.Find(t, filepath.Join(data, "projects"), "*.md"); len(hits) != 0 {
		t.Fatalf("incremental run replayed history.jsonl: %v", hits)
	}

	impRunIncr(t, data, claude) // full run still uses the fallback
	if hits := clitest.Find(t, filepath.Join(data, "projects"), "*.md"); len(hits) == 0 {
		t.Fatal("full run should import the history-only session")
	}
}

// A parent transcript older than the cursor must still be parsed when one of
// its subagent transcripts is fresh — subagent tokens are only discovered
// through the parent.
func TestNewerMtimeSubagentMakesParentFresh(t *testing.T) {
	t.Parallel()
	_, data, claude := impIncrementalFixture(t)
	pdir := filepath.Join(claude, "projects", "proj")
	os.MkdirAll(pdir, 0o755)
	parent := filepath.Join(pdir, "parentsess.jsonl")
	clitest.WriteFile(t, parent, strings.Join([]string{
		impUserLine("2026-07-14T10:00:00.000Z", "/repo", "parent prompt", false),
		impAssistantLine("2026-07-14T10:00:01.000Z", "/repo", "m1", "claude-opus-4-8",
			map[string]int{"input_tokens": 10, "output_tokens": 5}, []impContentBlock{{"type": "text", "text": "answer"}}),
	}, "\n")+"\n")
	sub := filepath.Join(pdir, "parentsess", "subagents", "agent-a1.jsonl")
	os.MkdirAll(filepath.Dir(sub), 0o755)
	clitest.WriteFile(t, sub, strings.Join([]string{
		impUserLine("2026-07-14T10:00:01.500Z", "/repo", "scan the repo", true),
		impAssistantLine("2026-07-14T10:00:02.000Z", "/repo", "m2", "claude-haiku-4-5",
			map[string]int{"input_tokens": 7, "output_tokens": 3}, []impContentBlock{{"type": "text", "text": "sub answer"}}),
	}, "\n")+"\n")

	// Parent is stale (behind the cursor); the subagent transcript is fresh.
	cursor := time.Now().Add(time.Hour)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(parent, old, old)
	future := cursor.Add(time.Hour)
	os.Chtimes(sub, future, future)

	impRunIncr(t, data, claude, "--newer-mtime", strconv.FormatInt(cursor.Unix(), 10))
	tok := filepath.Join(data, "projects", "miscellaneous", "tokens.jsonl")
	b, err := os.ReadFile(tok)
	if err != nil {
		t.Fatalf("tokens.jsonl not written: %v", err)
	}
	if !strings.Contains(string(b), "claude-haiku-4-5") {
		t.Fatalf("subagent tokens missing:\n%s", b)
	}
}
