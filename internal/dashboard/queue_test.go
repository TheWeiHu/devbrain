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
	// approved flag round-trips: set writes `approved: true`, clear removes it
	u = &Updates{}
	u.Set("approved", strp("true"))
	b, _ = q.Write("proj__a", "0002-beta-chore", u, "beta chore", "")
	if !b.Approved {
		t.Error("approve -> approved should be true")
	}
	u = &Updates{}
	u.Set("approved", nil)
	b, _ = q.Write("proj__a", "0002-beta-chore", u, "beta chore", "")
	if b.Approved {
		t.Error("un-approve -> approved should be cleared")
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

func TestNightshiftListAndPrune(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	seedThree(t, q)
	if ns := q.Nightshift(); len(ns["runs"].([]any)) != 0 {
		t.Errorf("nightshift should be empty with no runs: %v", ns)
	}
	// a live fleet is listed with its project + status forwarded
	repo := filepath.Join(q.Data, "repo")
	// status.json carries its OWN short "project" (the display name); the dir slug
	// must win — Stop/Scale resolve the registration by it.
	writeJSONFile(t, filepath.Join(repo, ".nightshift", "status.json"),
		map[string]any{"running": true, "project": "a", "workers": []any{map[string]any{"i": 0, "state": "working"}}})
	writeJSONFile(t, filepath.Join(q.Data, "projects", "proj__a", "nightshift-run.json"),
		map[string]any{"port": 8799, "repo": repo})
	runs := q.Nightshift()["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	run := runs[0].(map[string]any)
	if run["project"] != "proj__a" || len(run["workers"].([]any)) != 1 {
		t.Errorf("run shape wrong: %v", run)
	}
	// self-heal: a stopped fleet with a stale stamp is pruned off disk
	stale := filepath.Join(q.Data, "stale-repo")
	sf := filepath.Join(q.Data, "projects", "proj__b", "nightshift-run.json")
	writeJSONFile(t, sf, map[string]any{"port": 8799, "repo": stale})
	old := fixedClock().Add(-time.Hour).Format("2006-01-02T15:04:05Z")
	writeJSONFile(t, filepath.Join(stale, ".nightshift", "status.json"),
		map[string]any{"running": false, "updated": old})
	runs = q.Nightshift()["runs"].([]any)
	for _, r := range runs {
		if r.(map[string]any)["project"] == "proj__b" {
			t.Error("stale fleet must be pruned from the list")
		}
	}
	if _, err := os.Stat(sf); !os.IsNotExist(err) {
		t.Error("stale registration file must be deleted off disk")
	}
	if len(runs) != 1 {
		t.Errorf("live fleet must survive the prune, got %d runs", len(runs))
	}
	// a stopped fleet with a FRESH stamp is kept
	fresh := fixedClock().Add(-time.Minute).Format("2006-01-02T15:04:05Z")
	writeJSONFile(t, sf, map[string]any{"port": 8799, "repo": stale})
	writeJSONFile(t, filepath.Join(stale, ".nightshift", "status.json"),
		map[string]any{"running": false, "updated": fresh})
	if got := len(q.Nightshift()["runs"].([]any)); got != 2 {
		t.Errorf("fresh stopped fleet must be kept, got %d runs", got)
	}
	// a registration whose repo was deleted is pruned (no status.json, dir gone)
	gone := filepath.Join(q.Data, "projects", "proj__b", "nightshift-run.json")
	writeJSONFile(t, gone, map[string]any{"port": 8799, "repo": filepath.Join(q.Data, "vanished")})
	q.Nightshift()
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Error("registration for a vanished repo must be deleted")
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

func TestProjectRemote(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	q.NightshiftHome = filepath.Join(q.Data, "nshome")
	seedThree(t, q)
	seedRemote(t, q, "proj__a", "git@github.com:Proj/a.git")
	if got := q.ProjectRemote("proj__a"); got != "git@github.com:Proj/a.git" {
		t.Errorf("ProjectRemote = %q, want the stamped pointer", got)
	}
	if got := q.ProjectRemote("proj__z"); got != "" {
		t.Errorf("ProjectRemote for unknown project = %q, want empty", got)
	}
	// traversal shapes must never resolve to a path outside projects/
	for _, bad := range []string{"..", "../proj__a", "proj__a/../../etc", ".hidden", ""} {
		if got := q.ProjectRemote(bad); got != "" {
			t.Errorf("ProjectRemote(%q) = %q, want empty", bad, got)
		}
	}
	// a pointer whose URL maps to a DIFFERENT owner__repo is stale/misplaced -> ignored
	seedRemote(t, q, "proj__b", "git@github.com:someone/else.git")
	if got := q.ProjectRemote("proj__b"); got != "" {
		t.Errorf("mismatched pointer = %q, want rejected", got)
	}
	// a custom (non owner__repo) key has no derivable identity -> pointer trusted
	seedRemote(t, q, "legacy-name", "git@github.com:someone/else.git")
	if got := q.ProjectRemote("legacy-name"); got != "git@github.com:someone/else.git" {
		t.Errorf("custom-key pointer = %q, want trusted", got)
	}
}

func TestProjectRemoteBackfill(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	q := newTestQueue(t)
	q.NightshiftHome = filepath.Join(q.Data, "nshome")
	seedThree(t, q)
	// no pointer, but a NightshiftHome clone whose remote maps to proj__a
	clone := filepath.Join(q.NightshiftHome, "a")
	mustRun(t, "git", "init", "-q", clone)
	mustRun(t, "git", "-C", clone, "remote", "add", "origin", "https://github.com/proj/a.git")
	if got := q.ProjectRemote("proj__a"); got != "https://github.com/proj/a.git" {
		t.Fatalf("ProjectRemote = %q, want the clone's remote", got)
	}
	b, err := os.ReadFile(filepath.Join(q.projectsDir(), "proj__a", "remote"))
	if err != nil || strings.TrimSpace(string(b)) != "https://github.com/proj/a.git" {
		t.Errorf("pointer must be back-filled from the clone, got %q (%v)", b, err)
	}
}

func TestStartNightshift(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	q.NightshiftHome = filepath.Join(q.Data, "nshome")
	seedThree(t, q)
	checkout := filepath.Join(q.Data, "checkout-a")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	seedRemote(t, q, "proj__a", "https://github.com/proj/a.git")
	var spawned []string
	var spawnedEnv []string
	q.Running = func(string) bool { return false }
	q.EnsureClone = func(url string) (string, string) {
		if url != "https://github.com/proj/a.git" {
			t.Errorf("EnsureClone url = %q", url)
		}
		return checkout, "stub"
	}
	q.Spawn = func(argv, env []string) error { spawned, spawnedEnv = argv, env; return nil }

	if res := q.StartNightshift("proj__a", []string{"nope"}, 8799); res["error"] == nil {
		t.Error("bad ids must be rejected")
	}
	if res := q.StartNightshift("proj__z", []string{"0081"}, 8799); res["error"] == nil {
		t.Error("missing repo must error")
	}
	res := q.StartNightshift("proj__a", []string{"0081-foo", "0076-bar"}, 8123)
	if res["ok"] != true || res["repo"] != checkout {
		t.Fatalf("launch failed: %v", res)
	}
	if !reflect.DeepEqual(spawned, []string{"nightshift", "start", checkout, "--only", "0081-foo,0076-bar"}) {
		t.Errorf("spawn argv = %v", spawned)
	}
	if !reflect.DeepEqual(spawnedEnv, []string{"NIGHTSHIFT_NO_OPEN=1", "DEVBRAIN_QUEUE_PORT=8123"}) {
		t.Errorf("spawn env = %v", spawnedEnv)
	}
	// duplicate fleet refused, and no spawn happens
	spawned = nil
	q.Running = func(string) bool { return true }
	res = q.StartNightshift("proj__a", []string{"0081-foo"}, 8123)
	errMsg, _ := res["error"].(string)
	if !strings.Contains(errMsg, "already running") {
		t.Errorf("duplicate fleet must be refused: %v", res)
	}
	if spawned != nil {
		t.Error("duplicate launch must not spawn")
	}
	// launch.lock left by the first start: a second immediate start is
	// refused while the lock is <30s old (pin its mtime to the test clock)
	q.Running = func(string) bool { return false }
	lock := filepath.Join(checkout, ".nightshift", "launch.lock")
	if err := os.Chtimes(lock, fixedClock(), fixedClock()); err != nil {
		t.Fatal(err)
	}
	res = q.StartNightshift("proj__a", []string{"0081-foo"}, 8123)
	errMsg, _ = res["error"].(string)
	if !strings.Contains(errMsg, "already starting") {
		t.Errorf("mid-flight launch lock must refuse a second start: %v", res)
	}
}

func TestStopNightshift(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	q.NightshiftHome = filepath.Join(q.Data, "nshome")
	seedThree(t, q)
	seedRemote(t, q, "proj__a", "https://github.com/proj/a.git")
	clone := filepath.Join(q.NightshiftHome, "a")
	var spawned []string
	q.Spawn = func(argv, env []string) error { spawned = argv; return nil }

	if res := q.StopNightshift("proj__z"); res["error"] == nil {
		t.Error("missing repo must error")
	}
	res := q.StopNightshift("proj__a")
	if res["ok"] != true || res["repo"] != clone {
		t.Fatalf("stop failed: %v", res)
	}
	if !reflect.DeepEqual(spawned, []string{"nightshift", "stop", clone}) {
		t.Errorf("spawn argv = %v", spawned)
	}

	// A run registration wins over path resolution: stop where the fleet runs.
	regRepo := filepath.Join(q.Data, "fleet-clone")
	writeJSONFile(t, filepath.Join(q.projectsDir(), "proj__a", "nightshift-run.json"),
		map[string]any{"repo": regRepo, "run_id": "r1"})
	if res := q.StopNightshift("proj__a"); res["repo"] != regRepo {
		t.Fatalf("stop must target the registered repo: %v", res)
	}
	if !reflect.DeepEqual(spawned, []string{"nightshift", "stop", regRepo}) {
		t.Errorf("spawn argv = %v", spawned)
	}
}

func TestScaleNightshift(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	seedThree(t, q) // proj__a: 2 open tasks; proj__b: 1

	// No run registration → error.
	if res := q.ScaleNightshift("proj__a", 2); res["error"] == nil {
		t.Error("scaling a project with no running fleet must error")
	}

	repo := filepath.Join(q.Data, "run-repo")
	writeJSONFile(t, filepath.Join(q.projectsDir(), "proj__a", "nightshift-run.json"),
		map[string]any{"repo": repo, "run_id": "r1"})
	ctrl := filepath.Join(repo, ".nightshift", "desired-workers")

	// Floor clamp: 0 → 1, written to the control file.
	if res := q.ScaleNightshift("proj__a", 0); res["ok"] != true || res["workers"] != 1 {
		t.Fatalf("floor clamp: %v", res)
	}
	if b, _ := os.ReadFile(ctrl); strings.TrimSpace(string(b)) != "1" {
		t.Errorf("control file after floor clamp = %q", b)
	}

	// No ceiling clamp: the request is stored raw (the orchestrator caps to live
	// work each tick), so a target above today's queue survives until work arrives.
	if res := q.ScaleNightshift("proj__a", 9); res["workers"] != 9 {
		t.Fatalf("request stored unclamped: %v", res)
	}
	if b, _ := os.ReadFile(ctrl); strings.TrimSpace(string(b)) != "9" {
		t.Errorf("control file after scale = %q", b)
	}

	// tmux fleets can't be live-rescaled → reject and DON'T touch the control file.
	if err := os.WriteFile(filepath.Join(repo, ".nightshift", "mode"), []byte("tmux\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := q.ScaleNightshift("proj__a", 3); res["error"] == nil {
		t.Error("scaling a tmux fleet must error")
	}
	if b, _ := os.ReadFile(ctrl); strings.TrimSpace(string(b)) != "9" {
		t.Errorf("tmux reject must not rewrite the control file, got %q", b)
	}
}

func TestRepoNameFromURL(t *testing.T) {
	t.Parallel()
	if got := RepoNameFromURL("https://github.com/Owner/devbrain.git"); got != "devbrain" {
		t.Errorf("https form = %q", got)
	}
	if got := RepoNameFromURL("git@github.com:owner/repo.git"); got != "repo" {
		t.Errorf("ssh form = %q", got)
	}
}

func TestEnsureNightshiftClone(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	q := newTestQueue(t)
	q.NightshiftHome = filepath.Join(q.Data, "nshome")
	rem := filepath.Join(q.Data, "rem.git")
	wrk := filepath.Join(q.Data, "wrk")
	mustRun(t, "git", "init", "-q", "--bare", rem)
	mustRun(t, "git", "clone", "-q", rem, wrk)
	if err := os.WriteFile(filepath.Join(wrk, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", wrk, "add", ".")
	mustRun(t, "git", "-C", wrk, "-c", "user.email=a@b.c", "-c", "user.name=t", "commit", "-qm", "i")
	mustRun(t, "git", "-C", wrk, "push", "-q", "origin", "HEAD:main")
	if got := q.ClonePath(rem); got != filepath.Join(q.NightshiftHome, "rem") {
		t.Errorf("clone path = %q", got)
	}
	cr, _ := q.ensureNightshiftClone(rem)
	if cr != filepath.Join(q.NightshiftHome, "rem") {
		t.Fatalf("ensure clone = %q", cr)
	}
	if _, err := os.Stat(filepath.Join(cr, ".git")); err != nil {
		t.Error("fresh dedicated checkout must exist")
	}
	cr2, note2 := q.ensureNightshiftClone(rem)
	if cr2 != cr || !strings.Contains(note2, "reused") {
		t.Errorf("second ensure = %q / %q, want reuse", cr2, note2)
	}
	// a DIFFERENT repo whose name collides with the existing clone -> refuse
	other := filepath.Join(q.Data, "elsewhere", "rem.git")
	mustRun(t, "git", "init", "-q", "--bare", other)
	r3, n3 := q.ensureNightshiftClone(other)
	if r3 != "" || !strings.Contains(n3, "different remote") {
		t.Errorf("colliding clone = %q / %q, want refusal", r3, n3)
	}
}

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
