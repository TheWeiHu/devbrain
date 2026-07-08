package contextpack

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBuildQueryRanksMatchingBrainPages(t *testing.T) {
	data := t.TempDir()
	project := "owner__repo"
	writeFile(t, filepath.Join(data, "projects", project, "brain", "billing-export.md"), `# Billing export

The billing export keeps customer totals stable across retries.
`)
	writeFile(t, filepath.Join(data, "projects", project, "brain", "release-plan.md"), `# Release plan

Ship the dashboard polish first.
`)

	brief, err := Build(Options{
		DataDir:       data,
		Project:       project,
		Query:         "billing export",
		MaxPages:      5,
		MaxTodos:      5,
		MaxLogEntries: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if brief.Brain.Count != 2 {
		t.Fatalf("brain count = %d, want 2", brief.Brain.Count)
	}
	if brief.Brain.Matched != 1 {
		t.Fatalf("matched pages = %d, want 1", brief.Brain.Matched)
	}
	if got := brief.Brain.Pages[0].Slug; got != project+"/billing-export" {
		t.Fatalf("top page = %q", got)
	}
	if !strings.Contains(brief.Brain.Pages[0].Excerpt, "billing export") {
		t.Fatalf("excerpt did not include matching context: %#v", brief.Brain.Pages[0])
	}
}

func TestBuildIncludesActiveTodosAndRecentRawLogs(t *testing.T) {
	data := t.TempDir()
	project := "owner__repo"
	writeFile(t, filepath.Join(data, "projects", project, "brain", "overview.md"), `# Overview

Important project notes.
`)
	writeFile(t, filepath.Join(data, "projects", project, "todo", "0001-low.md"), taskFile("0001-low", "open", 10, "Low priority", "Later work."))
	writeFile(t, filepath.Join(data, "projects", project, "todo", "0002-high.md"), taskFile("0002-high", "open", 90, "High priority", "Do this first."))
	writeFile(t, filepath.Join(data, "projects", project, "todo", "0003-done.md"), taskFile("0003-done", "done", 100, "Done task", "Ignore me."))
	writeFile(t, filepath.Join(data, "projects", project, "log", "2026-07-07", "main.s1.md"), `# owner__repo - 2026-07-07

## 09:00:00

Older turn.

## 10:30:00

Newest turn with useful context.
`)

	brief, err := Build(Options{
		DataDir:       data,
		Project:       project,
		MaxPages:      5,
		MaxTodos:      5,
		MaxLogEntries: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if brief.TODO.ActiveCount != 2 {
		t.Fatalf("active todo count = %d, want 2", brief.TODO.ActiveCount)
	}
	if got := brief.TODO.Tasks[0].ID; got != "0002-high" {
		t.Fatalf("first todo = %q, want high-priority open task", got)
	}
	if brief.RawLogs.FileCount != 1 {
		t.Fatalf("raw log file count = %d, want 1", brief.RawLogs.FileCount)
	}
	if got := brief.RawLogs.Entries[0].At; got != "2026-07-07T10:30:00Z" {
		t.Fatalf("newest log at = %q", got)
	}
	if !strings.Contains(brief.RawLogs.Entries[0].Excerpt, "Newest turn") {
		t.Fatalf("newest log excerpt = %q", brief.RawLogs.Entries[0].Excerpt)
	}
}

func TestRunJSON(t *testing.T) {
	data := t.TempDir()
	project := "owner__repo"
	t.Setenv("DEVBRAIN_DATA", data)
	writeFile(t, filepath.Join(data, "projects", project, "brain", "overview.md"), `# Overview

Notes.
`)

	var stdout, stderr bytes.Buffer
	rc := Run([]string{"--project", project, "--json"}, &stdout, &stderr)
	if rc != 0 {
		t.Fatalf("Run rc=%d stderr=%s", rc, stderr.String())
	}
	var brief Brief
	if err := json.Unmarshal(stdout.Bytes(), &brief); err != nil {
		t.Fatalf("json output did not unmarshal: %v\n%s", err, stdout.String())
	}
	if brief.Project != project {
		t.Fatalf("project = %q", brief.Project)
	}
	if brief.Brain.Count != 1 {
		t.Fatalf("brain count = %d, want 1", brief.Brain.Count)
	}
}

func taskFile(id, status string, priority int, title, body string) string {
	return strings.TrimSpace(`---
id: `+id+`
status: `+status+`
priority: `+strconvItoa(priority)+`
created: 2026-07-07T00:00:00Z
---

# `+title+`

`+body) + "\n"
}

func strconvItoa(n int) string {
	return strconv.Itoa(n)
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
