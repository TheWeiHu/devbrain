package hooks_test

// Go-native port of scripts/test-capture.sh: black-box CLI tests for
// `devbrain hook capture` — synthetic-prompt filtering, redaction, and
// the Codex harness variant.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// capturePayload builds the JSON stdin for hook capture (session_id variant).
func capturePayload(prompt, cwd, sessionID string) string {
	b, _ := json.Marshal(map[string]any{
		"prompt":     prompt,
		"cwd":        cwd,
		"session_id": sessionID,
	})
	return string(b)
}

func TestCaptureCLI(t *testing.T) {
	h := clitest.New(t)
	workdir := t.TempDir()

	run := func(payload string) clitest.Result {
		return h.RunWith(clitest.RunOpts{Stdin: payload}, "hook", "capture")
	}

	// A synthetic (injected) prompt with zero user content -> skipped entirely.
	syntheticPrompt := "<system-reminder>\ninjected host noise, no user authorship</system-reminder>"
	run(capturePayload(syntheticPrompt, workdir, "sess1"))
	logFiles := clitest.Find(t, h.Data, "*.md")
	if len(logFiles) != 0 {
		t.Errorf("synthetic prompt writes nothing: got %d log file(s)", len(logFiles))
	}

	// A real prompt carrying a fake secret -> captured, secret redacted.
	realPrompt := "fix the bug; key sk-abcdefghijklmnopqrstuvwxyz0123 here"
	run(capturePayload(realPrompt, workdir, "sess1"))
	logFiles = clitest.Find(t, h.Data, "*.md")
	if len(logFiles) == 0 {
		t.Fatal("real prompt must be captured: no log file found")
	}
	log := clitest.Read(t, logFiles[0])

	if !strings.Contains(log, "fix the bug") {
		t.Error("real prompt captured: log missing 'fix the bug'")
	}
	if !strings.Contains(log, "authoritative deduped source is projects/<proj>/tokens.jsonl") {
		t.Error("header carries cost caveat: missing cost note")
	}
	if strings.Contains(log, "system-reminder") {
		t.Error("no synthetic leaked: 'system-reminder' found in log")
	}
	if !strings.Contains(log, "REDACTED") || strings.Contains(log, "sk-abcdefghijklmnopqrstuvwxyz0123") {
		t.Error("secret redacted: REDACTED missing or raw secret still present")
	}

	// A prompt that merely embeds the user's text inside a harness wrapper is still
	// a real prompt: capture it WHOLE (no per-harness special-casing; bias toward keeping).
	wrappedPrompt := "<system_instruction>\nYou are working inside some harness\n</system_instruction>\n\nship the wrapped feature"
	run(capturePayload(wrappedPrompt, workdir, "sess1"))
	log = clitest.Read(t, logFiles[0]) // same session, same file
	if !strings.Contains(log, "ship the wrapped feature") || !strings.Contains(log, "system_instruction") {
		t.Error("wrapped prompt captured whole: missing expected content")
	}

	// Codex uses the same markdown log layout; only the header metadata names the harness.
	codexPayload, _ := json.Marshal(map[string]any{
		"prompt":  "codex should land in the same log shape",
		"cwd":     workdir,
		"turn_id": "codexsess",
	})
	h.RunWith(clitest.RunOpts{
		Stdin: string(codexPayload),
		Env:   map[string]string{"DEVBRAIN_HARNESS": "codex"},
	}, "hook", "capture")

	// Find the codexsess log file.
	var codexLog string
	for _, f := range clitest.Find(t, h.Data, "*.md") {
		if strings.Contains(f, "codexsess") {
			codexLog = f
			break
		}
	}
	if codexLog == "" {
		t.Fatal("codex prompt captured in same log shape: no codexsess log file found")
	}
	cl := clitest.Read(t, codexLog)
	if !strings.Contains(cl, "codex should land in the same log shape") {
		t.Error("codex prompt captured in same log shape: content missing")
	}
	if !strings.Contains(cl, "agent: codex") {
		t.Error("codex header labels harness: 'agent: codex' missing")
	}
}
