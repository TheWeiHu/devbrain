package diagnostics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", remote},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReportDataRawLogsAndPendingDistill(t *testing.T) {
	data := t.TempDir()
	cwd := initRepo(t, "git@github.com:acme/widget.git")
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_GBRAIN", "gbrain-test-not-installed")
	writeFile(t, filepath.Join(data, "projects", "acme__widget", "log", "2026-07-08", "main.s1.md"),
		"# log\n\n## 10:00:00\n\nfirst\n\n## 11:00:00\n\nsecond\n")
	writeFile(t, filepath.Join(data, "projects", "acme__widget", "distilled.md"),
		"# distilled\n\n- 2026-07-08/main.s1.md - through 10:00:00\n")
	writeFile(t, filepath.Join(data, "projects", "acme__widget", "brain", "status.md"), "# Status\n")

	r := ReportData(DataOptions{CWD: cwd})
	if r.CWDProject != "acme__widget" || r.SelectedProject != "acme__widget" {
		t.Fatalf("project routing = cwd %q selected %q", r.CWDProject, r.SelectedProject)
	}
	if r.ProjectMismatch {
		t.Fatal("matching cwd project should not report mismatch")
	}
	if r.Raw.Count != 1 || r.Raw.NewestEntry != "2026-07-08 11:00:00" {
		t.Fatalf("raw report = %+v", r.Raw)
	}
	if r.Distill.PendingCount != 1 || r.Distill.Pending[0].Cursor != "10:00:00" {
		t.Fatalf("pending report = %+v", r.Distill)
	}
	if len(r.Failures) != 0 {
		t.Fatalf("unexpected failures: %v", r.Failures)
	}
	if !contains(r.Warnings, "distill has pending raw log files") || !contains(r.Warnings, "gbrain not on PATH") {
		t.Fatalf("warnings missing pending/gbrain: %v", r.Warnings)
	}
}

func TestReportDataDashboardProjectMismatch(t *testing.T) {
	data := t.TempDir()
	cwd := initRepo(t, "git@github.com:acme/widget.git")
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_GBRAIN", "gbrain-test-not-installed")
	writeFile(t, filepath.Join(data, "projects", "other__repo", "log", "2026-07-08", "main.s1.md"),
		"## 12:00:00\n\nother\n")

	r := ReportData(DataOptions{CWD: cwd, DashboardURL: "http://127.0.0.1:8799/?project=other__repo#monitor"})
	if !r.ProjectMismatch {
		t.Fatalf("expected mismatch, got %+v", r)
	}
	if r.ProjectSource != "dashboard-url" || r.SelectedProject != "other__repo" {
		t.Fatalf("selected project = %q from %q", r.SelectedProject, r.ProjectSource)
	}
	if !strings.Contains(r.Diagnosis, "selected project differs") {
		t.Fatalf("diagnosis should explain mismatch: %q", r.Diagnosis)
	}
}

func TestReportDataMissingLedgerMarksAllRawLogsPending(t *testing.T) {
	data := t.TempDir()
	cwd := initRepo(t, "git@github.com:acme/widget.git")
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_GBRAIN", "gbrain-test-not-installed")
	writeFile(t, filepath.Join(data, "projects", "acme__widget", "log", "2026-07-08", "main.s1.md"),
		"## 10:00:00\n\nfirst\n")
	writeFile(t, filepath.Join(data, "projects", "acme__widget", "log", "2026-07-09", "main.s2.md"),
		"## 12:00:00\n\nsecond\n")

	r := ReportData(DataOptions{CWD: cwd})
	if r.Distill.LedgerExists {
		t.Fatal("ledger should be missing")
	}
	if r.Distill.PendingCount != 2 {
		t.Fatalf("missing ledger should mark both files pending: %+v", r.Distill)
	}
}

func TestReportDataLedgerOnlyMarksNewerFilesPending(t *testing.T) {
	data := t.TempDir()
	cwd := initRepo(t, "git@github.com:acme/widget.git")
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_GBRAIN", "gbrain-test-not-installed")
	writeFile(t, filepath.Join(data, "projects", "acme__widget", "log", "2026-07-08", "main.s1.md"),
		"## 10:00:00\n\nfirst\n")
	writeFile(t, filepath.Join(data, "projects", "acme__widget", "log", "2026-07-09", "main.s2.md"),
		"## 12:00:00\n\nsecond\n")
	writeFile(t, filepath.Join(data, "projects", "acme__widget", "distilled.md"),
		"# distilled\n\n- 2026-07-08/main.s1.md - through 10:00:00\n- 2026-07-09/main.s2.md - through 11:00:00\n")

	r := ReportData(DataOptions{CWD: cwd})
	if r.Distill.PendingCount != 1 || r.Distill.Pending[0].RelPath != "2026-07-09/main.s2.md" {
		t.Fatalf("want only newer file pending, got %+v", r.Distill.Pending)
	}
}

func TestReportBrewMissingAndTapStates(t *testing.T) {
	t.Run("missing brew skips", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		r := ReportBrew()
		if r.Available || len(r.Failures) != 0 || !contains(r.Warnings, "brew not found") {
			t.Fatalf("missing brew report = %+v", r)
		}
	})

	t.Run("conflicted formula fails before info", func(t *testing.T) {
		bin := t.TempDir()
		tap := t.TempDir()
		writeFile(t, filepath.Join(tap, "Formula", "devbrain.rb"), "<<<<<<< ours\n=======\n>>>>>>> theirs\n")
		writeFakeBrew(t, bin, tap)
		t.Setenv("PATH", bin+":/usr/bin:/bin")
		r := ReportBrew()
		if !r.Available || !r.TapConflicted || len(r.Failures) == 0 {
			t.Fatalf("conflicted formula report = %+v", r)
		}
		if r.InfoOK {
			t.Fatal("brew info should not run after conflict marker detection")
		}
	})

	t.Run("clean formula passes", func(t *testing.T) {
		bin := t.TempDir()
		tap := t.TempDir()
		writeFile(t, filepath.Join(tap, "Formula", "devbrain.rb"), "class Devbrain < Formula\nend\n")
		writeFakeBrew(t, bin, tap)
		t.Setenv("PATH", bin+":/usr/bin:/bin")
		r := ReportBrew()
		if !r.Available || !r.InfoOK || len(r.Failures) != 0 {
			t.Fatalf("clean formula report = %+v", r)
		}
	})
}

func writeFakeBrew(t *testing.T, dir, tap string) {
	t.Helper()
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"  \"list --versions devbrain\") echo \"devbrain 1.3.6\" ;;\n" +
		"  \"--repo theweihu/devbrain\") echo \"" + tap + "\" ;;\n" +
		"  \"info theweihu/devbrain/devbrain\") echo \"stable 1.3.6\" ;;\n" +
		"  *) echo \"unexpected brew args: $*\" >&2; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
