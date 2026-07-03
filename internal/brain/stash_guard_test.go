package brain_test

// Static port of scripts/test-continue-stash-safe.sh: asserts that no skill
// body executes `git stash -u` (which buries untracked files in the shared
// stash and is never popped) and that /continue still parks tracked WIP via
// the safe `git -C ... stash` form. No binary is invoked — pure file reads.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// reStashU mirrors the bash:
//
//	grep -E 'git .*stash[[:space:]]+(-[A-Za-z]*u|--include-untracked)'
//
// but only on non-comment lines (after stripping lines whose first
// non-whitespace content is '#', matching `grep -vE ':[[:space:]]*#'`).
var reStashU = regexp.MustCompile(`git .*stash\s+(-[A-Za-z]*u|--include-untracked)`)

// reCommentLine matches a grep line (file:linenum:content) where the content
// part (after the last colon that introduced it) is a shell comment line.
// The bash strips lines matching `:[[:space:]]*#` — i.e. the colon-separated
// suffix starts with optional spaces then '#'.
var reCommentLine = regexp.MustCompile(`:\s*#`)

// reContinueStash mirrors:
//
//	grep -qE "git -C .* stash( |$)"
var reContinueStash = regexp.MustCompile(`git -C .+ stash( |$)`)

func TestSkillsNoStashU(t *testing.T) {
	skillsDir := filepath.Join(clitest.Root(t), "assets", "skills")

	if _, err := os.Stat(skillsDir); err != nil {
		t.Fatalf("skills dir not found: %s", skillsDir)
	}

	// Collect all skill files.
	var files []string
	filepath.WalkDir(skillsDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})

	t.Run("no skill runs 'git stash -u'", func(t *testing.T) {
		var offenders []string
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, ln := range strings.Split(string(b), "\n") {
				// Mirror `grep -vE ':[[:space:]]*#'`: skip lines whose
				// non-whitespace content starts with '#' (shell comment lines).
				trimmed := strings.TrimLeft(ln, " \t")
				if strings.HasPrefix(trimmed, "#") {
					continue
				}
				if reStashU.MatchString(ln) {
					offenders = append(offenders, f+": "+ln)
				}
			}
		}
		if len(offenders) > 0 {
			t.Errorf("skill(s) run 'git stash -u' — buries untracked files in shared stash:\n  %s",
				strings.Join(offenders, "\n  "))
		}
	})

	t.Run("/continue still parks tracked WIP", func(t *testing.T) {
		continueSkill := filepath.Join(skillsDir, "continue", "SKILL.md")
		b, err := os.ReadFile(continueSkill)
		if err != nil {
			t.Fatalf("cannot read continue/SKILL.md: %v", err)
		}
		found := false
		for _, ln := range strings.Split(string(b), "\n") {
			if reContinueStash.MatchString(ln) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("/continue/SKILL.md has no 'git -C ... stash' line — tracked WIP parking removed?")
		}
	})
}
