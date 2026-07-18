package maintenance

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDue(t *testing.T) {
	data := t.TempDir()
	proj := "owner__repo"
	pdir := filepath.Join(data, "projects", proj)
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.Local)

	// Empty repo: every pass is due.
	if got := Due(data, proj, now); len(got) != 5 {
		t.Fatalf("empty repo due = %v, want all 5", got)
	}

	// reconcile ran today -> not due; audit ran 2 days ago -> due.
	mustWrite(t, filepath.Join(pdir, "reconciled.md"), "last reconcile: 2026-07-08\n")
	mustWrite(t, filepath.Join(pdir, "audited.md"), "# audited\n\nlast audit: 2026-07-06\n")
	// archive ran 10 days ago -> not due (30-day gate).
	mustWrite(t, filepath.Join(pdir, "archived.md"), "last archive: 2026-06-28\n")
	// preferences distilled today -> not due (a 1-day gate is due once the day rolls).
	mustWrite(t, filepath.Join(data, "preferences", "edits.md"),
		"## 2026-07-01T09:00:00 · you\n\n## 2026-07-08T10:00:00 · distill\n\n```diff\n+- foo\n```\n")

	// sweep never stamped -> still due alongside audit.
	got := Due(data, proj, now)
	want := []string{"sweep", "audit"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("due = %v, want %v", got, want)
	}
	mustWrite(t, filepath.Join(pdir, "swept.md"), "last sweep: 2026-07-08\n")
	got = Due(data, proj, now)
	if len(got) != 1 || got[0] != "audit" {
		t.Fatalf("due after sweep stamp = %v, want [audit]", got)
	}
}

func TestDuePreferencesPicksNewestDistill(t *testing.T) {
	data := t.TempDir()
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.Local)
	// A newer `· you` edit must NOT reset the gate — only `· distill` counts,
	// and the newest distill (07-05) is 3 days old -> due.
	mustWrite(t, filepath.Join(data, "preferences", "edits.md"),
		"## 2026-07-05T10:00:00 · distill\n\n## 2026-07-08T11:00:00 · you\n")
	got := Due(data, "owner__repo", now)
	found := false
	for _, p := range got {
		if p == "preferences" {
			found = true
		}
	}
	if !found {
		t.Fatalf("preferences should be due (newest distill 07-05), got %v", got)
	}
}

func TestStampRoundTrip(t *testing.T) {
	data := t.TempDir()
	proj := "owner__repo"
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.Local)

	if err := Stamp(data, proj, "reconcile", now); err != nil {
		t.Fatal(err)
	}
	// After stamping today, reconcile drops out of the due set.
	for _, p := range Due(data, proj, now) {
		if p == "reconcile" {
			t.Fatalf("reconcile still due after stamp: %v", Due(data, proj, now))
		}
	}
	// File carries the canonical header the skill produced.
	b, _ := os.ReadFile(filepath.Join(data, "projects", proj, "reconciled.md"))
	if got := string(b); got != "# reconciled — /reconcile cursor for owner__repo\n\nlast reconcile: 2026-07-08\n" {
		t.Fatalf("reconciled.md = %q", got)
	}
}

func TestStampPreferencesRejected(t *testing.T) {
	if err := Stamp(t.TempDir(), "owner__repo", "preferences", time.Now()); err == nil {
		t.Fatal("stamping preferences should error")
	}
	if err := Stamp(t.TempDir(), "owner__repo", "bogus", time.Now()); err == nil {
		t.Fatal("stamping an unknown pass should error")
	}
}

func TestRunDueSatelliteSilent(t *testing.T) {
	data := t.TempDir()
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no real config leaks a role in
	t.Setenv("DEVBRAIN_ROLE", "satellite")

	var out, errb bytes.Buffer
	if code := Run([]string{"due", "owner__repo"}, &out, &errb); code != 0 {
		t.Fatalf("due exit = %d, stderr: %s", code, errb.String())
	}
	if out.String() != "" {
		t.Errorf("satellite due must print nothing, got %q", out.String())
	}

	t.Setenv("DEVBRAIN_ROLE", "")
	out.Reset()
	if code := Run([]string{"due", "owner__repo"}, &out, &errb); code != 0 {
		t.Fatalf("due exit = %d, stderr: %s", code, errb.String())
	}
	if out.String() == "" {
		t.Error("curator due must list the passes")
	}
}
