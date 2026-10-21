// Package queue is the Go port of the retired queue.py — the localhost
// kanban server for the TODO queue. It serves the embedded dashboard and
// reads/writes the task .md files directly, preserving frontmatter key order.
// Binds 127.0.0.1 only; never git-commits.
package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/frontmatter"
	"github.com/TheWeiHu/devbrain/internal/task"
)

// pyGet mirrors queue.py's `cur.get(k, "")` over the parse() dict during
// write(): modeled keys render from the struct (booleans like Python
// str(bool)); anything else falls back to the raw frontmatter, so a
// dashboard save PRESERVES fields like claimed_at and last_failure.
// (queue.py blanked those — cur.get(k, "") over its parse() dict — which
// silently wiped a task's lease and failure notes on every card edit;
// deliberate improvement over the legacy behavior.)
func pyGet(t *task.Task, k string) string {
	switch k {
	case "id":
		return t.ID
	case "project":
		return t.Project
	case "status":
		return t.Status
	case "priority":
		return strconv.Itoa(t.Priority)
	case "created":
		return t.Created
	case "pr":
		return t.PR
	case "reason":
		return t.Reason
	case "done_at":
		return t.DoneAt
	case "title":
		return t.Title
	case "body":
		return t.Body
	}
	return t.Raw(k) // unmodeled key -> preserved verbatim
}

// Updates is an insertion-ordered field-update map; a nil value deletes the
// field (Python's `None`).
type Updates struct {
	keys []string
	m    map[string]*string
}

// Set records an update, keeping first-insertion order (later Set on the
// same key overwrites in place, like a Python dict).
func (u *Updates) Set(key string, val *string) {
	if u.m == nil {
		u.m = map[string]*string{}
	}
	if _, ok := u.m[key]; !ok {
		u.keys = append(u.keys, key)
	}
	u.m[key] = val
}

func (u *Updates) get(key string) (*string, bool) {
	v, ok := u.m[key]
	return v, ok
}

func strp(s string) *string { return &s }

// Queue owns the task store rooted at Data. The clock is injectable so the
// queue.py tests port without sleeps.
type Queue struct {
	Data string
	Now  func() time.Time
	// promptCache incrementally memoizes the classified prompt corpus behind
	// ScanPrompts: each log file's parse is cached by its size+mtime, so a
	// Profile reload re-parses only the files that changed, not all of history.
	promptCache promptScanCache
}

// New builds a Queue with the real clock.
func New(data string) *Queue {
	return &Queue{Data: data, Now: time.Now}
}

// nowStamp is queue.py's now(): UTC seconds with a Z.
func (q *Queue) nowStamp() string {
	return q.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func (q *Queue) projectsDir() string { return filepath.Join(q.Data, "projects") }

// Projects lists every project that has a todo/ dir, sorted.
func (q *Queue) Projects() []string {
	matches, _ := filepath.Glob(filepath.Join(q.projectsDir(), "*", "todo"))
	out := []string{}
	for _, d := range matches {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			out = append(out, filepath.Base(filepath.Dir(d)))
		}
	}
	sort.Strings(out)
	return out
}

// todoDir returns the project's todo dir, or "" — existing project dirs
// only (basename() kills traversal).
func (q *Queue) todoDir(project string) string {
	safe := filepath.Base(project)
	if fi, err := os.Stat(filepath.Join(q.projectsDir(), safe)); err != nil || !fi.IsDir() {
		return ""
	}
	return filepath.Join(q.projectsDir(), safe, "todo")
}

// AllTasks parses every task across projects, sorted by (-priority, created).
// Archived tasks (moved under todo/archive/ by `todo archive`) are included and
// flagged so the dashboard can fold them separately but still search them — the
// CLI `todo list`, which reads todo/ non-recursively, is the one that skips them.
func (q *Queue) AllTasks() []*task.Task {
	out := []*task.Task{}
	dirs, _ := filepath.Glob(filepath.Join(q.projectsDir(), "*", "todo"))
	for _, d := range dirs {
		project := filepath.Base(filepath.Dir(d))
		load := func(f string, archived bool) {
			t, err := task.Load(f, project)
			if err != nil {
				t = &task.Task{ID: filepath.Base(f), Project: project, Status: "open",
					Title: "(parse error) " + err.Error(), Order: []string{}}
			}
			t.Archived = archived
			out = append(out, t)
		}
		files, _ := filepath.Glob(filepath.Join(d, "*.md"))
		for _, f := range files {
			load(f, false)
		}
		archived, _ := filepath.Glob(filepath.Join(d, "archive", "*.md"))
		for _, f := range archived {
			load(f, true)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].Created < out[j].Created
	})
	return out
}

// Write applies updates to a task file, rewriting frontmatter in original
// key order (deletions skipped, new keys appended), then title + body.
// done_at follows status: stamped on entering done, cleared on any other
// status change.
func (q *Queue) Write(project, tid string, updates *Updates, title, body string) (*task.Task, error) {
	d := q.todoDir(project)
	if d == "" {
		return nil, errors.New("unknown project")
	}
	path := filepath.Join(d, filepath.Base(tid)+".md")
	if _, err := os.Stat(path); err != nil {
		return nil, errors.New(tid)
	}
	cur, err := task.Load(path, project)
	if err != nil {
		return nil, err
	}
	if s, ok := updates.get("status"); ok && s != nil && *s == "done" {
		updates.Set("done_at", strp(q.nowStamp()))
	} else if ok && s != nil && *s != "" {
		updates.Set("done_at", nil) // clear on leaving done (no zombie)
	}
	order := cur.Order
	if len(order) == 0 {
		order = []string{"id", "status", "priority", "created"}
	}
	fm := map[string]string{}
	kept := make([]string, 0, len(order)) // original order minus deletions
	written := map[string]bool{}
	for _, k := range order {
		fm[k] = pyGet(cur, k)
		if v, ok := updates.get(k); ok && v == nil {
			continue // delete this field
		}
		kept = append(kept, k)
		written[k] = true
	}
	var newKeys []string
	for _, k := range updates.keys {
		if v := updates.m[k]; v != nil {
			fm[k] = *v
			if !written[k] {
				newKeys = append(newKeys, k)
			}
		}
	}
	content := frontmatter.Render(kept, fm, newKeys, title, body)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return task.Load(path, project)
}

var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

// Create writes a new task file with the next sequential id. The id comes
// from task.AllocID — the same locked allocator as `devbrain todo add` — so a
// dashboard create racing a CLI add can't share a prefix.
func (q *Queue) Create(project, title string, priority int, body string) (*task.Task, error) {
	d := q.todoDir(project)
	if d == "" {
		return nil, errors.New("unknown project")
	}
	slugSrc := title
	if slugSrc == "" {
		slugSrc = "task"
	}
	slug := strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(slugSrc), "-"), "-")
	if r := []rune(slug); len(r) > 50 {
		slug = string(r[:50])
	}
	if slug == "" {
		slug = "task"
	}
	tid, err := task.AllocID(d, slug)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(d, tid+".md")
	prio := priority
	if prio < 0 {
		prio = 0
	}
	if prio > 100 {
		prio = 100
	}
	if title == "" {
		title = "untitled"
	}
	content := fmt.Sprintf("---\nid: %s\nstatus: open\npriority: %d\ncreated: %s\n---\n\n# %s\n\n%s\n",
		tid, prio, q.nowStamp(), title, strings.TrimRight(body, " \t\n\r\v\f"))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return task.Load(path, project)
}

// Delete removes a task file. Traversal-safe via basename.
func (q *Queue) Delete(project, tid string) bool {
	d := q.todoDir(project)
	if d == "" {
		return false
	}
	path := filepath.Join(d, filepath.Base(tid)+".md")
	if _, err := os.Stat(path); err != nil {
		return false
	}
	abs, _ := filepath.Abs(path)
	absDir, _ := filepath.Abs(d)
	if filepath.Dir(abs) != absDir {
		return false
	}
	return os.Remove(path) == nil
}

// --- shared JSON helpers ------------------------------------------------------

func decodeJSONMap(s string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	return m, nil
}

// pyTruthy is Python bool(v) over decoded JSON values.
func pyTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}
