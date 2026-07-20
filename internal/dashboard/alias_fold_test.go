package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

// A repo rename leaves old data under the old <owner>__<repo> dir while new
// data lands under the canonical dir. With a project-aliases entry, every
// dashboard reader must fold the old dir into the canonical key at read time —
// so cost, prompts, and gbrain all heal on rename without moving any files.
func TestReadersFoldRenameAlias(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := "2026-07-01" // inside the 30d window of fixedClock (2026-07-02)

	seed := func(proj string) {
		logdir := filepath.Join(q.Data, "projects", proj, "log", day)
		if err := os.MkdirAll(logdir, 0o755); err != nil {
			t.Fatal(err)
		}
		md := "# header\n> worktree: w · cwd: /Users/x/conductor/w · times in UTC\n\n" +
			"## 10:00:00\n\nhow do we fix the parser in " + proj + "?\n"
		if err := os.WriteFile(filepath.Join(logdir, "w.sess.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
		writeLine := func(name, line string) {
			if err := os.WriteFile(filepath.Join(q.Data, "projects", proj, name), []byte(line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeLine("tokens.jsonl", `{"ts":"2026-07-01T10:00:00Z","session":"`+proj+`","model":"claude-opus-4-8","in":1000,"out":500,"turn":"`+proj+`"}`)
		writeLine("gbrain-queries.log", `{"ts":"2026-07-01T10:00:00Z","cmd":"gbrain search x","modes":["search"]}`)
	}
	seed("acme__oldname") // pre-rename dir
	seed("acme__newname") // canonical dir

	// Owner-preserving bare-name alias, mirroring `longtail = theweihu__impetuous`.
	prefs := filepath.Join(q.Data, "preferences")
	if err := os.MkdirAll(prefs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prefs, "project-aliases"), []byte("oldname = acme__newname\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	countByP := func(ps []string) map[string]int {
		m := map[string]int{}
		for _, p := range ps {
			m[p]++
		}
		return m
	}

	// Prompts: both dirs fold into the canonical key; nothing left under the old.
	var promptPs []string
	for _, r := range q.ScanPrompts(30, "") {
		promptPs = append(promptPs, r.P)
	}
	pc := countByP(promptPs)
	if pc["acme__oldname"] != 0 {
		t.Errorf("prompts: %d left under old dir, want 0", pc["acme__oldname"])
	}
	if pc["acme__newname"] != 2 {
		t.Errorf("prompts under canonical = %d, want 2 (folded)", pc["acme__newname"])
	}
	// A filter on the canonical key still sees the pre-rename prompt.
	if got := len(q.ScanPrompts(30, "acme__newname")); got != 2 {
		t.Errorf("ScanPrompts(canonical) = %d, want 2", got)
	}

	// Tokens fold too.
	var tokPs []string
	for _, r := range q.TokenUsage(30, "") {
		tokPs = append(tokPs, r.P)
	}
	tc := countByP(tokPs)
	if tc["acme__oldname"] != 0 || tc["acme__newname"] != 2 {
		t.Errorf("tokens by project = %v, want acme__newname:2, acme__oldname:0", tc)
	}

	// gbrain folds too.
	var gbPs []string
	for _, r := range q.GBrainQueries(30, "") {
		gbPs = append(gbPs, r.P)
	}
	gc := countByP(gbPs)
	if gc["acme__oldname"] != 0 || gc["acme__newname"] != 2 {
		t.Errorf("gbrain by project = %v, want acme__newname:2, acme__oldname:0", gc)
	}
}
