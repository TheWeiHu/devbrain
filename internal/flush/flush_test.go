package flush

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// setup returns a data-repo clone (with one pushed commit on main) and its
// bare origin.
func setup(t *testing.T) (data, origin string) {
	t.Helper()
	// Isolate the machine-local flush stamp: without this, tests that commit
	// (the manual-path ones) write the REAL ~/.config/devbrain/flush-stamp
	// and shift the host's live throttle window.
	if os.Getenv("DEVBRAIN_FLUSH_STAMP_DIR") == "" {
		t.Setenv("DEVBRAIN_FLUSH_STAMP_DIR", t.TempDir())
	}
	tmp := t.TempDir()
	origin = filepath.Join(tmp, "origin.git")
	data = filepath.Join(tmp, "data")
	mustGit(t, tmp, "init", "-q", "--bare", origin)
	mustGit(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	mustGit(t, tmp, "clone", "-q", origin, data)
	// A real data repo has an identity. Without one, any git operation that
	// needs to write a commit — a rebase during pull, a merge — fails before
	// it can conflict, which silently turns the conflict tests green on a CI
	// box that has no global git config.
	mustGit(t, data, "config", "user.name", "t")
	mustGit(t, data, "config", "user.email", "t@t")
	mustGit(t, data, "checkout", "-q", "-B", "main")
	os.WriteFile(filepath.Join(data, "f"), []byte("base\n"), 0o644)
	// A real data repo already carries the merge rules; the bootstrap path
	// (a repo predating them) has its own test.
	os.WriteFile(filepath.Join(data, ".gitattributes"), []byte(mergeAttrs), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "init")
	mustGit(t, data, "push", "-q", "-u", "origin", "main")
	t.Setenv("DEVBRAIN_DATA", data)
	return data, origin
}

// Every flush tick refreshes the AGENTS.md prefs inline — including idle
// ticks, so a prefs-only edit still propagates with no repo change.
func TestFlushRefreshesAgentsPrefs(t *testing.T) {
	setup(t)
	called := false
	old := RefreshAgents
	RefreshAgents = func() { called = true }
	t.Cleanup(func() { RefreshAgents = old })
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if !called {
		t.Error("Run did not invoke RefreshAgents")
	}
}

// A scrub-and-re-add of origin drops branch.main.remote; flush must still
// push new commits.
func TestFlushPushesWithoutUpstream(t *testing.T) {
	data, origin := setup(t)
	url := mustGit(t, data, "remote", "get-url", "origin")
	mustGit(t, data, "remote", "remove", "origin")
	mustGit(t, data, "remote", "add", "origin", url)

	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, origin, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("origin main tip = %q, want capture commit", got)
	}
}

// Commits stranded by an earlier failed push go out on the next flush even
// when the working tree is clean.
func TestFlushRepushesStrandedCommits(t *testing.T) {
	data, origin := setup(t)
	os.WriteFile(filepath.Join(data, "stranded"), []byte("x\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "stranded")

	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, origin, "log", "-1", "--format=%s", "main"); got != "stranded" {
		t.Fatalf("origin main tip = %q, want %q", got, "stranded")
	}
}

// The tracking ref is gone post-scrub and an offline pull can't restore it;
// pushNeeded must not silently report false.
func TestPushNeededWithoutTrackingRef(t *testing.T) {
	data, _ := setup(t)
	mustGit(t, data, "update-ref", "-d", "refs/remotes/origin/main")
	if !pushNeeded(data, "main") {
		t.Fatal("pushNeeded = false with origin/main tracking ref absent")
	}
}

// A conflicted pull must abort, not commit conflict markers via add -A.
func TestFlushAbortsConflictedPull(t *testing.T) {
	data, origin := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, filepath.Dir(other), "clone", "-q", origin, other)
	os.WriteFile(filepath.Join(other, "f"), []byte("theirs\n"), 0o644)
	mustGit(t, other, "add", ".")
	mustGit(t, other, "commit", "-qm", "theirs")
	mustGit(t, other, "push", "-q", "origin", "main")

	os.WriteFile(filepath.Join(data, "f"), []byte("ours\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "ours")

	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if _, err := os.Stat(filepath.Join(data, ".git", "rebase-merge")); err == nil {
		t.Fatal("rebase left in progress")
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s"); got != "ours" {
		t.Fatalf("local tip = %q, want %q (nothing committed mid-conflict)", got, "ours")
	}
}

// The corruption this guard exists for: the rebase LANDS but the autostash
// pop conflicts. git exits 0 and leaves no rebase dir, so the dir check above
// sails past — only unmerged index entries reveal it. Without the guard the
// `add -A` stages the markers as the resolution and commits them.
func TestFlushRefusesConflictedAutostash(t *testing.T) {
	data, origin := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, filepath.Dir(other), "clone", "-q", origin, other)
	os.WriteFile(filepath.Join(other, "f"), []byte("base\ntheirs\n"), 0o644)
	mustGit(t, other, "add", ".")
	mustGit(t, other, "commit", "-qm", "theirs")
	mustGit(t, other, "push", "-q", "origin", "main")

	// Uncommitted and conflicting — this is what --autostash pockets.
	os.WriteFile(filepath.Join(data, "f"), []byte("base\nours\n"), 0o644)

	var errbuf strings.Builder
	if rc := Run(nil, io.Discard, &errbuf); rc != 1 {
		t.Fatalf("Run = %d, want 1 (refuse to commit a conflicted tree)", rc)
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s"); got != "theirs" {
		t.Fatalf("local tip = %q, want %q (nothing committed mid-conflict)", got, "theirs")
	}
	if !strings.Contains(errbuf.String(), "unresolved merge") {
		t.Fatalf("stderr = %q, want an unresolved-merge warning", errbuf.String())
	}
	if mustGit(t, data, "stash", "list") == "" {
		t.Fatal("autostash dropped — the local changes must stay recoverable")
	}
}

// A captured transcript may legitimately contain a conflict marker at column
// 0 — devbrain logs sessions, including sessions about merge conflicts. Flush
// must commit it, not mistake it for damage and wedge on it forever.
func TestFlushCommitsTranscriptQuotingMarkers(t *testing.T) {
	data, _ := setup(t)
	os.MkdirAll(filepath.Join(data, "projects", "p", "log"), 0o755)
	os.WriteFile(filepath.Join(data, "projects", "p", "log", "s.md"),
		[]byte("# session\n\n```\n<<<<<<< Updated upstream\nx\n=======\ny\n>>>>>>> Stashed changes\n```\n"), 0o644)

	if rc := Run([]string{"capture"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0 (a transcript quoting markers is content, not damage)", rc)
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("local tip = %q, want the capture commit", got)
	}
}

// A repo predating the merge rules gains them BEFORE the pull they protect,
// not after — otherwise its first flush races unguarded.
func TestFlushSeedsMergeAttrs(t *testing.T) {
	data, _ := setup(t)
	os.Remove(filepath.Join(data, ".gitattributes"))
	mustGit(t, data, "rm", "-q", "--cached", ".gitattributes")
	mustGit(t, data, "commit", "-qm", "pre-attrs repo")

	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	for _, probe := range attrProbes {
		if got := mustGit(t, data, "check-attr", "merge", "--", probe); !strings.HasSuffix(got, ": union") {
			t.Fatalf("check-attr %s = %q, want union", probe, got)
		}
	}
	// Idempotent: a second flush must not append a duplicate block.
	first, _ := os.ReadFile(filepath.Join(data, ".gitattributes"))
	os.WriteFile(filepath.Join(data, "new2"), []byte("y\n"), 0o644)
	Run(nil, io.Discard, io.Discard)
	if b2, _ := os.ReadFile(filepath.Join(data, ".gitattributes")); string(b2) != string(first) {
		t.Fatalf(".gitattributes rewritten: %q -> %q", first, b2)
	}
}

// Substring-matching .gitattributes is fooled by a comment, a narrower
// pattern, a half-written file, or a later override — so the check asks git
// for the EFFECTIVE attribute instead. Each of these must be repaired.
func TestFlushRepairsIneffectiveMergeAttrs(t *testing.T) {
	for name, content := range map[string]string{
		"commented":  "# *.jsonl merge=union\n",
		"narrower":   "foo*.jsonl merge=union\n",
		"half":       "*.jsonl merge=union\n",
		"overridden": "*.jsonl merge=union\n*.log merge=union\n*.jsonl -merge\n",
	} {
		t.Run(name, func(t *testing.T) {
			data, _ := setup(t)
			os.WriteFile(filepath.Join(data, ".gitattributes"), []byte(content), 0o644)
			os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)
			if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
				t.Fatalf("Run = %d, want 0", rc)
			}
			for _, probe := range attrProbes {
				if got := mustGit(t, data, "check-attr", "merge", "--", probe); !strings.HasSuffix(got, ": union") {
					t.Fatalf("check-attr %s = %q, want union", probe, got)
				}
			}
		})
	}
}

// A conflicted tree must gate `add -A` even with no remote in play: the guard
// is about not committing damage, not about syncing.
func TestFlushRefusesConflictWithoutRemote(t *testing.T) {
	data, _ := setup(t)
	mustGit(t, data, "remote", "remove", "origin")
	// A conflict git records with no textual markers at all.
	os.WriteFile(filepath.Join(data, "bin"), []byte("ours\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "ours")
	mustGit(t, data, "checkout", "-q", "-b", "side", "HEAD~1")
	os.WriteFile(filepath.Join(data, "bin"), []byte("theirs\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "theirs")
	_ = exec.Command("git", "-C", data, "merge", "main").Run() // conflicts on purpose

	before := mustGit(t, data, "rev-parse", "HEAD")
	var errbuf strings.Builder
	if rc := Run(nil, io.Discard, &errbuf); rc != 1 {
		t.Fatalf("Run = %d, want 1", rc)
	}
	if got := mustGit(t, data, "rev-parse", "HEAD"); got != before {
		t.Fatal("committed on top of an unresolved merge")
	}
	if !strings.Contains(errbuf.String(), "unresolved merge") {
		t.Fatalf("stderr = %q, want the warning", errbuf.String())
	}
}

// A conflicted tree blocks new commits — but commits already made and safe
// must still reach the remote, or a conflict nobody notices strands them.
func TestFlushPushesStrandedDespiteConflict(t *testing.T) {
	data, origin := setup(t)
	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, filepath.Dir(other), "clone", "-q", origin, other)
	os.WriteFile(filepath.Join(other, "f"), []byte("base\ntheirs\n"), 0o644)
	mustGit(t, other, "add", ".")
	mustGit(t, other, "commit", "-qm", "theirs")
	mustGit(t, other, "push", "-q", "origin", "main")

	os.WriteFile(filepath.Join(data, "safe"), []byte("committed and safe\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "safe")
	// Uncommitted and conflicting — the autostash pop will fail.
	os.WriteFile(filepath.Join(data, "f"), []byte("base\nours\n"), 0o644)

	if rc := Run(nil, io.Discard, io.Discard); rc != 1 {
		t.Fatalf("Run = %d, want 1", rc)
	}
	if !conflicted(data) {
		t.Skip("git resolved the autostash cleanly; nothing to assert")
	}
	if mustGit(t, origin, "log", "--format=%s", "-20") == "" ||
		!strings.Contains(mustGit(t, origin, "log", "--format=%s", "-20"), "safe") {
		t.Fatal("the already-safe commit was stranded by the conflict guard")
	}
}

// End-to-end: with union merge in place the tokens.jsonl race that caused the
// corruption resolves to both machines' lines and never conflicts.
func TestUnionMergeResolvesSidecarRace(t *testing.T) {
	data, origin := setup(t)
	os.WriteFile(filepath.Join(data, ".gitattributes"), []byte(mergeAttrs), 0o644)
	os.MkdirAll(filepath.Join(data, "projects", "p"), 0o755)
	sidecar := filepath.Join("projects", "p", "tokens.jsonl")
	os.WriteFile(filepath.Join(data, sidecar), []byte("{\"n\":1}\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "base sidecar")
	mustGit(t, data, "push", "-q", "origin", "main")

	other := filepath.Join(t.TempDir(), "other")
	mustGit(t, filepath.Dir(other), "clone", "-q", origin, other)
	os.WriteFile(filepath.Join(other, sidecar), []byte("{\"n\":1}\n{\"n\":2,\"who\":\"them\"}\n"), 0o644)
	mustGit(t, other, "add", ".")
	mustGit(t, other, "commit", "-qm", "theirs")
	mustGit(t, other, "push", "-q", "origin", "main")

	// This machine appends a different line, uncommitted — the exact race.
	os.WriteFile(filepath.Join(data, sidecar), []byte("{\"n\":1}\n{\"n\":2,\"who\":\"us\"}\n"), 0o644)
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0 (union merge, no conflict)", rc)
	}
	got, _ := os.ReadFile(filepath.Join(data, sidecar))
	if strings.Contains(string(got), "<<<<<<<") {
		t.Fatalf("conflict markers survived union merge: %q", got)
	}
	for _, want := range []string{`"who":"them"`, `"who":"us"`} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("sidecar = %q, missing %s", got, want)
		}
	}
}

// No remote at all: flush commits locally and stays quiet.
func TestFlushNoRemote(t *testing.T) {
	data, _ := setup(t)
	mustGit(t, data, "remote", "remove", "origin")

	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)
	var errBuf strings.Builder
	if rc := Run(nil, io.Discard, &errBuf); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("local tip = %q, want capture commit", got)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errBuf.String())
	}
}

func stampDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEVBRAIN_FLUSH_STAMP_DIR", dir)
	return dir
}

func writeStampAt(t *testing.T, dir string, at time.Time) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "flush-stamp"),
		[]byte(strconv.FormatInt(at.Unix(), 10)+"\n"), 0o644)
}

// The first scheduled commit stamps the machine-local window; the next
// scheduled tick defers (files stay on disk); a manual flush is unthrottled
// and resets the window.
func TestScheduledFlushThrottlesCommits(t *testing.T) {
	dir := stampDir(t)
	data, _ := setup(t)

	// Never committed on this machine -> the first scheduled tick commits.
	os.WriteFile(filepath.Join(data, "a"), []byte("x\n"), 0o644)
	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, data, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("first scheduled flush did not commit: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "flush-stamp")); err != nil {
		t.Fatal("commit did not write the flush stamp")
	}

	// Inside the window -> defer; the swept file stays on disk uncommitted.
	os.WriteFile(filepath.Join(data, "b"), []byte("y\n"), 0o644)
	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if st := mustGit(t, data, "status", "--porcelain"); st == "" {
		t.Fatal("deferred flush should leave the new file uncommitted on disk")
	}

	// Manual flush: no throttle.
	if rc := Run(nil, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("manual Run = %d, want 0", rc)
	}
	if st := mustGit(t, data, "status", "--porcelain"); st != "" {
		t.Fatalf("manual flush should commit the deferred file, tree still dirty:\n%s", st)
	}
}

// Once the machine's stamp is older than commitEvery, the scheduled tick
// commits — even if HEAD is fresh (e.g. a commit just pulled from another
// machine must not starve this one's).
func TestScheduledFlushCommitsAfterInterval(t *testing.T) {
	dir := stampDir(t)
	data, origin := setup(t) // setup's init commit = fresh HEAD
	writeStampAt(t, dir, time.Now().Add(-commitEvery-time.Minute))
	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)

	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, origin, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "capture:") {
		t.Fatalf("scheduled flush past the interval did not commit+push: %q", got)
	}
}

// A stamp in the future (clock skew, restored backup) must not freeze the
// throttle: the negative age reads as "huge", so the tick commits.
func TestScheduledFlushSurvivesFutureStamp(t *testing.T) {
	dir := stampDir(t)
	data, _ := setup(t)
	writeStampAt(t, dir, time.Now().Add(48*time.Hour))
	os.WriteFile(filepath.Join(data, "new"), []byte("x\n"), 0o644)

	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if st := mustGit(t, data, "status", "--porcelain"); st != "" {
		t.Fatalf("future stamp froze the throttle; tree still dirty:\n%s", st)
	}
}

// Stranded commits are pushed by a scheduled tick even inside the throttle
// window — the throttle defers new commits, never durability.
func TestScheduledFlushStillRepushesStranded(t *testing.T) {
	dir := stampDir(t)
	writeStampAt(t, dir, time.Now())
	data, origin := setup(t)
	os.WriteFile(filepath.Join(data, "s"), []byte("x\n"), 0o644)
	mustGit(t, data, "add", ".")
	mustGit(t, data, "commit", "-qm", "stranded: local only")

	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("Run = %d, want 0", rc)
	}
	if got := mustGit(t, origin, "log", "-1", "--format=%s", "main"); !strings.HasPrefix(got, "stranded:") {
		t.Fatalf("stranded commit not pushed by scheduled tick: %q", got)
	}
}
