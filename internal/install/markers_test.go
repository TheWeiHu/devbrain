package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/config"
)

func TestMarkerBodiesLimitMidSessionDistill(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		distill         string
		continueCommand string
	}{
		{name: "Claude", body: claudeMdBody("~/devbrain-data"), distill: "/distill", continueCommand: "/continue"},
		{name: "Codex", body: agentsMdBody("~/devbrain-data", ""), distill: "$distill", continueCommand: "$continue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Join(strings.Fields(tt.body), " ")
			for _, forbidden := range []string{"After meaningful progress", "after ordinary turns", "session is clearly ending", "ask for permission"} {
				if strings.Contains(body, forbidden) {
					t.Errorf("marker contains proactive or ambiguous distill trigger %q", forbidden)
				}
			}
			for _, want := range []string{
				"At the start of a session, or when the user explicitly asks",
				tt.continueCommand + "` to pull this project's brain and refresh the live world; it includes `" + tt.distill,
				"Never initiate `" + tt.distill + "` proactively",
				"An explicit `" + tt.continueCommand + "` or `" + tt.distill + "` invocation is already consent: run it immediately without asking again",
				"Do not infer consent from progress, a final response, a session boundary, a commit, or a PR being created or merged",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("marker missing %q:\n%s", want, body)
				}
			}
		})
	}
}

// RefreshAgentsPrefs: inlines the current preferences page into an existing
// AGENTS.md devbrain block, is byte-idempotent, caps oversized pages, and
// never creates AGENTS.md for a --without codex install.
func TestRefreshAgentsPrefs(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	codex := filepath.Join(home, ".codex")
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("DEVBRAIN_DATA", data)

	md := filepath.Join(codex, "AGENTS.md")

	t.Run("no AGENTS.md -> nothing created", func(t *testing.T) {
		RefreshAgentsPrefs()
		if _, err := os.Stat(md); err == nil {
			t.Fatal("RefreshAgentsPrefs created AGENTS.md")
		}
	})

	// Seed a block without prefs (install before the page existed).
	if err := writeMarkerBlock(md, agentsMdBody("~/devbrain-data", "")); err != nil {
		t.Fatal(err)
	}

	t.Run("prefs page inlined", func(t *testing.T) {
		prefs := filepath.Join(data, "preferences", "global.md")
		if err := os.MkdirAll(filepath.Dir(prefs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(prefs, []byte("- No warm colors.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		RefreshAgentsPrefs()
		got, _ := os.ReadFile(md)
		if !strings.Contains(string(got), "## Global preferences") ||
			!strings.Contains(string(got), "No warm colors.") {
			t.Errorf("prefs not inlined:\n%s", got)
		}
	})

	t.Run("second refresh is a no-op write", func(t *testing.T) {
		before, _ := os.Stat(md)
		RefreshAgentsPrefs()
		after, _ := os.Stat(md)
		if !after.ModTime().Equal(before.ModTime()) {
			t.Error("unchanged refresh rewrote AGENTS.md")
		}
	})

	t.Run("oversized page capped", func(t *testing.T) {
		big := strings.Repeat("x", config.PrefsCapBytes+500)
		if err := os.WriteFile(filepath.Join(data, "preferences", "global.md"), []byte(big), 0o644); err != nil {
			t.Fatal(err)
		}
		RefreshAgentsPrefs()
		got, _ := os.ReadFile(md)
		if !strings.Contains(string(got), strings.Repeat("x", config.PrefsCapBytes)) {
			t.Error("capped prefs section missing")
		}
		if strings.Contains(string(got), strings.Repeat("x", config.PrefsCapBytes+1)) {
			t.Error("prefs section not capped at PrefsCapBytes")
		}
	})

	t.Run("user content outside the block preserved", func(t *testing.T) {
		raw, _ := os.ReadFile(md)
		userNote := "my own codex notes\n\n"
		if err := os.WriteFile(md, []byte(userNote+string(raw)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "preferences", "global.md"), []byte("- fresh steer\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		RefreshAgentsPrefs()
		got, _ := os.ReadFile(md)
		if !strings.HasPrefix(string(got), userNote) || !strings.Contains(string(got), "fresh steer") {
			t.Errorf("user content lost or prefs stale:\n%s", got)
		}
	})
}
