// Package task is the one parsed read model of a TODO task file, shared by
// the todo CLI and the queue server. Reading follows queue.py's parse
// (frontmatter.Parse). Writing stays split on purpose: the todo CLI edits
// fields surgically (frontmatter.SetField) and the queue server rewrites the
// whole file (frontmatter.Render) — each preserves its own frozen on-disk
// bytes, pinned by testdata/golden/.
package task

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/frontmatter"
	"github.com/TheWeiHu/devbrain/internal/pytext"
)

// Statuses is the fixed kanban column set.
var Statuses = []string{"open", "taken", "review", "held", "done"}

// Fixed-set (--only) fence marker. Shared here so both writers agree on it: the
// orchestrator parks out-of-set tasks at boot, and the todo CLI parks tasks
// ADDED mid-run — both must be prefix-matched (FenceMark) and repo-tagged
// (FenceNote) so only the tagging run's Unfence releases them.
const (
	FenceMark = "fixed-set: parked"
	fenceBy   = " by "
	fenceTail = " — while nightshift runs your selected tasks — auto-released when it finishes"
)

// FenceNote builds a fixed-set park reason tagged with the run's repo checkout
// path, so a concurrent fleet on a DIFFERENT checkout releases only its own holds.
func FenceNote(repo string) string { return FenceMark + fenceBy + repo + fenceTail }

// FenceRepo extracts the checkout path a fence reason was tagged with, or "" for
// an untagged (legacy / self-heal) marker any run may release.
func FenceRepo(reason string) string {
	rest, ok := strings.CutPrefix(reason, FenceMark+fenceBy)
	if !ok {
		return ""
	}
	if i := strings.Index(rest, fenceTail); i >= 0 {
		return rest[:i]
	}
	return rest
}

// Task is the parsed view of one task file (JSON field names pinned by the
// dashboard + testdata/golden/api/todos.json).
type Task struct {
	ID        string   `json:"id"`
	Project   string   `json:"project"`
	Status    string   `json:"status"`
	Priority  int      `json:"priority"`
	Created   string   `json:"created"`
	ClaimedBy string   `json:"claimed_by"`
	PR        string   `json:"pr"`
	Reason    string   `json:"reason"`
	DoneAt    string   `json:"done_at"`
	Approved  bool     `json:"approved"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Order     []string `json:"_order"`

	// raw is the parsed frontmatter verbatim (unexported — not serialized).
	// Raw exposes it so writers can preserve keys the struct doesn't model
	// (claimed_at, last_failure…) and readers can see a field's exact string
	// (a blank priority stays blank instead of becoming 0).
	raw map[string]string
}

// Raw returns the verbatim frontmatter value for key, "" when absent.
func (t *Task) Raw(key string) string { return t.raw[key] }

// Parse builds the Task view of one file's content (queue.py Queue.parse).
// Status defaults to open only when the key is absent.
func Parse(content, project string) *Task {
	fmTask := frontmatter.Parse(content)
	pr := 0
	if v, ok := fmTask.FM["priority"]; ok && v != "" {
		if n, err := pytext.Int(v); err == nil {
			pr = n
		}
	}
	get := func(k string) string { return fmTask.FM[k] }
	status := fmTask.FM["status"]
	if _, ok := fmTask.FM["status"]; !ok {
		status = "open"
	}
	order := fmTask.Order
	if order == nil {
		order = []string{}
	}
	return &Task{
		ID: get("id"), Project: project, Status: status, Priority: pr,
		Created: get("created"), ClaimedBy: get("claimed_by"), PR: get("pr"),
		Reason: get("reason"), DoneAt: get("done_at"),
		Approved: strings.ToLower(get("approved")) == "true",
		Title:    fmTask.Title, Body: fmTask.Body, Order: order,
		raw: fmTask.FM,
	}
}

// Load reads and parses one task file; the id falls back to the filename
// stem when the frontmatter has none.
func Load(path, project string) (*Task, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	t := Parse(string(raw), project)
	if t.ID == "" {
		base := filepath.Base(path)
		t.ID = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return t, nil
}
