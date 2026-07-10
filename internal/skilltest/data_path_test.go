package skilltest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsUseSharedDataPathResolver(t *testing.T) {
	for _, name := range []string{"audit", "continue", "distill", "journal", "reconcile", "work"} {
		name := name
		t.Run(name, func(t *testing.T) {
			path := repoPath(t, filepath.Join("assets", "skills", name, "SKILL.md"))
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(b)
			if !strings.Contains(text, `DATA="$(devbrain config data-dir)"`) {
				t.Fatal("skill does not resolve DATA through devbrain")
			}
			if strings.Contains(text, `DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"`) {
				t.Fatal("skill still carries an independent data-dir fallback")
			}
			if strings.Contains(text, `pull --rebase --autostash`) {
				t.Fatal("skill bypasses conflict-safe devbrain flush")
			}
		})
	}
}
