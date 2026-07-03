package frontmatter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `---
id: 0003-paginate-results
status: review
priority: 70
created: 2026-06-19T11:00:00Z
claimed_by: dev@laptop
claimed_at: 2026-06-20T13:00:00Z
pr: https://github.com/fix/demo/pull/88
---

# Paginate results

50 rows per page with cursor links.

## Context (synthesized 2026-06-20T13:05:00Z)

Prior art in table.go; the API already supports cursors.
`

func TestSetFieldReplaceInPlace(t *testing.T) {
	t.Parallel()
	got := SetField(sample, "status", "done")
	// the one field updated, all other lines byte-identical
	if strings.Replace(sample, "status: review", "status: done", 1) != got {
		t.Error("SetField touched unrelated lines")
	}
}

func TestSetFieldInsertsBeforeClosingFence(t *testing.T) {
	t.Parallel()
	got := SetField(sample, "done_at", "2026-06-21T00:00:00Z")
	lines := strings.Split(got, "\n")
	// the new field must be the line right before the SECOND fence
	var fenceIdx []int
	for i, l := range lines {
		if l == "---" {
			fenceIdx = append(fenceIdx, i)
		}
	}
	if len(fenceIdx) < 2 || lines[fenceIdx[1]-1] != "done_at: 2026-06-21T00:00:00Z" {
		t.Errorf("insertion position wrong:\n%s", got)
	}
}

func TestSetFieldIgnoresBodyLines(t *testing.T) {
	t.Parallel()
	// a body line that looks like a field must never be rewritten
	text := sample + "\nstatus: fake-body-line\n"
	got := SetField(text, "status", "held")
	if !strings.Contains(got, "status: fake-body-line") {
		t.Error("body line was modified")
	}
	if Parse(got).FM["status"] != "held" {
		t.Error("frontmatter not updated")
	}
}

func TestParseFixtureFiles(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "testdata", "dashboard-fixture", "projects", "fix__demo", "todo")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		n++
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		task := Parse(string(b))
		id := strings.TrimSuffix(e.Name(), ".md")
		if task.FM["id"] != id {
			t.Errorf("%s: id = %q", e.Name(), task.FM["id"])
		}
		if task.Title == "" {
			t.Errorf("%s: empty title", e.Name())
		}
		if len(task.Order) == 0 || task.Order[0] != "id" {
			t.Errorf("%s: order starts with %v", e.Name(), task.Order)
		}
	}
	if n < 5 {
		t.Fatalf("expected fixture tasks, found %d", n)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	t.Parallel()
	task := Parse("just some text\nwith lines")
	if task.Body != "just some text\nwith lines" || len(task.Order) != 0 {
		t.Errorf("bad no-frontmatter parse: %+v", task)
	}
}

func TestRenderShape(t *testing.T) {
	t.Parallel()
	got := Render([]string{"id", "status"}, map[string]string{"id": "0001-x", "status": "open", "pr": "url"},
		[]string{"pr"}, "My title", "body line\n\n")
	want := "---\nid: 0001-x\nstatus: open\npr: url\n---\n\n# My title\n\nbody line\n"
	if got != want {
		t.Errorf("Render:\n got %q\nwant %q", got, want)
	}
}

func TestFenceTolerance(t *testing.T) {
	t.Parallel()
	// awk fence is /^---[[:space:]]*$/ — trailing spaces allowed
	text := "--- \nid: x\n---\t\n\n# T\n"
	want := "--- \nid: y\n---\t\n\n# T\n"
	if got := SetField(text, "id", "y"); got != want {
		t.Errorf("fence with trailing ws:\n got %q\nwant %q", got, want)
	}
}
