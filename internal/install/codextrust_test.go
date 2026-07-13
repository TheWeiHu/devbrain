package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

// Golden fingerprints captured from a real ~/.codex/config.toml after Codex
// 0.138.0 trusted these exact hook definitions — writing these values
// restored hook execution end-to-end, so they pin Codex's real algorithm,
// not just our port of it. The binary path is part of the fingerprint.
func TestCodexHookHashGolden(t *testing.T) {
	t.Parallel()
	const bin = "/opt/homebrew/bin/devbrain"
	cases := []struct {
		label   string
		matcher *string
		command string
		want    string
	}{
		{"user_prompt_submit", nil, "DEVBRAIN_HARNESS=codex " + bin + " hook capture",
			"sha256:a068aff826b460ede0a64795493566f96c9a19b2e4cae443aee29c6c0eed07fe"},
		{"post_tool_use", strPtr("Bash"), "DEVBRAIN_HARNESS=codex " + bin + " hook gbrain",
			"sha256:435eda5ff351a2a1b61e45cb973171747ac614580510b37e0faf1eaad7102446"},
		{"stop", strPtr("ignored — stop strips matchers"), "DEVBRAIN_HARNESS=codex " + bin + " hook response",
			"sha256:e1c1840f41fd6bb881404e6eefb0bcd9500e41c90967808b4da549af2a0d18f3"},
		{"session_start", strPtr("startup|resume"), "DEVBRAIN_HARNESS=codex " + bin + " hook session-start",
			"sha256:e24167505f9724ec745f2fe8597c241f100f06dded12b9f53a27022c4bad8065"},
	}
	for _, c := range cases {
		got := codexHookHash(c.label, c.matcher, codexHandler{Type: "command", Command: c.command})
		if got != c.want {
			t.Errorf("codexHookHash(%s) = %s, want %s", c.label, got, c.want)
		}
	}
}

func TestTrustCodexHooks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hooksJSON := filepath.Join(dir, "hooks.json")
	configTOML := filepath.Join(dir, "config.toml")
	os.WriteFile(hooksJSON, []byte(`{"hooks":{
		"UserPromptSubmit":[{"hooks":[{"type":"command","command":"DEVBRAIN_HARNESS=codex /opt/homebrew/bin/devbrain hook capture"}]}],
		"Stop":[{"hooks":[{"type":"command","command":"DEVBRAIN_HARNESS=codex /opt/homebrew/bin/devbrain hook response"}]}],
		"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/tmp/devbrain-lookalike hook thing"}]}]
	}}`), 0o644)
	registered := map[string]bool{
		"DEVBRAIN_HARNESS=codex /opt/homebrew/bin/devbrain hook capture":  true,
		"DEVBRAIN_HARNESS=codex /opt/homebrew/bin/devbrain hook response": true,
	}
	stale := "[hooks.state." + `"` + hooksJSON + `:stop:0:0"` + "]\n" +
		"trusted_hash = \"sha256:stale\"\nenabled = true\n"
	os.WriteFile(configTOML, []byte("model = \"gpt-5.5\"\n\n[features]\nhooks = true\n\n"+stale), 0o644)

	n, err := trustCodexHooks(hooksJSON, configTOML, registered)
	if err != nil || n != 2 {
		t.Fatalf("trustCodexHooks = %d, %v; want 2 devbrain hooks stamped", n, err)
	}
	got, _ := os.ReadFile(configTOML)
	text := string(got)
	// untouched content survives
	for _, keep := range []string{"model = \"gpt-5.5\"", "[features]", "hooks = true", "enabled = true"} {
		if !strings.Contains(text, keep) {
			t.Errorf("existing config content %q was lost:\n%s", keep, text)
		}
	}
	if strings.Contains(text, "sha256:stale") {
		t.Errorf("stale trusted_hash not replaced:\n%s", text)
	}
	// stop: replaced in the existing table; capture: appended as a new table
	if strings.Count(text, "trusted_hash = \"sha256:") != 2 {
		t.Errorf("want exactly 2 trusted_hash entries:\n%s", text)
	}
	if !strings.Contains(text, hooksJSON+":user_prompt_submit:0:0") {
		t.Errorf("missing appended state table for capture hook:\n%s", text)
	}
	// the lookalike hook this run did not register must not be trusted
	if strings.Contains(text, "post_tool_use") {
		t.Errorf("unregistered hook was stamped:\n%s", text)
	}

	// idempotent: a second run changes nothing
	n2, err := trustCodexHooks(hooksJSON, configTOML, registered)
	if err != nil || n2 != 2 {
		t.Fatalf("second trustCodexHooks = %d, %v", n2, err)
	}
	again, _ := os.ReadFile(configTOML)
	if string(again) != text {
		t.Errorf("second run not idempotent:\n--- first:\n%s\n--- second:\n%s", text, string(again))
	}
}

func TestTrustCodexHooksCreatesConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hooksJSON := filepath.Join(dir, "hooks.json")
	os.WriteFile(hooksJSON, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/usr/local/bin/devbrain hook response"}]}]}}`), 0o644)
	configTOML := filepath.Join(dir, "config.toml")
	registered := map[string]bool{"/usr/local/bin/devbrain hook response": true}
	if n, err := trustCodexHooks(hooksJSON, configTOML, registered); err != nil || n != 1 {
		t.Fatalf("trustCodexHooks = %d, %v", n, err)
	}
	got, _ := os.ReadFile(configTOML)
	if !strings.Contains(string(got), "trusted_hash = \"sha256:") {
		t.Errorf("config.toml not created with trust entry:\n%s", got)
	}
}
