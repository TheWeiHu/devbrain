package dashboard

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/task"
)

// fixedClock is an injected clock where queue.py used now()/date.today().
var fixedClock = func() time.Time {
	return time.Date(2026, 7, 2, 12, 30, 45, 0, time.UTC)
}

func newTestQueue(t *testing.T) *Queue {
	t.Helper()
	data := t.TempDir()
	q := New(data)
	q.Now = fixedClock
	return q
}

func writeTask(t *testing.T, q *Queue, project, id, content string) string {
	t.Helper()
	dir := filepath.Join(q.Data, "projects", project, "todo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func seedThree(t *testing.T, q *Queue) {
	t.Helper()
	writeTask(t, q, "proj__a", "0001-alpha-task",
		"---\nid: 0001-alpha-task\nstatus: open\npriority: 90\ncreated: 2026-06-01T00:00:00Z\nclaimed_by: \n---\n\n# alpha task\n\nalpha body\n")
	writeTask(t, q, "proj__a", "0002-beta-chore",
		"---\nid: 0002-beta-chore\nstatus: open\npriority: 20\ncreated: 2026-06-02T00:00:00Z\n---\n\n# beta chore\n\n")
	writeTask(t, q, "proj__b", "0001-other-proj-task",
		"---\nid: 0001-other-proj-task\nstatus: open\npriority: 50\ncreated: 2026-06-03T00:00:00Z\n---\n\n# other proj task\n\n")
}

func get(q *Queue, project, id string) *task.Task {
	for _, t := range q.AllTasks() {
		if t.ID == id && t.Project == project {
			return t
		}
	}
	return nil
}

func TestAllTasksIncludesArchiveFlagged(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	writeTask(t, q, "proj__a", "0001-live",
		"---\nid: 0001-live\nstatus: open\npriority: 50\ncreated: 2026-06-01T00:00:00Z\n---\n\n# live\n")
	// An archived card lives under todo/archive/ — served (so the dashboard can
	// still fold + search it) but flagged archived; the live one is not.
	arch := filepath.Join(q.Data, "projects", "proj__a", "todo", "archive")
	if err := os.MkdirAll(arch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(arch, "0000-old-done.md"),
		[]byte("---\nid: 0000-old-done\nstatus: done\n---\n\n# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, t := range q.AllTasks() {
		got[t.ID] = t.Archived
	}
	if len(got) != 2 {
		t.Fatalf("AllTasks ids = %v, want both live and archived served", got)
	}
	if got["0001-live"] {
		t.Error("live task flagged archived")
	}
	if !got["0000-old-done"] {
		t.Error("archived task not flagged archived")
	}
}

func TestDiscoveryAndSort(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	seedThree(t, q)
	if got := q.Projects(); !reflect.DeepEqual(got, []string{"proj__a", "proj__b"}) {
		t.Errorf("Projects() = %v", got)
	}
	tasks := q.AllTasks()
	if len(tasks) != 3 {
		t.Fatalf("AllTasks len = %d", len(tasks))
	}
	if tasks[0].Priority != 90 || tasks[1].Priority != 50 {
		t.Errorf("not sorted by priority desc: %d, %d", tasks[0].Priority, tasks[1].Priority)
	}
}

func TestWriteFieldsAndOrder(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	seedThree(t, q)
	u := &Updates{}
	u.Set("status", strp("held"))
	u.Set("priority", strp("55"))
	u.Set("reason", strp("blocked: x"))
	a, err := q.Write("proj__a", "0001-alpha-task", u, "renamed", "new\nbody")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "held" || a.Priority != 55 || a.Reason != "blocked: x" ||
		a.Title != "renamed" || !strings.Contains(a.Body, "new") {
		t.Errorf("save fields not applied: %+v", a)
	}
	raw, _ := os.ReadFile(filepath.Join(q.Data, "projects", "proj__a", "todo", "0001-alpha-task.md"))
	head := strings.Split(string(raw), "---")[1]
	if !strings.HasPrefix(strings.TrimSpace(head), "id:") {
		t.Errorf("frontmatter key order not preserved (id first):\n%s", head)
	}
	// done stamps done_at with the injected clock; moving off done clears it
	u = &Updates{}
	u.Set("status", strp("done"))
	b, err := q.Write("proj__a", "0002-beta-chore", u, "beta chore", "")
	if err != nil {
		t.Fatal(err)
	}
	if b.DoneAt != "2026-07-02T12:30:45Z" {
		t.Errorf("done_at = %q, want the injected clock stamp", b.DoneAt)
	}
	u = &Updates{}
	u.Set("status", strp("open"))
	b, _ = q.Write("proj__a", "0002-beta-chore", u, "beta chore", "")
	if b.DoneAt != "" {
		t.Errorf("done_at not cleared on leaving done: %q", b.DoneAt)
	}
}

func TestCreateAndDelete(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	seedThree(t, q)
	task, err := q.Create("proj__a", "fresh task", 33, "why")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "open" || len(q.AllTasks()) != 4 {
		t.Errorf("create: status=%q tasks=%d", task.Status, len(q.AllTasks()))
	}
	if task.ID != "0003-fresh-task" {
		t.Errorf("create id = %q, want next sequential", task.ID)
	}
	// id is the FIRST frontmatter key on a fresh file
	raw, _ := os.ReadFile(filepath.Join(q.Data, "projects", "proj__a", "todo", task.ID+".md"))
	if !strings.HasPrefix(string(raw), "---\nid: "+task.ID+"\n") {
		t.Errorf("created file not id-first:\n%s", raw)
	}
	huge, _ := q.Create("proj__a", "huge", 9999, "")
	if huge.Priority != 100 {
		t.Errorf("priority not clamped: %d", huge.Priority)
	}
	if !q.Delete("proj__a", task.ID) || get(q, "proj__a", task.ID) != nil {
		t.Error("delete should remove the file")
	}
}

func TestGuards(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	seedThree(t, q)
	u := &Updates{}
	u.Set("status", strp("open"))
	if _, err := q.Write("nope__x", "0001-alpha-task", u, "x", ""); err == nil {
		t.Error("save to unknown project must be rejected")
	}
	if _, err := q.Write("proj__a", "../../../etc/passwd", u, "x", ""); err == nil {
		t.Error("traversal id must be rejected")
	}
	if q.Delete("proj__a", "../../../etc/passwd") {
		t.Error("traversal delete must be rejected")
	}
	// a traversal PROJECT collapses to its basename, never escaping projects/
	if q.todoDir("../../etc") != "" {
		t.Error("traversal project must not resolve")
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func seedRemote(t *testing.T, q *Queue, project, url string) {
	t.Helper()
	dir := filepath.Join(q.Data, "projects", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "remote"), []byte(url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
