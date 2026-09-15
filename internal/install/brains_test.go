package install

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/config"
)

func TestPartitionPreferenceRefreshRemovesGlobalImportsWithBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("DEVBRAIN_DATA", "")
	t.Setenv("DEVBRAIN_BRAIN", "")
	one, two := filepath.Join(home, "personal"), filepath.Join(home, "work")
	for _, p := range []string{one, two} {
		os.MkdirAll(filepath.Join(p, ".git"), 0o755)
		os.MkdirAll(filepath.Join(p, "preferences"), 0o755)
		os.WriteFile(filepath.Join(p, "preferences", "global.md"), []byte("private-sentinel"), 0o644)
	}
	if err := config.Write(one); err != nil {
		t.Fatal(err)
	}
	claude, codex := filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".codex", "AGENTS.md")
	for _, p := range []string{claude, codex} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("Keep This User Instruction\n"), 0o644)
	}
	if err := writeMarkerBlock(claude, claudeMdBody(one)); err != nil {
		t.Fatal(err)
	}
	if err := writeMarkerBlock(codex, agentsMdBody(one, "private-sentinel")); err != nil {
		t.Fatal(err)
	}
	if rc := LinkPreferences(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatal("link failed")
	}
	originals := map[string][]byte{}
	for _, p := range []string{claude, codex} {
		originals[p], _ = os.ReadFile(p)
	}
	if err := config.RegisterBrain("work", two); err != nil {
		t.Fatal(err)
	}
	RefreshAgentsPrefs()
	RefreshAgentsPrefs()
	for _, p := range []string{claude, codex} {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "Keep This User Instruction") || !strings.Contains(string(body), "devbrain data-dir") {
			t.Fatalf("missing scoped instructions: %s", body)
		}
		if strings.Contains(string(body), "private-sentinel") || strings.Contains(string(body), prefMarker) {
			t.Fatalf("global preferences leaked: %s", body)
		}
		backup, err := os.ReadFile(p + ".before-brains")
		if err != nil || string(backup) != string(originals[p]) {
			t.Fatalf("original not recoverable: %s: %v", p, err)
		}
	}
}
