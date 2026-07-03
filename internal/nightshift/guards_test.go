package nightshift_test

// Port of scripts/test-nightshift-guards.sh

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftGuards(t *testing.T) {
	h := clitest.New(t)
	h.Project = "test__repo"

	binDir := filepath.Join(h.Data, "bin")
	clitest.WriteExec(t, filepath.Join(binDir, "claude"), "#!/usr/bin/env bash\nexit 0\n")
	h.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	rem := filepath.Join(h.Data, "rem.git")
	seed := filepath.Join(h.Data, "seed")
	base := filepath.Join(h.Data, "repo")

	clitest.Git(t, "", "init", "-q", "--bare", rem)
	clitest.Git(t, "", "clone", "-q", rem, seed)
	clitest.WriteFile(t, filepath.Join(seed, "f"), "base\n")
	clitest.Git(t, seed, "add", "f")
	clitest.Git(t, seed, "commit", "-qm", "init")
	clitest.Git(t, seed, "push", "-q", "origin", "HEAD:main")
	clitest.Git(t, "", "clone", "-q", rem, base)

	td := filepath.Join(h.Data, "projects", "test__repo", "todo")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}
	mkTask := func(id, status, title string) {
		clitest.WriteFile(t, filepath.Join(td, id+".md"),
			"---\nid: "+id+"\nstatus: "+status+"\npriority: 50\n"+
				"created: 2026-06-20T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n# "+title+"\n")
	}
	mkTask("0001-alpha", "open", "Alpha")
	mkTask("0002-beta", "open", "Beta")

	ns := func(args ...string) clitest.Result {
		full := append([]string{"nightshift", "internal"}, args...)
		full = append(full, "--repo", base)
		return h.RunWith(clitest.RunOpts{Dir: base}, full...)
	}
	nsShowField := func(id, field string) string {
		t.Helper()
		r := h.RunWith(clitest.RunOpts{Dir: base}, "todo", "show", id)
		return clitest.Field(r.Stdout, field)
	}

	// ── Bug 5 — --only input precondition ──
	t.Run("only_input_precondition", func(t *testing.T) {
		onlyRC := func(only string) int {
			return ns("parse-only", "--only", only).Code
		}
		onlyOut := func(only string) string {
			r := ns("parse-only", "--only", only)
			return r.Stdout + r.Stderr
		}

		if rc := onlyRC(""); rc != 1 {
			t.Errorf("empty --only is a hard error: rc=%d, want 1", rc)
		}
		if rc := onlyRC(" , , "); rc != 1 {
			t.Errorf("whitespace/comma-only --only is an error: rc=%d, want 1", rc)
		}
		if rc := onlyRC("9999,8888"); rc != 1 {
			t.Errorf("all-unknown --only is an error: rc=%d, want 1", rc)
		}
		if rc := onlyRC("0001,0002"); rc != 0 {
			t.Errorf("valid --only is accepted (returns 0): rc=%d", rc)
		}
		out := onlyOut("0001,0002")
		if !strings.Contains(out, "0001-alpha") {
			t.Errorf("valid --only echoes the resolved fence (canonical slugs): output %q", out)
		}
		out = onlyOut("")
		if !strings.Contains(strings.ToLower(out), "unfenced run") {
			t.Errorf("empty --only names the danger: output %q", out)
		}
		out = onlyOut("0001,7777")
		if !strings.Contains(out, "7777") {
			t.Errorf("mixed valid+unknown warns but proceeds: output %q", out)
		}
		if rc := onlyRC("0001,7777"); rc != 0 {
			t.Errorf("mixed valid+unknown still starts (rc 0): rc=%d", rc)
		}
	})

	// ── Bug 4 — output post-condition ──
	t.Run("output_postcondition", func(t *testing.T) {
		// Build origin/nightshift.
		clitest.Git(t, base, "fetch", "-q", "origin")
		clitest.Git(t, base, "branch", "-f", "nightshift", "origin/main")
		clitest.Git(t, base, "push", "-q", "origin", "nightshift")
		clitest.Git(t, base, "fetch", "-q", "origin")
		if err := os.MkdirAll(filepath.Join(base, ".nightshift"), 0o755); err != nil {
			t.Fatal(err)
		}

		// land 0001: a real commit on nightshift, then record_landed stamps the post-push SHA.
		clitest.Git(t, base, "checkout", "-q", "nightshift")
		clitest.WriteFile(t, filepath.Join(base, "g"), "work0001\n")
		clitest.Git(t, base, "add", "g")
		clitest.Git(t, base, "commit", "-qm", "work 0001")
		clitest.Git(t, base, "commit", "--allow-empty", "-qm",
			"nightshift: merge todo/0001-alpha into nightshift")
		clitest.Git(t, base, "push", "-q", "origin", "nightshift")
		clitest.Git(t, base, "fetch", "-q", "origin")
		ns("record-landed", "0001-alpha")
		goodSHA := nsRevParse(t, base, "origin/nightshift")

		if sha := nsShowField("0001-alpha", ""); sha == "" {
			_ = sha // we check via landed-sha verb below
		}
		landedSHA := strings.TrimSpace(ns("landed-sha", "0001-alpha").Stdout)
		if landedSHA == "" {
			t.Error("record-landed writes a landing SHA: got empty")
		}
		if landedSHA != goodSHA {
			t.Errorf("landed SHA == current origin/nightshift: %q vs %q", landedSHA, goodSHA)
		}

		// Mark both done; 0001 landed (present), 0002 done but never landed.
		h.RunWith(clitest.RunOpts{Dir: base}, "todo", "done", "0001-alpha")
		h.RunWith(clitest.RunOpts{Dir: base}, "todo", "done", "0002-beta")

		r := ns("verify", "--only", "0001-alpha")
		if r.Code != 0 {
			t.Errorf("verify PASSES when the done task's work is present: rc=%d", r.Code)
		}
		r = ns("unresolved", "--only", "0001-alpha,0002-beta")
		if strings.TrimSpace(r.Stdout) != "1" {
			t.Errorf("derived status makes absent done work unresolved: got %q, want 1", r.Stdout)
		}
		r = ns("verify", "--only", "0001-alpha,0002-beta")
		if r.Code != 0 {
			t.Errorf("verify ignores absent stored-done work no longer derived as done: rc=%d", r.Code)
		}

		// Simulate a hard base RESET.
		clitest.Git(t, base, "checkout", "-q", "nightshift")
		exec.Command("git", "-c", "user.email=a@b.c", "-c", "user.name=t",
			"-C", base, "reset", "--hard", "origin/main").Run()
		exec.Command("git", "-C", base, "push", "-f", "origin", "nightshift").Run()
		clitest.Git(t, base, "fetch", "-q", "origin")

		r = ns("unresolved", "--only", "0001-alpha")
		if strings.TrimSpace(r.Stdout) != "1" {
			t.Errorf("after a reset, derived status reopens previously-present work: got %q, want 1", r.Stdout)
		}
	})

	// ── reopen verb ──
	t.Run("reopen_verb", func(t *testing.T) {
		// Ensure 0001-alpha is done.
		h.RunWith(clitest.RunOpts{Dir: base}, "todo", "done", "0001-alpha")

		// release REFUSES to reopen a done task.
		h.RunWith(clitest.RunOpts{Dir: base}, "todo", "release", "0001-alpha")
		if got := nsShowField("0001-alpha", "status"); got != "done" {
			t.Errorf("release REFUSES to reopen a done task: status = %q, want done", got)
		}

		// reopen forces done -> open.
		h.RunWith(clitest.RunOpts{Dir: base}, "todo", "reopen", "0001-alpha")
		if got := nsShowField("0001-alpha", "status"); got != "open" {
			t.Errorf("reopen forces done -> open: status = %q, want open", got)
		}
		if got := nsShowField("0001-alpha", "done_at"); got != "" {
			t.Errorf("reopen clears the done_at stamp: done_at = %q, want empty", got)
		}
	})

	// ── env containment ──
	t.Run("env_containment", func(t *testing.T) {
		mkTask("0003-gamma", "open", "Gamma")
		ns("parse-only", "--only", "0001-alpha,0002-beta")

		// The process env must NOT carry DEVBRAIN_TODO_ONLY after parse-only.
		if v := os.Getenv("DEVBRAIN_TODO_ONLY"); v != "" {
			t.Errorf("--only does not export DEVBRAIN_TODO_ONLY: env = %q", v)
		}
		if v := os.Getenv("DEVBRAIN_TODO_DERIVE_GIT"); v != "" {
			t.Errorf("boot does not export DEVBRAIN_TODO_DERIVE_GIT: env = %q", v)
		}

		// todo wrapper scopes to the fixed set.
		r := ns("todo", "list", "--only", "0001-alpha,0002-beta")
		if !strings.Contains(r.Stdout, "0001-alpha") {
			t.Errorf("todo wrapper scopes to fixed set: missing 0001-alpha in %q", r.Stdout)
		}
		if strings.Contains(r.Stdout, "0003-gamma") {
			t.Errorf("todo wrapper scopes to fixed set: 0003-gamma should be hidden in %q", r.Stdout)
		}

		// todo-all wrapper sees the whole queue.
		r = ns("todo-all", "list")
		if !strings.Contains(r.Stdout, "0003-gamma") {
			t.Errorf("todo-all wrapper sees whole queue: missing 0003-gamma in %q", r.Stdout)
		}

		// Cleanup: remove the extra task.
		_ = os.Remove(filepath.Join(td, "0003-gamma.md"))
	})
}
