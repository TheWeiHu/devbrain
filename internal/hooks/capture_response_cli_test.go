package hooks_test

// Go-native port of scripts/test-capture-response.sh: Stop hook tests —
// recap appending, token/sidecar dedup, sidechain handling, Codex shape.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// responseWorktreeSlug sanitizes a dirname the same way WorktreeSlug does
// (no git repo -> basename of cwd, Sanitize = lowercase, space->dash, keep [a-z0-9._-]).
func responseWorktreeSlug(cwd string) string {
	base := filepath.Base(cwd)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == ' ':
			b.WriteByte('-')
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// responseLogPath returns where the log file lives for (h, cwd, session).
// It must match sessionLogPath in hooks.go: projects/<project>/log/<today>/<wt>.<session>.md
func responseLogPath(h *clitest.Harness, cwd, session string) string {
	day := time.Now().UTC().Format("2006-01-02")
	wt := responseWorktreeSlug(cwd)
	return filepath.Join(h.Data, "projects", h.Project, "log", day, wt+"."+session+".md")
}

// mkResponseLog pre-creates a log file with a minimal prompt entry so the
// hook finds it (hook is a no-op when no log exists).
func mkResponseLog(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# testproj log\n\n## 00:00:00\n\nprompt\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fireResponse sends a Claude stop-hook payload.
func fireResponse(h *clitest.Harness, transcriptPath, cwd, session string) clitest.Result {
	b, _ := json.Marshal(map[string]any{
		"transcript_path": transcriptPath,
		"cwd":             cwd,
		"session_id":      session,
	})
	return h.RunWith(clitest.RunOpts{Stdin: string(b)}, "hook", "response")
}

// fireCodexResponse sends a Codex stop-hook payload.
func fireCodexResponse(h *clitest.Harness, transcriptPath, cwd, turnID, lastMsg string) clitest.Result {
	b, _ := json.Marshal(map[string]any{
		"transcript_path":       transcriptPath,
		"cwd":                   cwd,
		"turn_id":               turnID,
		"last_assistant_message": lastMsg,
	})
	return h.RunWith(clitest.RunOpts{
		Stdin: string(b),
		Env:   map[string]string{"DEVBRAIN_HARNESS": "codex"},
	}, "hook", "response")
}

func TestCaptureResponse(t *testing.T) {
	h := clitest.New(t)
	cwd := t.TempDir()
	side := filepath.Join(h.Data, "projects", h.Project, "tokens.jsonl")

	// ── Case 1: short response, two assistant blocks ──────────────────────────
	t1 := filepath.Join(t.TempDir(), "t1.jsonl")
	lines1 := []string{
		`{"type":"user","message":{"content":[{"type":"text","text":"please refactor foo"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look."},{"type":"tool_use","name":"Read","input":{"file_path":"/x/foo.py"}},{"type":"tool_use","name":"Skill","input":{"skill":"distill"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Did the refactor.\n\nDecided to keep it simple, no extra config. Token sk-abcdefghijklmnopqrstuvwxyz0123 leaked.\n\nRefactored foo.py to remove the duplicate loop."}]}}`,
	}
	clitest.WriteFile(t, t1, strings.Join(lines1, "\n")+"\n")

	t.Run("no-op when log file absent", func(t *testing.T) {
		// fire with a session whose log file doesn't exist
		logPath := responseLogPath(h, cwd, "absent")
		fireResponse(h, t1, cwd, "absent")
		if _, err := os.Stat(logPath); err == nil {
			t.Error("no-op when log file absent: file was created")
		}
	})

	t.Run("short response appended correctly", func(t *testing.T) {
		L1 := responseLogPath(h, cwd, "short")
		mkResponseLog(t, L1)
		fireResponse(h, t1, cwd, "short")
		log := clitest.Read(t, L1)

		if !clitest.HasLineWith(log, "↳", " — ") {
			t.Error("appends recap arrow: missing '↳ ... — '")
		}
		if !strings.Contains(log, "Refactored foo.py to remove the duplicate loop.") {
			t.Error("recap = closing sentence: missing")
		}
		if !strings.Contains(log, "tools: Read") {
			t.Error("meta records tool: 'tools: Read' missing")
		}
		if !strings.Contains(log, "Skill:distill×1") {
			t.Error("meta names the skill run: 'Skill:distill×1' missing")
		}
		if !strings.Contains(log, "touched: foo.py") {
			t.Error("meta records touched file: 'touched: foo.py' missing")
		}
		if !strings.Contains(log, "response sample:") {
			t.Error("labels response sample: missing")
		}
		if !strings.Contains(log, "   > Let me look.") {
			t.Error("captures whole turn (head): intermediate block missing")
		}
		if !strings.Contains(log, "Decided to keep it simple") {
			t.Error("body includes reasoning: missing")
		}
		if strings.Contains(log, "[…]") {
			t.Error("short response not sampled: gap markers found but response is short")
		}
		if !strings.Contains(log, "REDACTED") || strings.Contains(log, "sk-abcdefghijklmnopqrstuvwxyz0123") {
			t.Error("secret redacted in body: REDACTED missing or raw secret present")
		}
		if strings.Contains(log, "tokens: ") {
			t.Error("no tokens field w/o usage: 'tokens:' line present but no usage in transcript")
		}
	})

	// ── Case 3: transcript WITH per-message usage + model ────────────────────
	t3 := filepath.Join(t.TempDir(), "t3.jsonl")
	lines3 := []string{
		`{"type":"user","message":{"content":[{"type":"text","text":"do work"}]}}`,
		`{"type":"assistant","message":{"id":"msg_aaa","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":300,"cache_read_input_tokens":400},"content":[{"type":"text","text":"first."}]}}`,
		`{"type":"assistant","message":{"id":"msg_aaa","model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":300,"cache_read_input_tokens":400},"content":[{"type":"text","text":"first, block two."}]}}`,
		`{"type":"assistant","timestamp":"2026-06-23T10:01:00.000Z","message":{"id":"msg_bbb","model":"claude-opus-4-8","usage":{"input_tokens":5,"output_tokens":42,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Summed the turn cleanly."}]}}`,
	}
	clitest.WriteFile(t, t3, strings.Join(lines3, "\n")+"\n")

	t.Run("usage and sidecar", func(t *testing.T) {
		L3 := responseLogPath(h, cwd, "tok")
		mkResponseLog(t, L3)
		fireResponse(h, t3, cwd, "tok")
		log := clitest.Read(t, L3)

		if !strings.Contains(log, "tokens: 105/242/300/400") {
			t.Errorf("meta emits summed tokens: missing 'tokens: 105/242/300/400'\n%s", log)
		}
		if !strings.Contains(log, "model: claude-opus-4-8") {
			t.Error("meta records model: missing")
		}
		st, err := os.Stat(side)
		if err != nil || st.Size() == 0 {
			t.Errorf("sidecar tokens.jsonl written: stat err=%v size=%d", err, func() int64 {
				if err != nil {
					return 0
				}
				return st.Size()
			}())
		}
		sc := clitest.Read(t, side)
		if !strings.Contains(sc, `"in": 105`) || !strings.Contains(sc, `"out": 242`) || !strings.Contains(sc, "claude-opus-4-8") {
			t.Errorf("sidecar has summed record: %s", sc)
		}
		if !strings.Contains(sc, `"auto": false`) {
			t.Errorf("sidecar marks interactive: 'auto: false' missing: %s", sc)
		}
		if !strings.Contains(sc, `"ts": "2026-06-23T10:01:00Z"`) {
			t.Errorf("sidecar ts = normalized turn ts: %s", sc)
		}
	})

	// ── Case 4: sidechain/sub-agent transcript entries ────────────────────────
	t4 := filepath.Join(t.TempDir(), "t4.jsonl")
	lines4 := []string{
		`{"type":"user","message":{"content":[{"type":"text","text":"run parent task"}]}}`,
		`{"type":"assistant","timestamp":"2026-06-23T11:00:10.000Z","message":{"id":"msg_parent_a","model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Started parent work."}]}}`,
		`{"type":"user","isSidechain":true,"message":{"content":[{"type":"text","text":"sub-agent prompt"}]}}`,
		`{"type":"assistant","timestamp":"2026-06-23T11:00:20.000Z","message":{"id":"msg_side","model":"claude-opus-4-8","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Sub-agent investigated."}]}}`,
		`{"type":"assistant","timestamp":"2026-06-23T11:00:30.000Z","message":{"id":"msg_parent_b","model":"claude-opus-4-8","usage":{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Finished parent turn."}]}}`,
	}
	clitest.WriteFile(t, t4, strings.Join(lines4, "\n")+"\n")

	t.Run("sidechain transcript", func(t *testing.T) {
		// Clear sidecar from previous subtests.
		os.Remove(side)
		L4 := responseLogPath(h, cwd, "side")
		mkResponseLog(t, L4)
		fireResponse(h, t4, cwd, "side")
		log := clitest.Read(t, L4)

		if !strings.Contains(log, "Started parent work") {
			t.Error("sidechain keeps parent head: missing")
		}
		if !strings.Contains(log, "Sub-agent investigated") {
			t.Error("sidechain keeps sub-agent text: missing")
		}
		if !strings.Contains(log, "Finished parent turn.") {
			t.Error("sidechain recap is parent final: missing")
		}
		if !strings.Contains(log, "tokens: 14/26/0/0") {
			t.Errorf("sidechain tokens include whole turn: missing 'tokens: 14/26/0/0'\n%s", log)
		}
		sc := clitest.Read(t, side)
		// Check sidecar record has the right ts, session, and token counts.
		if !strings.Contains(sc, `"ts": "2026-06-23T11:00:30Z"`) ||
			!strings.Contains(sc, `"session": "side"`) ||
			!strings.Contains(sc, `"in": 14`) ||
			!strings.Contains(sc, `"out": 26`) {
			t.Errorf("sidechain sidecar ts is final parent response: %s", sc)
		}
	})

	// ── Case 5: Codex transcript shape ────────────────────────────────────────
	tc := filepath.Join(t.TempDir(), "tc.jsonl")
	linesC := []string{
		`{"timestamp":"2026-06-29T19:32:05.000Z","type":"session_meta","payload":{"id":"codex-session","cwd":"/tmp/repo","source":"exec"}}`,
		`{"timestamp":"2026-06-29T19:32:05.500Z","type":"turn_context","payload":{"turn_id":"codexturn","model":"gpt-5.5","cwd":"/tmp/repo"}}`,
		`{"timestamp":"2026-06-29T19:32:06.000Z","type":"event_msg","payload":{"type":"user_message","message":"do codex work"}}`,
		`{"timestamp":"2026-06-29T19:32:06.500Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>continue</name>\n<path>/x/continue/SKILL.md</path>\n---\nname: continue\n"}]}}`,
		`{"timestamp":"2026-06-29T19:32:07.000Z","type":"event_msg","payload":{"type":"exec_command_begin"}}`,
		`{"timestamp":"2026-06-29T19:32:08.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checked the repo and made the change."}],"phase":"final_answer"}}`,
		`{"timestamp":"2026-06-29T19:32:09.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":5,"reasoning_output_tokens":0,"total_tokens":15}}}}`,
		`{"timestamp":"2026-06-29T19:32:09.500Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":11,"cached_input_tokens":6,"output_tokens":7,"reasoning_output_tokens":2,"total_tokens":18}}}}`,
		`{"timestamp":"2026-06-29T19:32:10.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"codexturn","last_agent_message":"Checked the repo and made the change.","completed_at":1782761530}}`,
	}
	clitest.WriteFile(t, tc, strings.Join(linesC, "\n")+"\n")

	t.Run("codex transcript shape", func(t *testing.T) {
		os.Remove(side)
		LC := responseLogPath(h, cwd, "codexturn")
		mkResponseLog(t, LC)
		fireCodexResponse(h, tc, cwd, "codexturn", "Checked the repo and made the change.")
		log := clitest.Read(t, LC)

		if !strings.Contains(log, "Checked the repo and made the change.") {
			t.Error("codex recap appended: missing")
		}
		if !strings.Contains(log, "Bash×1") {
			t.Errorf("codex tool meta recorded: 'Bash×1' missing\n%s", log)
		}
		if !strings.Contains(log, "Skill:continue×1") {
			t.Errorf("codex names the skill run: 'Skill:continue×1' missing\n%s", log)
		}
		if !strings.Contains(log, "tokens: 11/12/0/10") {
			t.Errorf("codex tokens recorded: missing 'tokens: 11/12/0/10'\n%s", log)
		}
		if !strings.Contains(log, "model: gpt-5.5") {
			t.Error("codex model recorded: missing")
		}
		sc := clitest.Read(t, side)
		if !strings.Contains(sc, `"session": "codexturn"`) ||
			!strings.Contains(sc, `"in": 11`) ||
			!strings.Contains(sc, "gpt-5.5") {
			t.Errorf("codex sidecar written: %s", sc)
		}
	})

	// ── Case 2: long response (> cap) -> head sampled, tail dropped ──────────
	t.Run("long response sampled", func(t *testing.T) {
		// Build a ~6k text: HEADMARKER + filler + ENDMARKER_DROPPED + final sentence.
		filler := strings.Repeat("lorem ipsum dolor sit amet ", 215) // ~6k chars
		longtext := fmt.Sprintf("HEADMARKER opening framing. %s\n\nENDMARKER_DROPPED in its own paragraph near the end.\n\nFinal sampling sentence here.", filler)

		t2 := filepath.Join(t.TempDir(), "t2.jsonl")
		asst, _ := json.Marshal(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": longtext},
				},
			},
		})
		lines2 := []string{
			`{"type":"user","message":{"content":[{"type":"text","text":"do a big thing"}]}}`,
			string(asst),
		}
		clitest.WriteFile(t, t2, strings.Join(lines2, "\n")+"\n")

		L2 := responseLogPath(h, cwd, "long")
		mkResponseLog(t, L2)
		fireResponse(h, t2, cwd, "long")
		log := clitest.Read(t, L2)

		if !strings.Contains(log, "[…]") {
			t.Error("samples long response: gap markers '[…]' missing")
		}
		if !strings.Contains(log, "HEADMARKER opening framing") {
			t.Error("sample keeps head: HEADMARKER missing")
		}
		if strings.Contains(log, "ENDMARKER_DROPPED") {
			t.Error("sample drops tail region: ENDMARKER_DROPPED found")
		}
		if !strings.Contains(log, "Final sampling sentence here.") {
			t.Error("recap still final sentence: missing")
		}
		fi, err := os.Stat(L2)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() >= 5500 {
			t.Errorf("sample is bounded (<5k): log is %d bytes", fi.Size())
		}
	})
}
