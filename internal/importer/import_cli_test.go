package importer_test

// Go-native port of scripts/test-import.sh: drives `devbrain import` as a
// black-box subprocess and asserts on stdout, exit-code, and on-disk state.
// Fixture transcripts are built with encoding/json — no python3 required.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// ── JSON transcript builders ──────────────────────────────────────────────────

// impUserLine returns one Claude transcript JSONL line: a user event.
func impUserLine(ts, cwd, content string, isSidechain bool) string {
	m := map[string]any{
		"type":        "user",
		"isSidechain": isSidechain,
		"cwd":         cwd,
		"message":     map[string]any{"content": content},
	}
	if ts != "" {
		m["timestamp"] = ts
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// impAssistantLine returns one Claude transcript JSONL line: an assistant event.
type impContentBlock map[string]any

func impAssistantLine(ts, cwd, id, model string, usage map[string]int, content []impContentBlock) string {
	m := map[string]any{
		"type": "assistant",
		"cwd":  cwd,
	}
	if ts != "" {
		m["timestamp"] = ts
	}
	msg := map[string]any{
		"content": content,
	}
	if id != "" {
		msg["id"] = id
	}
	if model != "" {
		msg["model"] = model
	}
	if usage != nil {
		msg["usage"] = map[string]any{
			"input_tokens":               usage["input_tokens"],
			"output_tokens":              usage["output_tokens"],
			"cache_creation_input_tokens": usage["cache_creation_input_tokens"],
			"cache_read_input_tokens":    usage["cache_read_input_tokens"],
		}
	}
	m["message"] = msg
	b, _ := json.Marshal(m)
	return string(b)
}

func impTextBlock(text string) impContentBlock {
	return impContentBlock{"type": "text", "text": text}
}

func impToolBlock(name, filePath string) impContentBlock {
	b := impContentBlock{"type": "tool_use", "name": name}
	if filePath != "" {
		b["input"] = map[string]any{"file_path": filePath}
	}
	return b
}

func impThinkBlock(thinking string) impContentBlock {
	return impContentBlock{"type": "thinking", "thinking": thinking}
}

// ── Codex transcript builders ─────────────────────────────────────────────────

func impCodexLine(obj map[string]any) string {
	b, _ := json.Marshal(obj)
	return string(b)
}

// ── file helpers ──────────────────────────────────────────────────────────────

// impWriteLines writes lines joined by newlines + trailing newline.
func impWriteLines(t *testing.T, path string, lines []string) {
	t.Helper()
	clitest.WriteFile(t, path, strings.Join(lines, "\n")+"\n")
}

// impFindMD finds the first *.md file under a directory tree.
func impFindMD(t *testing.T, root string) string {
	t.Helper()
	hits := clitest.Find(t, root, "*.md")
	if len(hits) == 0 {
		return ""
	}
	return hits[0]
}

// impCountLines counts non-empty lines that match a substring.
func impCountLines(text, sub string) int {
	n := 0
	for _, ln := range strings.Split(text, "\n") {
		if strings.Contains(ln, sub) {
			n++
		}
	}
	return n
}

// impLinesTotal counts all non-blank lines in a file.
func impLinesTotal(t *testing.T, path string) int {
	t.Helper()
	n := 0
	for _, ln := range strings.Split(clitest.Read(t, path), "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}

// impFileLinesCount returns the number of actual (including blank) lines in a file
// matching how `wc -l` counts them (newline-terminated records).
func impFileNewlineCount(t *testing.T, path string) int {
	t.Helper()
	content := clitest.Read(t, path)
	return strings.Count(content, "\n")
}

// ── main fixture ─────────────────────────────────────────────────────────────

// impMainFixture builds the primary claude home + memory fixture used by most subtests.
// Returns (claudeDir, slug) where slug = <claudeDir>/projects/-tmp-acme-widgets.
func impMainFixture(t *testing.T) (string, string) {
	t.Helper()
	claudeDir := t.TempDir()
	slug := filepath.Join(claudeDir, "projects", "-tmp-acme-widgets")
	if err := os.MkdirAll(filepath.Join(slug, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}

	usage := map[string]int{
		"input_tokens":               120,
		"output_tokens":              340,
		"cache_creation_input_tokens": 0,
		"cache_read_input_tokens":    7000,
	}
	lines := []string{
		impUserLine("2026-05-20T10:00:00.000Z", "/tmp/acme/widgets", "add a healthcheck endpoint", false),
		impAssistantLine(
			"2026-05-20T10:01:00.000Z", "/tmp/acme/widgets",
			"", "claude-opus-4-8", usage,
			[]impContentBlock{
				impTextBlock("Added /healthz returning 200. Wired it into the router. Done."),
				impToolBlock("Edit", "/tmp/acme/widgets/app.py"),
			},
		),
	}
	impWriteLines(t, filepath.Join(slug, "session1.jsonl"), lines)

	// Memory file with a fake secret token — bait for redaction assertion.
	memLines := []string{
		"---",
		"name: deploy-note",
		"type: reference",
		"---",
		"Deploy via git only. Token sk-abcdefghijklmnopqrstuvwxyz0123 must be scrubbed.",
	}
	impWriteLines(t, filepath.Join(slug, "memory", "reference_deploy.md"), memLines)

	return claudeDir, slug
}

// ── TestImportCLI ─────────────────────────────────────────────────────────────

func TestImportCLI(t *testing.T) {
	// Build the binary once (shared across all subtests via clitest.Bin).
	bin := clitest.Bin(t)
	_ = bin

	// ── sub-harness that runs `devbrain import` with explicit --data / --claude ─
	type runFn func(data, claude string, extra ...string) clitest.Result
	mkRun := func(t *testing.T) runFn {
		t.Helper()
		h := clitest.New(t)
		return func(data, claude string, extra ...string) clitest.Result {
			args := []string{"import",
				"--data", data,
				"--claude", claude,
			}
			args = append(args, extra...)
			codexEmpty := t.TempDir()
			return h.RunWith(clitest.RunOpts{Env: map[string]string{"CODEX_HOME": codexEmpty}}, args...)
		}
	}

	// ── 1. dry-run writes nothing ─────────────────────────────────────────────
	t.Run("dry-run writes nothing", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()
		run := mkRun(t)
		r := run(data, claudeDir, "--alias", "widgets=acme__widgets")
		if r.Code != 0 {
			t.Fatalf("dry-run exited %d\nstderr: %s", r.Code, r.Stderr)
		}
		hits := clitest.Find(t, data, "*")
		if len(hits) != 0 {
			t.Errorf("dry-run wrote %d file(s): %v", len(hits), hits)
		}
	})

	// ── 2. unrouted history names the dir for the agent ───────────────────────
	t.Run("unrouted names dir for agent", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()
		run := mkRun(t)
		// No --alias: "widgets" stays in miscellaneous; dry-run must output AGENT: + "widgets"
		r := run(data, claudeDir)
		if !strings.Contains(r.Stdout, "AGENT:") {
			t.Errorf("expected AGENT: in dry-run stdout:\n%s", r.Stdout)
		}
		if !strings.Contains(r.Stdout, "widgets") {
			t.Errorf("expected 'widgets' in dry-run stdout:\n%s", r.Stdout)
		}
	})

	// ── 3. --apply writes logs + memory, redacts secrets ─────────────────────
	t.Run("apply writes logs and memory", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()
		run := mkRun(t)
		r := run(data, claudeDir, "--alias", "widgets=acme__widgets", "--apply")
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}

		logFile := impFindMD(t, filepath.Join(data, "projects", "acme__widgets", "log"))
		memFile := filepath.Join(data, "projects", "acme__widgets", "memory", "reference_deploy.md")

		t.Run("writes a log file", func(t *testing.T) {
			if logFile == "" {
				t.Fatal("no log .md file found")
			}
		})
		t.Run("log has the prompt", func(t *testing.T) {
			if logFile == "" {
				t.Skip("no log file")
			}
			content := clitest.Read(t, logFile)
			if !strings.Contains(content, "add a healthcheck endpoint") {
				t.Errorf("log missing prompt:\n%s", content)
			}
		})
		t.Run("recap = closing sentence", func(t *testing.T) {
			if logFile == "" {
				t.Skip("no log file")
			}
			content := clitest.Read(t, logFile)
			// Check for recap line pattern "↳ HH:MM:SS — ..." and that it contains the sentence
			hasRecap := clitest.HasLineWith(content, "↳") && clitest.HasLineWith(content, "—")
			if !hasRecap {
				t.Errorf("log missing recap line (↳ … —):\n%s", content)
			}
			if !strings.Contains(content, "Wired it into the router") {
				t.Errorf("log missing recap sentence 'Wired it into the router':\n%s", content)
			}
		})
		t.Run("log records touched file", func(t *testing.T) {
			if logFile == "" {
				t.Skip("no log file")
			}
			content := clitest.Read(t, logFile)
			if !strings.Contains(content, "touched: app.py") {
				t.Errorf("log missing 'touched: app.py':\n%s", content)
			}
		})
		t.Run("log carries BACKFILLED banner", func(t *testing.T) {
			if logFile == "" {
				t.Skip("no log file")
			}
			content := clitest.Read(t, logFile)
			if !strings.Contains(content, "BACKFILLED") {
				t.Errorf("log missing BACKFILLED banner:\n%s", content)
			}
		})
		t.Run("mirrors the memory file", func(t *testing.T) {
			if _, err := os.Stat(memFile); err != nil {
				t.Errorf("memory file not written: %v", err)
			}
		})
		t.Run("redacts secret in memory", func(t *testing.T) {
			if _, err := os.Stat(memFile); err != nil {
				t.Skip("no memory file")
			}
			content := clitest.Read(t, memFile)
			if !strings.Contains(content, "REDACTED") {
				t.Errorf("memory file missing REDACTED:\n%s", content)
			}
			if strings.Contains(content, "sk-abcdefghijklmnopqrstuvwxyz0123") {
				t.Errorf("memory file still contains raw secret:\n%s", content)
			}
		})
	})

	// ── 4. token sidecar ─────────────────────────────────────────────────────
	t.Run("token sidecar", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()
		run := mkRun(t)
		r := run(data, claudeDir, "--alias", "widgets=acme__widgets", "--apply")
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}
		tok := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")

		t.Run("backfills tokens sidecar", func(t *testing.T) {
			fi, err := os.Stat(tok)
			if err != nil || fi.Size() == 0 {
				t.Errorf("tokens.jsonl not written or empty: %v", err)
			}
		})
		t.Run("sidecar carries usage+model", func(t *testing.T) {
			if _, err := os.Stat(tok); err != nil {
				t.Skip("no tokens.jsonl")
			}
			content := clitest.Read(t, tok)
			if !strings.Contains(content, `"in": 120`) {
				t.Errorf("sidecar missing in:120\n%s", content)
			}
			if !strings.Contains(content, `"out": 340`) {
				t.Errorf("sidecar missing out:340\n%s", content)
			}
			if !strings.Contains(content, "claude-opus-4-8") {
				t.Errorf("sidecar missing model\n%s", content)
			}
		})
		t.Run("sidecar marks interactive (auto:false)", func(t *testing.T) {
			if _, err := os.Stat(tok); err != nil {
				t.Skip("no tokens.jsonl")
			}
			content := clitest.Read(t, tok)
			if !strings.Contains(content, `"auto": false`) {
				t.Errorf("sidecar missing auto:false\n%s", content)
			}
		})
		t.Run("re-apply does not duplicate", func(t *testing.T) {
			// Run import again — must stay at 1 line.
			run(data, claudeDir, "--alias", "widgets=acme__widgets", "--apply")
			if _, err := os.Stat(tok); err != nil {
				t.Skip("no tokens.jsonl")
			}
			n := impFileNewlineCount(t, tok)
			if n != 1 {
				t.Errorf("tokens.jsonl has %d lines after re-apply, want 1\n%s", n, clitest.Read(t, tok))
			}
		})
	})

	// ── 5. per-message dedup: usage billed once, not per-block ───────────────
	t.Run("per-message dedup", func(t *testing.T) {
		claudeDir := t.TempDir()
		sb := filepath.Join(claudeDir, "projects", "-tmp-acme-widgets")
		if err := os.MkdirAll(sb, 0o755); err != nil {
			t.Fatal(err)
		}

		sharedUsage := map[string]int{
			"input_tokens":               10,
			"output_tokens":              20,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":    7000,
		}
		blocks := []impContentBlock{
			impThinkBlock("hm"),
			impTextBlock("All set. Done."),
			impToolBlock("Edit", "/tmp/acme/widgets/a.py"),
		}
		var lines []string
		lines = append(lines, impUserLine("2026-05-20T10:00:00.000Z", "/tmp/acme/widgets", "split a response into blocks", false))
		for _, blk := range blocks {
			lines = append(lines, impAssistantLine(
				"2026-05-20T10:01:00.000Z", "/tmp/acme/widgets",
				"msg_dup1", "claude-opus-4-8", sharedUsage,
				[]impContentBlock{blk},
			))
		}
		impWriteLines(t, filepath.Join(sb, "sessionB.jsonl"), lines)

		data := t.TempDir()
		run := mkRun(t)
		r := run(data, claudeDir, "--alias", "widgets=acme__widgets", "--apply")
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}
		tokB := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")
		content := clitest.Read(t, tokB)

		if !strings.Contains(content, `"cache_read": 7000`) {
			t.Errorf("dedup: expected cache_read:7000 (billed once)\n%s", content)
		}
		if strings.Contains(content, "21000") {
			t.Errorf("dedup: sidecar contains 21000 (triple-billed)\n%s", content)
		}
	})

	// ── 6. sidechain / sub-agent entries stay inside parent turn ─────────────
	t.Run("sidechain import", func(t *testing.T) {
		claudeDir := t.TempDir()
		ss := filepath.Join(claudeDir, "projects", "-tmp-acme-widgets")
		if err := os.MkdirAll(ss, 0o755); err != nil {
			t.Fatal(err)
		}

		mkUsage := func(in, out int) map[string]int {
			return map[string]int{
				"input_tokens": in, "output_tokens": out,
				"cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
			}
		}
		lines := []string{
			impUserLine("2026-05-22T12:00:00.000Z", "/tmp/acme/widgets", "run parent import task", false),
			impAssistantLine("2026-05-22T12:00:10.000Z", "/tmp/acme/widgets",
				"msg_parent_a", "claude-opus-4-8", mkUsage(10, 20),
				[]impContentBlock{impTextBlock("Started parent import work.")}),
			impUserLine("2026-05-22T12:00:15.000Z", "/tmp/acme/widgets", "sub-agent prompt", true), // isSidechain
			impAssistantLine("2026-05-22T12:00:20.000Z", "/tmp/acme/widgets",
				"msg_side", "claude-opus-4-8", mkUsage(1, 2),
				[]impContentBlock{impTextBlock("Sub-agent imported context.")}),
			impAssistantLine("2026-05-22T12:00:30.000Z", "/tmp/acme/widgets",
				"msg_parent_b", "claude-opus-4-8", mkUsage(3, 4),
				[]impContentBlock{impTextBlock("Finished parent import turn.")}),
		}
		impWriteLines(t, filepath.Join(ss, "sessionS.jsonl"), lines)

		data := t.TempDir()
		run := mkRun(t)
		r := run(data, claudeDir, "--alias", "widgets=acme__widgets", "--apply")
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}

		logS := impFindMD(t, filepath.Join(data, "projects", "acme__widgets", "log"))
		tokS := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")

		t.Run("writes one parent prompt", func(t *testing.T) {
			if logS == "" {
				t.Fatal("no log file")
			}
			content := clitest.Read(t, logS)
			n := impCountLines(content, "## 12:00:00")
			if n != 1 {
				t.Errorf("sidechain: expected 1 '## 12:00:00' line, got %d\n%s", n, content)
			}
		})
		t.Run("recap uses parent final", func(t *testing.T) {
			if logS == "" {
				t.Skip("no log file")
			}
			content := clitest.Read(t, logS)
			if !strings.Contains(content, "Finished parent import turn.") {
				t.Errorf("sidechain: recap not from parent final:\n%s", content)
			}
		})
		t.Run("tokens include whole turn", func(t *testing.T) {
			content := clitest.Read(t, tokS)
			// parent_a(10+20) + side(1+2) + parent_b(3+4) = in:14, out:26
			if !strings.Contains(content, `"in": 14`) {
				t.Errorf("sidechain tokens: expected in:14\n%s", content)
			}
			if !strings.Contains(content, `"out": 26`) {
				t.Errorf("sidechain tokens: expected out:26\n%s", content)
			}
		})
		t.Run("ts is final parent response", func(t *testing.T) {
			content := clitest.Read(t, tokS)
			if !strings.Contains(content, `"ts": "2026-05-22T12:00:30Z"`) {
				t.Errorf("sidechain ts: expected 2026-05-22T12:00:30Z\n%s", content)
			}
		})
	})

	// ── 7. malformed / timestamp-less transcript ──────────────────────────────
	t.Run("timestamp-less transcript falls back", func(t *testing.T) {
		claudeDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(claudeDir, "projects", "-tmp-acme-widgets"), 0o755); err != nil {
			t.Fatal(err)
		}
		// No "timestamp" key in either event.
		lines := []string{
			impUserLine("", "/tmp/acme/widgets", "timestamp missing", false),
			impAssistantLine("", "/tmp/acme/widgets", "", "", nil,
				[]impContentBlock{impTextBlock("Handled missing timestamp.")}),
		}
		impWriteLines(t, filepath.Join(claudeDir, "projects", "-tmp-acme-widgets", "missing-ts.jsonl"), lines)

		data := t.TempDir()
		run := mkRun(t)
		r := run(data, claudeDir, "--alias", "widgets=acme__widgets", "--apply")
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}

		logM := filepath.Join(data, "projects", "acme__widgets", "log", "1970-01-01", "widgets.missing-ts.md")
		if _, err := os.Stat(logM); err != nil {
			t.Fatalf("expected fallback log at %s: %v", logM, err)
		}
		content := clitest.Read(t, logM)
		if !strings.Contains(content, "## 00:00:00") {
			t.Errorf("fallback log missing '## 00:00:00':\n%s", content)
		}
		if !strings.Contains(content, "timestamp missing") {
			t.Errorf("fallback log missing prompt:\n%s", content)
		}
	})

	// ── 8. route heal: stale-route row stripped; turn re-lands under current route ─
	t.Run("route heal", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()

		// Pre-seed session1's token row under 'miscellaneous' (the stale route).
		miscDir := filepath.Join(data, "projects", "miscellaneous")
		if err := os.MkdirAll(miscDir, 0o755); err != nil {
			t.Fatal(err)
		}
		seedRow := `{"ts":"2026-05-20T10:01:00Z","session":"session1","model":"claude-opus-4-8","in":120,"out":340,"cache_create":0,"cache_read":7000,"auto":false}`
		clitest.WriteFile(t, filepath.Join(miscDir, "tokens.jsonl"), seedRow+"\n")

		run := mkRun(t)
		run(data, claudeDir, "--alias", "widgets=acme__widgets", "--tokens-only", "--apply")

		t.Run("stale-route row stripped", func(t *testing.T) {
			content := clitest.Read(t, filepath.Join(miscDir, "tokens.jsonl"))
			if strings.Contains(content, "session1") {
				t.Errorf("route heal: stale-route miscellaneous/tokens.jsonl still has session1:\n%s", content)
			}
		})
		t.Run("turn re-lands once under current route", func(t *testing.T) {
			tokPath := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")
			content := clitest.Read(t, tokPath)
			n := impCountLines(content, "session1")
			if n != 1 {
				t.Errorf("route heal: expected exactly 1 session1 line in acme__widgets/tokens.jsonl, got %d:\n%s", n, content)
			}
		})
	})

	// ── 9. --exclude skips the project ───────────────────────────────────────
	t.Run("exclude skips project", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()
		run := mkRun(t)
		run(data, claudeDir, "--alias", "widgets=acme__widgets", "--exclude", "acme__widgets", "--apply")

		hits := clitest.Find(t, filepath.Join(data, "projects", "acme__widgets"), "*")
		if len(hits) != 0 {
			t.Errorf("--exclude: still wrote %d file(s): %v", len(hits), hits)
		}
	})

	// ── 10. persistent alias file routes ─────────────────────────────────────
	t.Run("alias file routes project", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()
		clitest.WriteFile(t, filepath.Join(data, "import-aliases"), "# rename map\nwidgets=acme__widgets\n")

		run := mkRun(t)
		run(data, claudeDir, "--apply") // no --alias flag

		logDir := filepath.Join(data, "projects", "acme__widgets", "log")
		if impFindMD(t, logDir) == "" {
			t.Error("alias file: no log .md found after --apply")
		}
	})

	// ── 11. legacy .import-aliases still routes ───────────────────────────────
	t.Run("legacy .import-aliases routes project", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()
		clitest.WriteFile(t, filepath.Join(data, ".import-aliases"), "widgets=acme__widgets\n")

		run := mkRun(t)
		run(data, claudeDir, "--apply")

		logDir := filepath.Join(data, "projects", "acme__widgets", "log")
		if impFindMD(t, logDir) == "" {
			t.Error("legacy .import-aliases: no log .md found after --apply")
		}
	})

	// ── 12. --tokens-only writes sidecar but no logs ─────────────────────────
	t.Run("tokens-only", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()
		run := mkRun(t)
		run(data, claudeDir, "--alias", "widgets=acme__widgets", "--tokens-only", "--apply")

		tok := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")
		fi, err := os.Stat(tok)
		if err != nil || fi.Size() == 0 {
			t.Errorf("tokens-only: tokens.jsonl not written or empty")
		}
		logHits := clitest.Find(t, filepath.Join(data, "projects", "acme__widgets", "log"), "*.md")
		if len(logHits) != 0 {
			t.Errorf("tokens-only: wrote %d log file(s): %v", len(logHits), logHits)
		}
	})

	// ── 13. live session: tokens still backfilled, log NOT duplicated ─────────
	t.Run("live session tokens backfilled log not duplicated", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()

		// Pre-plant a live (non-BACKFILLED) log for the same session + day.
		liveLogDir := filepath.Join(data, "projects", "acme__widgets", "log", "2026-05-20")
		if err := os.MkdirAll(liveLogDir, 0o755); err != nil {
			t.Fatal(err)
		}
		liveLog := filepath.Join(liveLogDir, "widgets.session1.md")
		clitest.WriteFile(t, liveLog,
			"# live\n\n## 10:00:00\n\nadd a healthcheck endpoint\n\n↳ 10:01:00 — pre-existing live recap\n\n")

		run := mkRun(t)
		run(data, claudeDir, "--alias", "widgets=acme__widgets", "--apply")

		tok4 := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")
		t.Run("tokens still backfilled", func(t *testing.T) {
			fi, err := os.Stat(tok4)
			if err != nil || fi.Size() == 0 {
				t.Error("live session: tokens.jsonl not written")
			}
			content := clitest.Read(t, tok4)
			if !strings.Contains(content, `"in": 120`) {
				t.Errorf("live session: tokens not backfilled\n%s", content)
			}
		})
		t.Run("log NOT duplicated", func(t *testing.T) {
			content := clitest.Read(t, liveLog)
			if strings.Contains(content, "BACKFILLED") {
				t.Error("live session: live log was overwritten with BACKFILLED marker")
			}
			n := impCountLines(content, "## 10:00:00")
			if n != 1 {
				t.Errorf("live session: log has %d '## 10:00:00' sections, want 1\n%s", n, content)
			}
		})
	})

	// ── 14. replace: stale partial row stripped; transcript re-derived ───────
	// A session whose transcript is on disk is authoritative. A stale sidecar row
	// (e.g. a partial Stop-hook capture) is stripped and re-derived from the transcript.
	t.Run("replace", func(t *testing.T) {
		claudeDir, _ := impMainFixture(t)
		data := t.TempDir()

		// Seed a stale row with a DIFFERENT ts (09:00 vs the transcript's 10:01) for the same session.
		projDir := filepath.Join(data, "projects", "acme__widgets")
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			t.Fatal(err)
		}
		seededRow := `{"ts": "2026-05-20T09:00:00Z", "session": "session1", "model": "claude-opus-4-8", "in": 1, "out": 1, "cache_create": 0, "cache_read": 0, "auto": false}`
		clitest.WriteFile(t, filepath.Join(projDir, "tokens.jsonl"), seededRow+"\n")

		run := mkRun(t)
		run(data, claudeDir, "--alias", "widgets=acme__widgets", "--tokens-only", "--apply")

		tok6 := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")
		content := clitest.Read(t, tok6)

		t.Run("stale partial row stripped", func(t *testing.T) {
			if strings.Contains(content, "2026-05-20T09:00:00Z") {
				t.Errorf("replace: seeded stale ts still present\n%s", content)
			}
		})
		t.Run("transcript turn re-derived", func(t *testing.T) {
			if !strings.Contains(content, "2026-05-20T10:01:00Z") {
				t.Errorf("replace: transcript turn ts missing\n%s", content)
			}
		})
		t.Run("exactly the transcript rows", func(t *testing.T) {
			n := impFileNewlineCount(t, tok6)
			if n != 1 {
				t.Errorf("replace: got %d newlines, want 1\n%s", n, content)
			}
		})
		t.Run("rows carry the turn key", func(t *testing.T) {
			if !strings.Contains(content, `"turn": "2026-05-20T10:00:00Z"`) {
				t.Errorf("replace: row missing turn key\n%s", content)
			}
		})
	})

	// ── 15. subagent transcripts billed to parent session ────────────────────
	// <session>/subagents/agent-*.jsonl are separate files the Stop hook never sees;
	// import bills their turns to the PARENT session with an agent-prefixed turn key.
	t.Run("subagent transcript", func(t *testing.T) {
		claudeDir, slug := impMainFixture(t)
		data := t.TempDir()

		// Create the subagent transcript directory and file.
		subagentDir := filepath.Join(slug, "session1", "subagents")
		if err := os.MkdirAll(subagentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		saLines := []string{
			impUserLine("2026-05-20T10:00:30.000Z", "/tmp/acme/widgets", "scan the repo", true),
			impAssistantLine("2026-05-20T10:00:50.000Z", "/tmp/acme/widgets",
				"sa1", "claude-haiku-4-5",
				map[string]int{
					"input_tokens": 10, "output_tokens": 5,
					"cache_creation_input_tokens": 0, "cache_read_input_tokens": 600,
				},
				[]impContentBlock{impTextBlock("Scanned.")}),
		}
		impWriteLines(t, filepath.Join(subagentDir, "agent-x1.jsonl"), saLines)

		run := mkRun(t)
		r := run(data, claudeDir, "--alias", "widgets=acme__widgets", "--tokens-only", "--apply")
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}

		tok7 := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")
		content := clitest.Read(t, tok7)

		t.Run("subagent turn billed to parent session", func(t *testing.T) {
			if !strings.Contains(content, `"turn": "agent-x1:2026-05-20T10:00:30Z"`) {
				t.Errorf("subagent: missing agent-prefixed turn key\n%s", content)
			}
			n := impCountLines(content, "session1")
			if n != 2 {
				t.Errorf("subagent: expected 2 session1 rows, got %d\n%s", n, content)
			}
		})
		t.Run("subagent re-import stays deduped", func(t *testing.T) {
			// Re-run import — must stay at 2 rows.
			run(data, claudeDir, "--alias", "widgets=acme__widgets", "--tokens-only", "--apply")
			n := impFileNewlineCount(t, tok7)
			if n != 2 {
				t.Errorf("subagent re-import: expected 2 newlines, got %d\n%s", n, clitest.Read(t, tok7))
			}
		})
	})

	// ── 16. killed-turn backfill: routed by PATH (match_known), auto=true ────
	t.Run("killed turn routed by path", func(t *testing.T) {
		claudeDir := t.TempDir()
		data := t.TempDir()

		// Make "widgets" a KNOWN repo by creating the project dir.
		if err := os.MkdirAll(filepath.Join(data, "projects", "acme__widgets"), 0o755); err != nil {
			t.Fatal(err)
		}

		// Worker worktree: cwd = /tmp/nightshift/widgets-w3 → match_known finds acme__widgets.
		sk := filepath.Join(claudeDir, "projects", "-tmp-nightshift-widgets-w3")
		if err := os.MkdirAll(sk, 0o755); err != nil {
			t.Fatal(err)
		}
		usage := map[string]int{
			"input_tokens": 500, "output_tokens": 900,
			"cache_creation_input_tokens": 0, "cache_read_input_tokens": 40000,
		}
		lines := []string{
			impUserLine("2026-05-21T02:00:00.000Z", "/tmp/nightshift/widgets-w3", "/continue", false),
			impAssistantLine("2026-05-21T02:05:00.000Z", "/tmp/nightshift/widgets-w3",
				"msg_killed", "claude-opus-4-8", usage,
				[]impContentBlock{impTextBlock("Drained a task. Done.")}),
		}
		impWriteLines(t, filepath.Join(sk, "sessionK.jsonl"), lines)

		run := mkRun(t)
		r := run(data, claudeDir, "--tokens-only", "--apply") // NO --alias
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}

		tokK := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")

		t.Run("routed to project by PATH", func(t *testing.T) {
			fi, err := os.Stat(tokK)
			if err != nil || fi.Size() == 0 {
				t.Fatalf("killed turn: tokens.jsonl not written: %v", err)
			}
			content := clitest.Read(t, tokK)
			if !strings.Contains(content, `"in": 500`) {
				t.Errorf("killed turn: expected in:500\n%s", content)
			}
		})
		t.Run("marked auto (nightshift worker)", func(t *testing.T) {
			content := clitest.Read(t, tokK)
			if !strings.Contains(content, `"auto": true`) {
				t.Errorf("killed turn: expected auto:true\n%s", content)
			}
		})
		t.Run("NOT pooled in miscellaneous", func(t *testing.T) {
			miscTok := filepath.Join(data, "projects", "miscellaneous", "tokens.jsonl")
			if _, err := os.Stat(miscTok); err == nil {
				t.Errorf("killed turn: miscellaneous tokens.jsonl was created\n%s", clitest.Read(t, miscTok))
			}
		})
	})

	// ── 17. Codex token backfill ──────────────────────────────────────────────
	t.Run("codex token backfill", func(t *testing.T) {
		claudeDir := t.TempDir()
		codexDir := t.TempDir()
		data := t.TempDir()

		// Pre-seed an older partial Codex row to be replaced.
		if err := os.MkdirAll(filepath.Join(data, "projects", "acme__widgets"), 0o755); err != nil {
			t.Fatal(err)
		}
		staleRow := `{"ts":"2026-06-30T10:09:00Z","session":"codex-session","model":"gpt-5.5","in":999,"out":999,"cache_create":0,"cache_read":999,"auto":false}`
		clitest.WriteFile(t, filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl"), staleRow+"\n")

		// Codex session file.
		sessDir := filepath.Join(codexDir, "sessions", "2026", "06", "30")
		if err := os.MkdirAll(sessDir, 0o755); err != nil {
			t.Fatal(err)
		}
		codexLines := []string{
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T10:00:00.000Z",
				"type":      "session_meta",
				"payload":   map[string]any{"id": "codex-session", "cwd": "/tmp/acme/widgets"},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T10:00:01.000Z",
				"type":      "turn_context",
				"payload":   map[string]any{"turn_id": "turn-a", "model": "gpt-5.5", "cwd": "/tmp/acme/widgets"},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T10:00:01.500Z",
				"type":      "event_msg",
				"payload":   map[string]any{"type": "user_message", "message": "do codex work"},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T10:00:02.000Z",
				"type":      "event_msg",
				"payload": map[string]any{
					"type": "token_count",
					"info": map[string]any{
						"last_token_usage": map[string]any{
							"input_tokens": 100, "cached_input_tokens": 40,
							"output_tokens": 5, "total_tokens": 105,
						},
					},
				},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T10:00:03.000Z",
				"type":      "event_msg",
				"payload": map[string]any{
					"type": "token_count",
					"info": map[string]any{
						"last_token_usage": map[string]any{
							"input_tokens": 120, "cached_input_tokens": 50,
							"output_tokens": 7, "total_tokens": 127,
						},
					},
				},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T10:00:04.000Z",
				"type":      "event_msg",
				"payload": map[string]any{
					"type":               "task_complete",
					"turn_id":            "turn-a",
					"last_agent_message": "Codex work done.",
					"completed_at":       1782813604,
				},
			}),
		}
		impWriteLines(t, filepath.Join(sessDir, "rollout-2026-06-30T10-00-00-codex-session.jsonl"), codexLines)

		h := clitest.New(t)
		r := h.RunWith(clitest.RunOpts{Env: map[string]string{"CODEX_HOME": t.TempDir()}},
			"import",
			"--data", data,
			"--claude", claudeDir,
			"--codex", codexDir,
			"--alias", "widgets=acme__widgets",
			"--tokens-only",
			"--apply",
		)
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}

		tokC := filepath.Join(data, "projects", "acme__widgets", "tokens.jsonl")
		content := clitest.Read(t, tokC)

		t.Run("writes one turn row", func(t *testing.T) {
			// wc -l: count newlines
			n := impFileNewlineCount(t, tokC)
			if n != 1 {
				t.Errorf("codex: expected 1 line, got %d\n%s", n, content)
			}
			// aggregated: 100+120=220 in → but cached is subtracted? Let's check actual:
			// bash says "in": 130 and "out": 12
			if !strings.Contains(content, `"in": 130`) {
				t.Errorf("codex: expected in:130\n%s", content)
			}
			if !strings.Contains(content, `"out": 12`) {
				t.Errorf("codex: expected out:12\n%s", content)
			}
		})
		t.Run("replaces stale partial rows", func(t *testing.T) {
			if strings.Contains(content, "999") {
				t.Errorf("codex: stale '999' still present\n%s", content)
			}
		})
		t.Run("carries cached input", func(t *testing.T) {
			if !strings.Contains(content, `"cache_read": 90`) {
				t.Errorf("codex: expected cache_read:90\n%s", content)
			}
		})
	})

	// ── 18. Codex log harvest ──────────────────────────────────────────────────
	t.Run("codex log harvest", func(t *testing.T) {
		claudeDir := t.TempDir()
		codexDir := t.TempDir()
		data := t.TempDir()
		if err := os.MkdirAll(filepath.Join(data, "projects", "acme__widgets"), 0o755); err != nil {
			t.Fatal(err)
		}

		sessDir := filepath.Join(codexDir, "sessions", "2026", "06", "30")
		if err := os.MkdirAll(sessDir, 0o755); err != nil {
			t.Fatal(err)
		}
		codexLines := []string{
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T12:00:00.000Z",
				"type":      "session_meta",
				"payload":   map[string]any{"id": "codexlog", "cwd": "/tmp/acme/widgets"},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T12:00:01.000Z",
				"type":      "turn_context",
				"payload":   map[string]any{"turn_id": "turn-a", "model": "gpt-5.5", "cwd": "/tmp/acme/widgets"},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T12:00:01.500Z",
				"type":      "event_msg",
				"payload":   map[string]any{"type": "user_message", "message": "$distill"},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T12:00:01.700Z",
				"type":      "response_item",
				"payload": map[string]any{
					"type": "message", "role": "user",
					"content": []map[string]any{
						{"type": "input_text", "text": "<skill>\n<name>distill</name>\n<path>/x/distill/SKILL.md</path>\n"},
					},
				},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T12:00:02.000Z",
				"type":      "event_msg",
				"payload":   map[string]any{"type": "exec_command_begin"},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T12:00:03.000Z",
				"type":      "event_msg",
				"payload": map[string]any{
					"type": "token_count",
					"info": map[string]any{
						"last_token_usage": map[string]any{
							"input_tokens": 100, "cached_input_tokens": 40,
							"output_tokens": 5, "total_tokens": 105,
						},
					},
				},
			}),
			impCodexLine(map[string]any{
				"timestamp": "2026-06-30T12:00:04.000Z",
				"type":      "event_msg",
				"payload": map[string]any{
					"type":               "task_complete",
					"turn_id":            "turn-a",
					"last_agent_message": "Folded the log into the brain.",
					"completed_at":       1782820804,
				},
			}),
		}
		impWriteLines(t, filepath.Join(sessDir, "rollout-2026-06-30T12-00-00-codexlog.jsonl"), codexLines)

		h := clitest.New(t)
		r := h.RunWith(clitest.RunOpts{Env: map[string]string{"CODEX_HOME": t.TempDir()}},
			"import",
			"--data", data,
			"--claude", claudeDir,
			"--codex", codexDir,
			"--alias", "widgets=acme__widgets",
			"--apply",
		)
		if r.Code != 0 {
			t.Fatalf("--apply exited %d\nstderr: %s", r.Code, r.Stderr)
		}

		logL := filepath.Join(data, "projects", "acme__widgets", "log", "2026-06-30", "widgets.codexlog.md")

		t.Run("codex log imported", func(t *testing.T) {
			if _, err := os.Stat(logL); err != nil {
				t.Fatalf("codex log not found at %s: %v", logL, err)
			}
		})
		t.Run("codex log has typed prompt", func(t *testing.T) {
			content := clitest.Read(t, logL)
			// The prompt line must appear as its own line.
			found := false
			for _, ln := range strings.Split(content, "\n") {
				if ln == "$distill" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("codex log: '$distill' not on its own line:\n%s", content)
			}
		})
		t.Run("codex log names skill run", func(t *testing.T) {
			content := clitest.Read(t, logL)
			if !strings.Contains(content, "tools: Skill:distill") {
				t.Errorf("codex log: missing 'tools: Skill:distill':\n%s", content)
			}
		})
		t.Run("codex log has recap", func(t *testing.T) {
			content := clitest.Read(t, logL)
			if !strings.Contains(content, "Folded the log into the brain.") {
				t.Errorf("codex log: missing recap:\n%s", content)
			}
		})
		t.Run("codex log marked BACKFILLED", func(t *testing.T) {
			content := clitest.Read(t, logL)
			if !strings.Contains(content, "BACKFILLED") {
				t.Errorf("codex log: missing BACKFILLED banner:\n%s", content)
			}
		})
	})

	// ── 19. per-DAY log backfill (multi-day session) ──────────────────────────
	t.Run("per-day log backfill multi-day session", func(t *testing.T) {
		claudeDir := t.TempDir()
		slugD := filepath.Join(claudeDir, "projects", "-tmp-acme-widgets")
		if err := os.MkdirAll(slugD, 0o755); err != nil {
			t.Fatal(err)
		}

		mkUsage := func(cr int) map[string]int {
			return map[string]int{
				"input_tokens": 10, "output_tokens": 20,
				"cache_creation_input_tokens": 0, "cache_read_input_tokens": cr,
			}
		}
		lines := []string{
			impUserLine("2026-05-20T10:00:00.000Z", "/tmp/acme/widgets", "day one prompt", false),
			impAssistantLine("2026-05-20T10:01:00.000Z", "/tmp/acme/widgets",
				"", "claude-opus-4-8", mkUsage(100),
				[]impContentBlock{impTextBlock("Did day one. Done.")}),
			impUserLine("2026-05-21T10:00:00.000Z", "/tmp/acme/widgets", "day two prompt", false),
			impAssistantLine("2026-05-21T10:01:00.000Z", "/tmp/acme/widgets",
				"", "claude-opus-4-8", mkUsage(100),
				[]impContentBlock{impTextBlock("Did day two. Done.")}),
		}
		impWriteLines(t, filepath.Join(slugD, "multi.jsonl"), lines)

		data := t.TempDir()
		// Live log exists for day ONE only.
		liveDayOneDir := filepath.Join(data, "projects", "acme__widgets", "log", "2026-05-20")
		if err := os.MkdirAll(liveDayOneDir, 0o755); err != nil {
			t.Fatal(err)
		}
		liveDayOne := filepath.Join(liveDayOneDir, "widgets.multi.md")
		clitest.WriteFile(t, liveDayOne,
			"# live\n\n## 10:00:00\n\nday one prompt\n\n↳ 10:01:00 — live day-one recap\n\n")

		run := mkRun(t)
		run(data, claudeDir, "--alias", "widgets=acme__widgets", "--apply")

		dayTwo := filepath.Join(data, "projects", "acme__widgets", "log", "2026-05-21", "widgets.multi.md")

		t.Run("missing day backfilled", func(t *testing.T) {
			if _, err := os.Stat(dayTwo); err != nil {
				t.Fatalf("per-day: day-two log not written: %v", err)
			}
			content := clitest.Read(t, dayTwo)
			if !strings.Contains(content, "day two prompt") {
				t.Errorf("per-day: day-two log missing prompt:\n%s", content)
			}
			if !strings.Contains(content, "BACKFILLED") {
				t.Errorf("per-day: day-two log missing BACKFILLED:\n%s", content)
			}
		})
		t.Run("live day left untouched", func(t *testing.T) {
			content := clitest.Read(t, liveDayOne)
			if strings.Contains(content, "BACKFILLED") {
				t.Errorf("per-day: live day-one log was overwritten\n%s", content)
			}
			if !strings.Contains(content, "live day-one recap") {
				t.Errorf("per-day: live day-one recap missing\n%s", content)
			}
		})
	})
}

