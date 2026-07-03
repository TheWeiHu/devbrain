// Package queue is the Go port of the retired queue.py — the localhost
// kanban server for the TODO queue. It serves the embedded dashboard and
// reads/writes the task .md files directly, preserving frontmatter key order.
// Binds 127.0.0.1 only; never git-commits.
package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TheWeiHu/devbrain/internal/frontmatter"
	"github.com/TheWeiHu/devbrain/internal/procutil"
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
	case "claimed_by":
		return t.ClaimedBy
	case "pr":
		return t.PR
	case "reason":
		return t.Reason
	case "done_at":
		return t.DoneAt
	case "approved":
		if t.Approved {
			return "True"
		}
		return "False"
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

// Queue owns the task store rooted at Data. The clock and the nightshift
// side effects are injectable so the queue.py tests port without real
// processes or sleeps.
type Queue struct {
	Data string
	Now  func() time.Time
	// NightshiftHome is where dashboard-launched fleets live (a dedicated
	// clone per repo). DEVBRAIN_NIGHTSHIFT_HOME, default ~/nightshift.
	NightshiftHome string
	// Running reports whether a nightshift orchestrator is live on repo.
	Running func(repo string) bool
	// EnsureClone resolves the isolated clone for a checkout ("" repo = error note).
	EnsureClone func(checkout string) (string, string)
	// Spawn launches the detached nightshift CLI (argv, extra env KEY=VALUE).
	Spawn func(argv []string, extraEnv []string) error
}

// New builds a Queue with the real clock and process side effects.
func New(data string) *Queue {
	q := &Queue{Data: data, Now: time.Now}
	q.NightshiftHome = os.Getenv("DEVBRAIN_NIGHTSHIFT_HOME")
	if q.NightshiftHome == "" {
		home, _ := os.UserHomeDir()
		q.NightshiftHome = filepath.Join(home, "nightshift")
	}
	q.Running = nightshiftRunning
	q.EnsureClone = q.ensureNightshiftClone
	q.Spawn = spawnDetached
	return q
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
func (q *Queue) AllTasks() []*task.Task {
	out := []*task.Task{}
	dirs, _ := filepath.Glob(filepath.Join(q.projectsDir(), "*", "todo"))
	for _, d := range dirs {
		project := filepath.Base(filepath.Dir(d))
		files, _ := filepath.Glob(filepath.Join(d, "*.md"))
		for _, f := range files {
			t, err := task.Load(f, project)
			if err != nil {
				t = &task.Task{ID: filepath.Base(f), Project: project, Status: "open",
					Title: "(parse error) " + err.Error(), Order: []string{}}
			}
			out = append(out, t)
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

var leadingDigits = regexp.MustCompile(`^(\d+)`)
var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

// Create writes a new task file with the next sequential id.
func (q *Queue) Create(project, title string, priority int, body string) (*task.Task, error) {
	d := q.todoDir(project)
	if d == "" {
		return nil, errors.New("unknown project")
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return nil, err
	}
	mx := 0
	files, _ := filepath.Glob(filepath.Join(d, "*.md"))
	for _, f := range files {
		if m := leadingDigits.FindStringSubmatch(filepath.Base(f)); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > mx {
				mx = n
			}
		}
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
	tid := fmt.Sprintf("%04d-%s", mx+1, slug)
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

// Nightshift lists every project with a live fleet, forwarding each run's
// status.json. Phantom registrations (repo gone, or stopped and stale) are
// pruned off disk so dead runs clear themselves on the next poll.
func (q *Queue) Nightshift() map[string]any {
	runs := []any{}
	files, _ := filepath.Glob(filepath.Join(q.projectsDir(), "*", "nightshift-run.json"))
	sort.Strings(files)
	for _, f := range files {
		run, err := readJSONMap(f)
		if err != nil {
			continue
		}
		repo, _ := run["repo"].(string)
		status, err := readJSONMap(filepath.Join(repo, ".nightshift", "status.json"))
		if err != nil {
			if fi, serr := os.Stat(repo); serr != nil || !fi.IsDir() {
				q.pruneRun(f) // repo gone -> registration is dead
			}
			continue
		}
		if q.staleRun(status, 300) {
			q.pruneRun(f)
			continue
		}
		entry := map[string]any{"project": filepath.Base(filepath.Dir(f))}
		for k, v := range status { // {**status} overlays the computed project
			entry[k] = v
		}
		runs = append(runs, entry)
	}
	return map[string]any{"runs": runs}
}

// staleRun: a fleet that stopped without unregistering — not running AND its
// status stamp older than ttl seconds. A running fleet is kept regardless.
func (q *Queue) staleRun(status map[string]any, ttl float64) bool {
	if pyTruthy(status["running"]) {
		return false
	}
	updated, _ := status["updated"].(string)
	ts, err := parsePyISO(strings.ReplaceAll(updated, "Z", "+00:00"))
	if err != nil {
		return true // un-stamped / unparseable on a not-running run -> dead
	}
	return q.Now().UTC().Sub(ts).Seconds() > ttl
}

func (q *Queue) pruneRun(runFile string) { _ = os.Remove(runFile) }

// --- nightshift launch (drag-to-🌙) ----------------------------------------

var idShape = regexp.MustCompile(`^\d{4}`)

// StartNightshift launches a bounded fleet over the chosen task ids: resolve
// the project's local repo, sanity-check ids, refuse duplicates, spawn the
// nightshift CLI detached with NIGHTSHIFT_NO_OPEN + this dashboard's port.
func (q *Queue) StartNightshift(project string, ids []string, port int) map[string]any {
	valid := []string{}
	for _, id := range ids {
		if idShape.MatchString(id) {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return map[string]any{"error": "no valid task ids selected"}
	}
	checkout := q.ProjectRepo(project)
	if checkout == "" {
		return map[string]any{"error": fmt.Sprintf("couldn't find a local checkout for %s — run "+
			"`devbrain nightshift start <repo>` once from that repo first", project)}
	}
	// Run in a DEDICATED clone (~/nightshift/<repo>), not the active checkout.
	repo, note := q.EnsureClone(checkout)
	if repo == "" {
		return map[string]any{"error": note}
	}
	// Refuse a second fleet; an O_EXCL lock closes the double-click race the
	// CLI's own pgrep dedup leaves open.
	if q.Running(repo) {
		return map[string]any{"error": "nightshift is already running on this repo — stop it first"}
	}
	lock := filepath.Join(repo, ".nightshift", "launch.lock")
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err == nil {
		fd, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fd.Close()
		} else if os.IsExist(err) {
			if fi, serr := os.Stat(lock); serr == nil &&
				q.Now().UTC().Sub(fi.ModTime().UTC()).Seconds() < 30 {
				return map[string]any{"error": "a nightshift launch is already starting on this repo"}
			}
			now := q.Now()
			_ = os.Chtimes(lock, now, now) // stale lock from a crashed start -> reclaim
		}
	} // lock is best-effort; the Running guard above is the primary defense
	argv := []string{"nightshift", "start", repo, "--only", strings.Join(valid, ",")}
	env := []string{"NIGHTSHIFT_NO_OPEN=1", "DEVBRAIN_QUEUE_PORT=" + strconv.Itoa(port)}
	if err := q.Spawn(argv, env); err != nil {
		return map[string]any{"error": "could not launch nightshift: " + err.Error()}
	}
	idsAny := make([]any, len(valid))
	for i, v := range valid {
		idsAny[i] = v
	}
	return map[string]any{"ok": true, "repo": repo, "note": note, "ids": idsAny, "count": len(valid)}
}

// StopNightshift halts the fleet running on a project's repo: use the repo the
// run registration recorded (the dashboard's runs list is built from it, so it
// names where the fleet actually runs — clone or checkout), falling back to the
// /api/nightshift/resolve resolution, then self-exec `nightshift stop` to reuse
// cliStop's full reap (signal orchestrator, release claims, kill workers).
func (q *Queue) StopNightshift(project string) map[string]any {
	repo := ""
	if run, err := readJSONMap(filepath.Join(q.projectsDir(), project, "nightshift-run.json")); err == nil {
		repo, _ = run["repo"].(string)
	}
	if repo == "" {
		checkout := q.ProjectRepo(project)
		if checkout == "" {
			return map[string]any{"error": fmt.Sprintf("couldn't find a local checkout for %s", project)}
		}
		if repo = q.NightshiftClonePath(checkout); repo == "" {
			repo = checkout
		}
	}
	if err := q.Spawn([]string{"nightshift", "stop", repo}, nil); err != nil {
		return map[string]any{"error": "could not stop nightshift: " + err.Error()}
	}
	return map[string]any{"ok": true, "repo": repo}
}

// ScaleNightshift changes the worker count on a RUNNING fleet by writing the
// desired-workers control file the orchestrator re-reads each tick — no restart.
// Resolves the run's repo from its registration (as StopNightshift does). Floors
// at 1 but stores the request UNCLAMPED: the orchestrator caps to live work each
// tick, so a target above today's queue still applies when more tasks arrive.
func (q *Queue) ScaleNightshift(project string, workers int) map[string]any {
	repo := ""
	if run, err := readJSONMap(filepath.Join(q.projectsDir(), project, "nightshift-run.json")); err == nil {
		repo, _ = run["repo"].(string)
	}
	if repo == "" {
		return map[string]any{"error": fmt.Sprintf("no running nightshift fleet for %s", project)}
	}
	n := workers
	if n < 1 {
		n = 1
	}
	f := filepath.Join(repo, ".nightshift", "desired-workers")
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return map[string]any{"error": err.Error()}
	}
	if err := os.WriteFile(f, []byte(strconv.Itoa(n)+"\n"), 0o644); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"ok": true, "workers": n, "repo": repo}
}

// spawnDetached execs this binary's nightshift verb in a new session.
func spawnDetached(argv []string, extraEnv []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, argv...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// nightshiftRunning reports a live orchestrator on this repo: the Go
// daemon's pidfile first, then the legacy bash orchestrator via pgrep.
func nightshiftRunning(repo string) bool {
	if pid, ok := procutil.ReadPidfile(filepath.Join(repo, ".nightshift", "orchestrator.pid")); ok && procutil.Alive(pid) {
		return true
	}
	cmd := exec.Command("pgrep", "-f", "nightshift-orchestrate.sh --repo "+repo)
	return cmd.Run() == nil
}

func gitRemoteURL(checkout string) string {
	out, err := exec.Command("git", "-C", checkout, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// RepoNameFromURL: github.com/Owner/devbrain.git -> devbrain;
// git@host:owner/repo.git -> repo.
func RepoNameFromURL(url string) string {
	base := strings.TrimRight(url, "/")
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, ":"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".git")
}

// NightshiftClonePath maps a checkout to its dedicated clone dir ("" when it
// has no remote).
func (q *Queue) NightshiftClonePath(checkout string) string {
	url := gitRemoteURL(checkout)
	if url == "" {
		return ""
	}
	return filepath.Join(q.NightshiftHome, RepoNameFromURL(url))
}

// ensureNightshiftClone resolves the isolated clone the fleet should run in,
// cloning from the remote on first use. Falls back to the checkout itself
// for a remote-less repo; ("", error) when a needed clone fails or collides.
func (q *Queue) ensureNightshiftClone(checkout string) (string, string) {
	url := gitRemoteURL(checkout)
	if url == "" {
		return checkout, "no git remote — running in the checkout in place"
	}
	dest := filepath.Join(q.NightshiftHome, RepoNameFromURL(url))
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		if gitRemoteURL(dest) == url {
			return dest, "reused dedicated clone"
		}
		return "", dest + " exists but points at a different remote — move it aside"
	}
	if err := os.MkdirAll(q.NightshiftHome, 0o755); err != nil {
		return "", "could not clone: " + err.Error()
	}
	out, err := exec.Command("git", "clone", "--quiet", url, dest).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if r := []rune(msg); len(r) > 200 {
			msg = string(r[:200])
		}
		return "", "clone failed: " + msg
	}
	return dest, "cloned a fresh dedicated checkout"
}

// --- shared JSON helpers ------------------------------------------------------

// readJSONMap decodes one JSON object file with numbers preserved.
func readJSONMap(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeJSONMap(string(raw))
}

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

// parsePyISO is datetime.fromisoformat for the shapes the run stamps carry.
func parsePyISO(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02T15:04:05.999999999-07",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unparseable timestamp: " + s)
}
