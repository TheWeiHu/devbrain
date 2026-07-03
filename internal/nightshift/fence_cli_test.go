package nightshift_test

// Port of scripts/test-nightshift-fence.sh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftFence(t *testing.T) {
	h := clitest.New(t)
	h.Project = "test__repo"

	binDir := filepath.Join(h.Data, "bin")
	clitest.WriteExec(t, filepath.Join(binDir, "claude"), "#!/usr/bin/env bash\nexit 0\n")
	h.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	base := filepath.Join(h.Data, "repo")
	clitest.Git(t, "", "init", "-q", base)

	// Local origin so derive_init's `git fetch origin` stays instant.
	rem := filepath.Join(h.Data, "rem.git")
	clitest.Git(t, "", "init", "-q", "--bare", rem)
	clitest.Git(t, base, "remote", "add", "origin", rem)

	td := filepath.Join(h.Data, "projects", "test__repo", "todo")
	if err := os.MkdirAll(td, 0o755); err != nil {
		t.Fatal(err)
	}
	mkFenceTask := func(id string, prio int, day, title string) {
		clitest.WriteFile(t, filepath.Join(td, id+".md"),
			"---\nid: "+id+"\nstatus: open\npriority: "+
				fmt.Sprintf("%d", prio)+"\ncreated: 2026-06-2"+day+"T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n# "+title+"\n")
	}
	mkFenceTask("0001-alpha", 90, "1", "Build the alpha thing")
	mkFenceTask("0002-beta", 80, "2", "Wire beta")
	mkFenceTask("0003-gamma", 70, "3", "Gamma docs")
	mkFenceTask("0004-delta", 60, "4", "Delta fix")

	only := "0002-beta,0003-gamma"

	ns := func(args ...string) clitest.Result {
		full := append([]string{"nightshift", "internal"}, args...)
		full = append(full, "--repo", base)
		return h.RunWith(clitest.RunOpts{Dir: base}, full...)
	}
	// tq: todo queries with DEVBRAIN_TODO_ONLY cleared (simulates stale installed todo).
	tq := func(args ...string) clitest.Result {
		return h.RunWith(clitest.RunOpts{
			Dir: base,
			Env: map[string]string{"DEVBRAIN_TODO_ONLY": ""},
		}, append([]string{"todo"}, args...)...)
	}
	visible := func() []string {
		r := tq("list")
		var ids []string
		for _, ln := range strings.Split(r.Stdout, "\n") {
			// Format: "  [ 90] 0001-alpha   ..."
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "[") {
				rest := trimmed[strings.Index(trimmed, "]")+1:]
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					ids = append(ids, fields[0])
				}
			}
		}
		return ids
	}
	tqShowField := func(id, field string) string {
		t.Helper()
		r := tq("show", id)
		return clitest.Field(r.Stdout, field)
	}

	// ── in-only tests ──
	r := ns("in-only", "0002-beta", "--only", only)
	if r.Code != 0 {
		t.Error("in_only matches full slug: rc != 0")
	}
	r = ns("in-only", "0003", "--only", only)
	if r.Code != 0 {
		t.Error("in_only matches bare number: rc != 0")
	}
	r = ns("in-only", "0001-alpha", "--only", only)
	if r.Code == 0 {
		t.Error("in_only rejects out-of-set: expected non-zero, got 0")
	}

	// ── parse-only caps workers and arms fixed-set mode ──
	r = ns("parse-only", "--only", only, "--workers", "3")
	out := r.Stdout + r.Stderr
	if !strings.Contains(out, "capping workers 3 → 2") {
		t.Errorf("worker count capped to task count: output %q", out)
	}
	if !strings.Contains(out, "fixed-set mode") {
		t.Errorf("fixed-set mode armed: output %q", out)
	}

	// ── before fence: all 4 open ──
	vis := visible()
	if len(vis) != 4 {
		t.Errorf("before fence: all 4 open: visible=%v", vis)
	}

	// ── fence: only the subset is visible ──
	ns("fence", "--only", only)
	vis = visible()
	if len(vis) != 2 || !containsStr(vis, "0002-beta") || !containsStr(vis, "0003-gamma") {
		t.Errorf("after fence: only subset visible: %v", vis)
	}

	r = tq("next")
	if strings.TrimSpace(r.Stdout) != "0002-beta" {
		t.Errorf("next returns a subset task: %q, want 0002-beta", r.Stdout)
	}

	if got := tqShowField("0001-alpha", "status"); got != "held" {
		t.Errorf("parked tasks are held, not open: status = %q, want held", got)
	}
	if got := tqShowField("0001-alpha", "reason"); !strings.Contains(got, "fixed-set: parked") {
		t.Errorf("park note carries the recovery marker: reason = %q", got)
	}

	// ── unfence: all 4 open again ──
	ns("unfence")
	vis = visible()
	if len(vis) != 4 {
		t.Errorf("after unfence: all 4 open again: visible=%v", vis)
	}
	if got := tqShowField("0001-alpha", "reason"); got != "" {
		t.Errorf("unfence clears the stale note: reason = %q", got)
	}

	// unfence is idempotent (no error).
	r = ns("unfence")
	if r.Code != 0 {
		t.Errorf("unfence is idempotent: rc=%d, want 0", r.Code)
	}

	// ── RECOVERY: orphaned fence hold (no file, just the marker on the task) ──
	tq("hold", "0004-delta", "fixed-set: parked while nightshift runs your selected tasks")
	if got := tqShowField("0004-delta", "status"); got != "held" {
		t.Errorf("orphaned fence hold present: status = %q, want held", got)
	}
	ns("unfence")
	if got := tqShowField("0004-delta", "status"); got != "open" {
		t.Errorf("marker-based unfence recovers it: status = %q, want open", got)
	}

	// A NON-fence human hold must NOT be touched by recovery.
	tq("hold", "0001-alpha", "blocked: needs a human decision")
	ns("unfence")
	if got := tqShowField("0001-alpha", "status"); got != "held" {
		t.Errorf("human hold survives recovery: status = %q, want held", got)
	}
	tq("release", "0001-alpha")

	// ── done_at guard: a task carrying done_at must NOT be fence-parked ──
	clitest.WriteFile(t, filepath.Join(td, "0008-donez.md"),
		"---\nid: 0008-donez\nstatus: open\npriority: 45\n"+
			"created: 2026-06-25T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n"+
			"done_at: 2026-06-25T17:00:00Z\n---\n# carries done_at but reads open\n")

	// Before fence: visible.
	vis = visible()
	if !containsStr(vis, "0008-donez") {
		t.Errorf("before fence: task with done_at is visible (open): visible=%v", vis)
	}
	ns("fence", "--only", only)
	if got := tqShowField("0008-donez", "status"); got == "held" {
		t.Error("fence does NOT park a task carrying done_at: status is held")
	}
	if got := tqShowField("0008-donez", "done_at"); got == "" {
		t.Error("its done_at survives the fence: done_at is empty")
	}
	ns("unfence")
	_ = os.Remove(filepath.Join(td, "0008-donez.md"))

	// ── wind-down: stop only when EVERY selected task is terminal ──
	stFenceTask := func(id, status, title string) {
		clitest.WriteFile(t, filepath.Join(td, id+".md"),
			"---\nid: "+id+"\nstatus: "+status+"\npriority: 50\n"+
				"created: 2026-06-25T00:00:00Z\nclaimed_by:\nclaimed_at:\npr:\n---\n# "+title+"\n")
	}
	stFenceTask("0005-rev", "review", "in review")
	stFenceTask("0006-don", "done", "merged")
	stFenceTask("0007-hel", "held", "blocked")

	r = ns("unresolved", "--only", "0005-rev,0006-don,0007-hel")
	if strings.TrimSpace(r.Stdout) != "1" {
		t.Errorf("wind-down waits on a selected review task: unresolved=%q, want 1", r.Stdout)
	}
	r = ns("unresolved", "--only", "0006-don,0007-hel")
	if strings.TrimSpace(r.Stdout) != "0" {
		t.Errorf("wind-down fires when all selected are done/held: unresolved=%q, want 0", r.Stdout)
	}
	r = ns("unresolved", "--only", "0002-beta")
	if strings.TrimSpace(r.Stdout) != "1" {
		t.Errorf("wind-down waits on a selected open task: unresolved=%q, want 1", r.Stdout)
	}

	// ── reconcile is fenced ──
	stFenceTask("0001-alpha", "taken", "leftover from a prior run")
	stFenceTask("0002-beta", "taken", "selected, pushed")

	// Create branches on origin for both.
	clitest.Git(t, base, "commit", "--allow-empty", "-qm", "base")
	clitest.Git(t, base, "branch", "-f", "todo/0001-alpha")
	clitest.Git(t, base, "branch", "-f", "todo/0002-beta")
	clitest.Git(t, base, "push", "-q", "origin", "todo/0001-alpha", "todo/0002-beta")

	mlog := filepath.Join(h.Data, "merged")
	clitest.WriteFile(t, mlog, "")
	h.RunWith(clitest.RunOpts{
		Dir: base,
		Env: map[string]string{"NIGHTSHIFT_TEST_MERGE_LOG": mlog},
	}, "nightshift", "internal", "reconcile", "--only", only, "--fixed-set", "--repo", base)

	mlogContent := clitest.Read(t, mlog)
	if !strings.Contains(mlogContent, "0002-beta") {
		t.Errorf("reconcile merges a selected pushed branch: merge log = %q", mlogContent)
	}
	if strings.Contains(mlogContent, "0001-alpha") {
		t.Errorf("reconcile skips an out-of-set pushed branch: merge log = %q", mlogContent)
	}

	clitest.WriteFile(t, mlog, "")
	h.RunWith(clitest.RunOpts{
		Dir: base,
		Env: map[string]string{"NIGHTSHIFT_TEST_MERGE_LOG": mlog},
	}, "nightshift", "internal", "reconcile", "--repo", base)
	mlogContent = clitest.Read(t, mlog)
	if !strings.Contains(mlogContent, "0001-alpha") {
		t.Errorf("unbounded reconcile still adopts the leftover: merge log = %q", mlogContent)
	}

	// ── reclaim is fenced ──
	stFenceTask("0001-alpha", "taken", "out-of-set stale claim")
	stFenceTask("0002-beta", "taken", "selected stale claim")
	h.RunWith(clitest.RunOpts{Dir: base}, "nightshift", "internal",
		"reclaim", "--only", only, "--fixed-set", "--repo", base)

	if got := tqShowField("0002-beta", "status"); got != "open" {
		t.Errorf("reclaim releases a selected stale claim: status = %q, want open", got)
	}
	if got := tqShowField("0001-alpha", "status"); got != "taken" {
		t.Errorf("reclaim leaves an out-of-set claim taken: status = %q, want taken", got)
	}
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
