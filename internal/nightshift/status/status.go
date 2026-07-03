// Package status emits <repo>/.nightshift/status.json for the dashboard —
// the Go port of scripts/nightshift-status.py. Standalone by design: it
// reconstructs live state from tmux + git + the TODO queue + the orchestrator
// log, so the dashboard works regardless of orchestrator version, and the
// emitter deliberately OUTLIVES the orchestrator (it renders the "stopped"
// card and retires itself 10 minutes later).
package status

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/pricing"
	"github.com/TheWeiHu/devbrain/internal/procutil"
)

// Doc is the FROZEN status.json shape (field order = key order). The
// dashboard's Nightshift tab and queue.py's stale-run pruning read these
// keys; testdata/dashboard-fixture/nightshift-status.json pins the set.
type Doc struct {
	Updated     string      `json:"updated"`
	StoppedAt   string      `json:"stopped_at"` // "" while running; first-stopped stamp after
	RunID       string      `json:"run_id"`
	Started     string      `json:"started"`
	Project     string      `json:"project"`
	Running     bool        `json:"running"`
	Queue       QueueCounts `json:"queue"`
	TokensMin   TokenPair   `json:"tokens_min"`   // new (non-cached) tokens, last 60s
	TokensTotal TokenPair   `json:"tokens_total"` // cumulative non-cached in/out
	CostTotal   float64     `json:"cost_total"`   // true billed $ incl. cache
	History     []HistPoint `json:"history"`
	Parked      []Parked    `json:"parked"`
	ParkedCount int         `json:"parked_count"`
	Workers     []Worker    `json:"workers"`
	Nightshift  []string    `json:"nightshift"`
	Log         []string    `json:"log"`
}

type QueueCounts struct {
	Open   int `json:"open"`
	Done   int `json:"done"`
	Review int `json:"review"`
}

type TokenPair struct {
	In  int64 `json:"in"`
	Out int64 `json:"out"`
}

type HistPoint struct {
	T   string `json:"t"`
	Out int64  `json:"out"`
	In  int64  `json:"in"`
}

type Parked struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	URL    string `json:"url"`
}

type Worker struct {
	I         int        `json:"i"`
	State     string     `json:"state"`
	Task      string     `json:"task"`
	TIn       int64      `json:"tin"`
	TOut      int64      `json:"tout"`
	Pane      string     `json:"pane"`
	Responses []Response `json:"responses"`
}

type Response struct {
	T    string `json:"t"`
	SID  string `json:"sid"`
	Text string `json:"text"`
}

// RunIdentity gives the CURRENT run a stable identity so the dashboard can
// tell a restart apart from a continuing run. The orchestrator PID is
// constant within a run and new on every (re)start.
func RunIdentity(prior map[string]any, running bool, orchPID, now string) (runID, started string, resetHistory bool) {
	prevID, _ := prior["run_id"].(string)
	if running {
		runID = orchPID
		if runID == "" {
			runID = prevID
		}
		if runID == "" {
			runID = now
		}
		if runID != prevID {
			return runID, now, true // new run → fresh chart, new start stamp
		}
		started, _ = prior["started"].(string)
		if started == "" {
			started = now
		}
		return runID, started, false
	}
	started, _ = prior["started"].(string)
	return prevID, started, false // stopped → keep last known identity
}

// readPriorStatus loads the last status.json (retry: concurrent writers can
// leave it briefly unparseable). Returns an empty map when absent/garbage.
func readPriorStatus(nsDir string) map[string]any {
	statusPath := filepath.Join(nsDir, "status.json")
	prior := map[string]any{}
	for attempt := 0; attempt < 3; attempt++ {
		b, rerr := os.ReadFile(statusPath)
		if rerr != nil {
			break // genuinely absent → fresh start
		}
		if json.Unmarshal(b, &prior) == nil {
			break
		}
		prior = map[string]any{}
		if attempt < 2 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return prior
}

// worktreeRun returns the run id stamped into a worker worktree by prepWorktree
// (empty if unstamped). The emitter shows a worktree only when it matches the
// live run, so a prior run's leftover worktrees are hidden, not shown stale.
func worktreeRun(wt string) string {
	b, err := os.ReadFile(filepath.Join(wt, ".nightshift", "run"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

var (
	ansiRe    = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07`)
	rowRe     = regexp.MustCompile(`(?m)^\s*\[[^\]]*\]\s+([a-z]+)\s+([0-9]{4}-[a-z0-9-]+)`)
	reasonRe  = regexp.MustCompile(`(?m)^reason:\s*(.+)$`)
	prRe      = regexp.MustCompile(`(?m)^pr:\s*(https?://\S+)`)
	parkedRe  = regexp.MustCompile(`(?i)^\s*parked\b`)
	slugTail  = regexp.MustCompile(`.*[:/]([^/]+/[^/]+)$`)
	gitSuffix = regexp.MustCompile(`(\.git)?\s*$`)
)

// Now is the injectable clock (local time; UTC derived where needed).
var Now = time.Now

// Emitter reconstructs one status tick for a repo.
type Emitter struct {
	Repo string
	// TodoOutput runs `devbrain todo <args...>` in the repo with the derive
	// env (DERIVE_GIT=1, FETCH_TTL=60) and returns stdout. Injectable.
	TodoOutput func(args ...string) string
	// ClaudeProjects is ~/.claude/projects (transcript store root).
	ClaudeProjects string

	rows [][2]string // (status, id) — ONE derive pass per emit
}

func NewEmitter(repo string) *Emitter {
	home, _ := os.UserHomeDir()
	e := &Emitter{Repo: repo, ClaudeProjects: filepath.Join(home, ".claude", "projects")}
	e.TodoOutput = func(args ...string) string {
		self := os.Getenv("DEVBRAIN_BIN") // shim convention; test-binary guard
		if self == "" {
			var err error
			if self, err = os.Executable(); err != nil {
				return ""
			}
		}
		cmd := exec.Command(self, append([]string{"todo"}, args...)...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "DEVBRAIN_TODO_DERIVE_GIT=1", "DEVBRAIN_TODO_FETCH_TTL=60")
		out, _ := cmd.Output()
		return string(out)
	}
	return e
}

func sh(dir string, argv ...string) string {
	cmd := exec.Command(argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, _ := cmd.Output()
	return string(out)
}

func strip(s string) string {
	return strings.ReplaceAll(ansiRe.ReplaceAllString(s, ""), "\r", "")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strip(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\n")
}

func (e *Emitter) allRows() [][2]string {
	if e.rows == nil {
		e.rows = [][2]string{}
		for _, m := range rowRe.FindAllStringSubmatch(e.TodoOutput("list", "all"), -1) {
			e.rows = append(e.rows, [2]string{m[1], m[2]})
		}
	}
	return e.rows
}

// count tallies queue rows in the given status, scoped to only when non-nil (a
// --only run counts just its launched subset). only is passed in, not cached on
// the Emitter: the emit loop reuses one Emitter across runs, so the fence must
// be re-read each pass.
func (e *Emitter) count(status string, only map[string]bool) int {
	n := 0
	for _, r := range e.allRows() {
		if r[0] != status {
			continue
		}
		if only != nil && !only[taskNum(r[1])] {
			continue
		}
		n++
	}
	return n
}

// onlySet reads .nightshift/only.txt — the fixed-set the run was launched with,
// as the set of 4-digit task numbers. nil means no fence on disk (a full-drain
// run), so counts span the whole project queue. Read fresh every emit pass.
func (e *Emitter) onlySet() map[string]bool {
	b, err := os.ReadFile(filepath.Join(e.Repo, ".nightshift", "only.txt"))
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, tok := range strings.Split(strings.TrimSpace(string(b)), ",") {
		if tok = strings.TrimSpace(tok); tok != "" {
			set[taskNum(tok)] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// taskNum is a task token's leading 4-digit number ("0007-write-x" -> "0007"),
// the stable key matched across slug and bare-number forms.
func taskNum(tok string) string {
	if i := strings.Index(tok, "-"); i >= 0 {
		return tok[:i]
	}
	return tok
}

// workerSlug maps a worktree path to its Claude Code transcript dir name.
func workerSlug(wt string) string {
	abs, err := filepath.Abs(wt)
	if err != nil {
		abs = wt
	}
	return strings.ReplaceAll(abs, "/", "-")
}

type usageEvent struct {
	Message *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input       int64 `json:"input_tokens"`
			Output      int64 `json:"output_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Type      string `json:"type"`
}

func tailLines(path string, maxLines int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var ring []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		ring = append(ring, sc.Text())
		if len(ring) > maxLines {
			ring = ring[1:]
		}
	}
	return ring
}

func parseISO(ts string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.Replace(ts, "Z", "+00:00", 1)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// tokenRate sums new (non-cached) input/output billed in the last window
// from the worker's NEWEST transcript, deduped like ccusage.
func (e *Emitter) tokenRate(wt string, window time.Duration) (in, out int64) {
	dir := filepath.Join(e.ClaudeProjects, workerSlug(wt))
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	newest, newestMod := "", time.Time{}
	for _, en := range ents {
		if !strings.HasSuffix(en.Name(), ".jsonl") {
			continue
		}
		if info, err := en.Info(); err == nil && info.ModTime().After(newestMod) {
			newest, newestMod = filepath.Join(dir, en.Name()), info.ModTime()
		}
	}
	if newest == "" {
		return 0, 0
	}
	cutoff := Now().UTC().Add(-window)
	seen := map[[2]string]bool{}
	for _, ln := range tailLines(newest, 1500) {
		var ev usageEvent
		if json.Unmarshal([]byte(ln), &ev) != nil || ev.Message == nil || ev.Message.Usage == nil || ev.Timestamp == "" {
			continue
		}
		key := [2]string{ev.Message.ID, ev.RequestID}
		if key[0] != "" && seen[key] {
			continue
		}
		seen[key] = true
		t, ok := parseISO(ev.Timestamp)
		if !ok || t.Before(cutoff) {
			continue
		}
		in += ev.Message.Usage.Input
		out += ev.Message.Usage.Output
	}
	return in, out
}

// tokenTotal sums CUMULATIVE tokens across ALL of a worker's transcripts,
// returning non-cached in/out plus the per-model 4-way split for pricing.
func (e *Emitter) tokenTotal(wt string) (in, out int64, byModel map[string][4]int64) {
	byModel = map[string][4]int64{}
	dir := filepath.Join(e.ClaudeProjects, workerSlug(wt))
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, byModel
	}
	seen := map[[2]string]bool{}
	for _, en := range ents {
		if !strings.HasSuffix(en.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, en.Name()))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var ev usageEvent
			if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Message == nil || ev.Message.Usage == nil {
				continue
			}
			if ev.Message.Model == "<synthetic>" { // local, non-API turn → no spend
				continue
			}
			key := [2]string{ev.Message.ID, ev.RequestID}
			if key[0] != "" && seen[key] {
				continue
			}
			seen[key] = true
			u := ev.Message.Usage
			in += u.Input
			out += u.Output
			row := byModel[ev.Message.Model]
			row[0] += u.Input
			row[1] += u.Output
			row[2] += u.CacheCreate
			row[3] += u.CacheRead
			byModel[ev.Message.Model] = row
		}
		f.Close()
	}
	return in, out, byModel
}

// recentResponses pulls the agent's text messages from the worker's newest
// transcripts (the live feed `claude -p` can't stream to turn.log).
func (e *Emitter) recentResponses(wt string, limit, files int, since time.Time) []Response {
	dir := filepath.Join(e.ClaudeProjects, workerSlug(wt))
	ents, err := os.ReadDir(dir)
	if err != nil {
		return []Response{}
	}
	type fm struct {
		path string
		mod  time.Time
	}
	var fps []fm
	for _, en := range ents {
		if !strings.HasSuffix(en.Name(), ".jsonl") {
			continue
		}
		if info, err := en.Info(); err == nil {
			if !since.IsZero() && info.ModTime().Before(since) {
				continue // a prior run's transcript — keep the current run's feed clean
			}
			fps = append(fps, fm{filepath.Join(dir, en.Name()), info.ModTime()})
		}
	}
	for i := 0; i < len(fps); i++ { // insertion sort by mtime (small n)
		for j := i; j > 0 && fps[j].mod.Before(fps[j-1].mod); j-- {
			fps[j], fps[j-1] = fps[j-1], fps[j]
		}
	}
	if len(fps) > files {
		fps = fps[len(fps)-files:]
	}
	msgs := []Response{}
	for _, fp := range fps {
		sid, _, _ := strings.Cut(filepath.Base(fp.path), "-") // short turn/session tag
		for _, ln := range tailLines(fp.path, 4000) {
			var ev struct {
				Type    string `json:"type"`
				Message *struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
				Timestamp string `json:"timestamp"`
			}
			if json.Unmarshal([]byte(ln), &ev) != nil || ev.Type != "assistant" || ev.Message == nil {
				continue
			}
			var b strings.Builder
			for _, blk := range ev.Message.Content {
				if blk.Type == "text" {
					b.WriteString(blk.Text)
				}
			}
			txt := strings.TrimSpace(b.String())
			if txt == "" {
				continue
			}
			t := ""
			if pt, ok := parseISO(ev.Timestamp); ok {
				t = pt.Local().Format("15:04:05")
			}
			if len([]rune(txt)) > 700 {
				txt = string([]rune(txt)[:700])
			}
			msgs = append(msgs, Response{T: t, SID: sid, Text: txt})
		}
	}
	if len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs
}

// orchestratorPID reports the live orchestrator for this repo: the Go
// daemon's pidfile first, then the legacy bash orchestrator via pgrep.
func orchestratorPID(repo string) string {
	if pid, ok := procutil.ReadPidfile(filepath.Join(repo, ".nightshift", "orchestrator.pid")); ok && procutil.Alive(pid) {
		return fmt.Sprintf("%d", pid)
	}
	out := strings.TrimSpace(sh("", "pgrep", "-f", "nightshift-orchestrate.sh --repo "+repo))
	if out != "" {
		return strings.SplitN(out, "\n", 2)[0]
	}
	return ""
}

// Emit writes one status.json tick. retire=true means the fleet has been
// stopped for >10 minutes (the caller's loop exits and removes its pidfile;
// `emit --once` maps it to exit code 3 — a frozen contract).
func (e *Emitter) Emit() (retire bool, err error) {
	repo := e.Repo
	nsDir := filepath.Join(repo, ".nightshift")

	// Re-read the task rows every tick: allRows() caches within a single Emit
	// (it's called for each status count + the parked scan), but the emit loop
	// reuses one Emitter across ticks, so a stale cache would freeze the
	// open/done/review counts at the run-start snapshot while work merges.
	e.rows = nil

	// Resolve this run's identity up front — the worker loops scope cards to it.
	orchPID := orchestratorPID(repo)
	running := orchPID != ""
	prior := readPriorStatus(nsDir)
	updated := Now().UTC().Format("2006-01-02T15:04:05Z")
	runID, started, resetHistory := RunIdentity(prior, running, orchPID, updated)
	startedAt, _ := parseISO(started) // responses older than this are a prior run's

	sessions := sh("", "tmux", "ls")
	var workers []Worker
	var rateIn, rateOut, cumIn, cumOut int64
	cumByModel := map[string][4]int64{}
	addCumulative := func(wt string) {
		in, out, byModel := e.tokenTotal(wt)
		cumIn += in
		cumOut += out
		for model, counts := range byModel {
			row := cumByModel[model]
			for k := 0; k < 4; k++ {
				row[k] += counts[k]
			}
			cumByModel[model] = row
		}
	}
	taskOf := func(branch string) string {
		branch = strings.TrimSpace(branch)
		if strings.HasPrefix(branch, "todo/") {
			return branch[5:]
		}
		if branch == "" {
			return "—"
		}
		return branch
	}

	for i := 0; strings.Contains(sessions, fmt.Sprintf("ns-w%d", i)); i++ {
		sess, wt := fmt.Sprintf("ns-w%d", i), fmt.Sprintf("%s-w%d", repo, i)
		if worktreeRun(wt) != runID {
			continue // a prior run's leftover worktree — not part of this run
		}
		pane := sh("", "tmux", "capture-pane", "-t", sess, "-p")
		branch := sh("", "git", "-C", wt, "branch", "--show-current")
		rIn, rOut := e.tokenRate(wt, 60*time.Second)
		rateIn += rIn
		rateOut += rOut
		addCumulative(wt)
		state := "idle"
		if strings.Contains(pane, "esc to interrupt") {
			state = "working"
		}
		workers = append(workers, Worker{
			I: i, State: state, Task: taskOf(branch), TIn: rIn, TOut: rOut,
			Pane: lastLines(pane, 45), Responses: e.recentResponses(wt, 40, 8, startedAt),
		})
	}

	// Headless backend: no tmux sessions — reconstruct from worktrees.
	if len(workers) == 0 {
		for j := 0; ; j++ {
			wt := fmt.Sprintf("%s-w%d", repo, j)
			if st, err := os.Stat(wt); err != nil || !st.IsDir() {
				break
			}
			if worktreeRun(wt) != runID {
				continue // a prior run's leftover worktree — not part of this run
			}
			branch := sh("", "git", "-C", wt, "branch", "--show-current")
			rIn, rOut := e.tokenRate(wt, 60*time.Second)
			rateIn += rIn
			rateOut += rOut
			addCumulative(wt)
			pane := ""
			if b, err := os.ReadFile(filepath.Join(wt, ".nightshift", "turn.log")); err == nil {
				pane = lastLines(string(b), 45)
			}
			if pane == "" {
				pane = "(headless — the last turn's output appears here)"
			}
			state := "idle"
			if rOut > 0 { // billing tokens right now = a turn is mid-flight
				state = "working"
			}
			workers = append(workers, Worker{
				I: j, State: state, Task: taskOf(branch), TIn: rIn, TOut: rOut,
				Pane: pane, Responses: e.recentResponses(wt, 40, 8, startedAt),
			})
		}
	}
	if workers == nil {
		workers = []Worker{}
	}

	sh("", "git", "-C", repo, "fetch", "-q", "origin")
	var merges []string
	for _, l := range strings.Split(sh("", "git", "-C", repo, "log", "--oneline", "origin/main..origin/nightshift"), "\n") {
		if strings.Contains(strings.ToLower(l), "merge") {
			merges = append(merges, l)
			if len(merges) == 14 {
				break
			}
		}
	}
	if merges == nil {
		merges = []string{}
	}

	logTail := []string{}
	if b, err := os.ReadFile(filepath.Join(nsDir, "orchestrator.log")); err == nil {
		lines := strings.Split(string(b), "\n")
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		if len(lines) > 16 {
			lines = lines[len(lines)-16:]
		}
		logTail = lines
	}

	// held tasks: genuine blocks (the banner) vs deliberate focus-parks (count)
	slug := strings.TrimSpace(sh("", "git", "-C", repo, "remote", "get-url", "origin"))
	slug = gitSuffix.ReplaceAllString(slug, "")
	if m := slugTail.FindStringSubmatch(slug); m != nil {
		slug = m[1]
	} else {
		slug = ""
	}
	parked := []Parked{}
	parkedCount := 0
	for _, r := range e.allRows() {
		if r[0] != "held" {
			continue
		}
		show := e.TodoOutput("show", r[1])
		reason := ""
		if m := reasonRe.FindStringSubmatch(show); m != nil {
			reason = strings.TrimSpace(m[1])
		}
		if parkedRe.MatchString(reason) { // deliberate focus-park, not a "needs you"
			parkedCount++
			continue
		}
		url := ""
		if m := prRe.FindStringSubmatch(show); m != nil {
			url = m[1]
		}
		if url == "" && slug != "" &&
			strings.TrimSpace(sh("", "git", "-C", repo, "ls-remote", "--heads", "origin", "todo/"+r[1])) != "" {
			url = fmt.Sprintf("https://github.com/%s/compare/nightshift...todo/%s?expand=1", slug, r[1])
		}
		parked = append(parked, Parked{ID: r[1], Reason: reason, URL: url})
	}

	hist := []HistPoint{}
	if !resetHistory {
		if raw, ok := prior["history"].([]any); ok {
			for _, p := range raw {
				if m, ok := p.(map[string]any); ok {
					t, _ := m["t"].(string)
					o, _ := m["out"].(float64)
					in, _ := m["in"].(float64)
					hist = append(hist, HistPoint{T: t, Out: int64(o), In: int64(in)})
				}
			}
		}
	}
	minute := Now().Format("15:04")
	point := HistPoint{T: minute, Out: rateOut, In: rateIn}
	if n := len(hist); n > 0 && hist[n-1].T == minute {
		hist[n-1] = point // same clock-minute → keep the latest sample
	} else {
		hist = append(hist, point)
	}
	if len(hist) > 90 {
		hist = hist[len(hist)-90:]
	}

	stoppedAt := ""
	if !running {
		stoppedAt, _ = prior["stopped_at"].(string)
		if stoppedAt == "" {
			stoppedAt = updated
		}
	}

	byModel := map[string][]float64{}
	for m, row := range cumByModel {
		byModel[m] = []float64{float64(row[0]), float64(row[1]), float64(row[2]), float64(row[3])}
	}
	only := e.onlySet() // scope queue counts to a --only run's launched subset
	doc := Doc{
		Updated: updated, StoppedAt: stoppedAt, RunID: runID, Started: started,
		Project: filepath.Base(repo), Running: running,
		Queue:       QueueCounts{Open: e.count("open", only), Done: e.count("done", only), Review: e.count("review", only)},
		TokensMin:   TokenPair{In: rateIn, Out: rateOut},
		TokensTotal: TokenPair{In: cumIn, Out: cumOut},
		CostTotal:   pricing.CostUSD(byModel),
		History:     hist, Parked: parked, ParkedCount: parkedCount,
		Workers: workers, Nightshift: merges, Log: logTail,
	}

	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		return false, err
	}
	// Per-PID temp + rename: concurrent writers must never publish a partial file.
	tmp := filepath.Join(nsDir, fmt.Sprintf("status.json.%d.tmp", os.Getpid()))
	b, err := json.Marshal(doc)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, filepath.Join(nsDir, "status.json")); err != nil {
		os.Remove(tmp)
		return false, err
	}

	// retire when stopped >10 min (any end-of-run path, not just `stop`)
	if stoppedAt != "" {
		if t, ok := parseISO(stoppedAt); ok && Now().UTC().Sub(t) > 10*time.Minute {
			return true, nil
		}
	}
	return false, nil
}
