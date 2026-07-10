package nightshift

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/redact"
)

type runEvent struct {
	At         string `json:"at"`
	RunID      string `json:"run_id"`
	Type       string `json:"type"`
	Worker     int    `json:"worker"`
	Task       string `json:"task,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Category   string `json:"category,omitempty"`
	Detail     string `json:"detail,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

func (o *Orch) prepareEvents() {
	path := o.Opt.EventsFile()
	if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
		os.Rename(path, filepath.Join(filepath.Dir(path), "events.previous.jsonl"))
	}
}

func (o *Orch) emitEvent(event runEvent) {
	event.At = time.Now().UTC().Format(time.RFC3339Nano)
	event.RunID = o.RunID
	event.Detail = boundedRedacted(event.Detail, 1000)
	b, err := json.Marshal(event)
	if err != nil {
		return
	}
	f, err := os.OpenFile(o.Opt.EventsFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	f.Write(append(b, '\n'))
	f.Close()
}

func boundedRedacted(value string, limit int) string {
	value = redact.Redact(value)
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func failureCategory(why string) string {
	switch {
	case strings.Contains(why, "merge conflict"):
		return "merge_conflict"
	case strings.Contains(why, "gate failed"):
		return "gate"
	case strings.Contains(why, "no pushed branch"):
		return "no_branch"
	case strings.Contains(why, "timed out"):
		return "timeout"
	case strings.Contains(why, "push"):
		return "git_push"
	default:
		return "worker"
	}
}

func (o *Orch) writeFailureArtifact(id, category, output string) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	dir := filepath.Join(o.Opt.Repo, ".nightshift", "failures", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	attempt := o.retryCount(id) + 1
	name := fmt.Sprintf("attempt-%d-%s.log", attempt, category)
	path := filepath.Join(dir, name)
	content := boundedRedacted(output, 16<<10)
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		return ""
	}
	return filepath.ToSlash(filepath.Join(".nightshift", "failures", id, name))
}

func worktreeTaskID(wt string) string {
	branch, _ := wtRepo(wt).Run("branch", "--show-current")
	return strings.TrimPrefix(strings.TrimSpace(branch), "todo/")
}
