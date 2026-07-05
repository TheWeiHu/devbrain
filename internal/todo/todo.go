// Package todo ports scripts/todo.sh verb-for-verb: the markdown TODO queue.
// One markdown file per task under $DATA/projects/<project>/todo/<id>.md;
// priority-ranked; `claim` marks a task taken so a parallel run skips it.
//
// Parity is byte-for-byte on stdout/stderr/exit codes against the retired bash
// todo.sh (pinned by testdata/golden + internal/todo/golden_cli_test.go).
package todo

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/frontmatter"
	"github.com/TheWeiHu/devbrain/internal/procutil"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
	"github.com/TheWeiHu/devbrain/internal/task"
)

// Now is the injectable clock: every timestamp and lease/TTL comparison flows
// through it so tests can pin time.
var Now = func() time.Time { return time.Now() }

// helpText is lines 2-22 of the legacy todo.sh header with the `# ` prefix
// stripped — the exact `todo help` output.
const helpText = `devbrain — TODO queue. One markdown file per task (conflict-free sync like the
log); priority-ranked; ` + "`claim`" + ` marks a task taken so a parallel run skips it.
Tasks are created by /distill and worked by /continue — this CLI is the substrate.

  $DEVBRAIN_DATA/projects/<project>/todo/<id>.md

  todo add "<title>" [-p N] [-b "body"]   create (prints id); priority 0-100, default 0
  todo list [status]                      tasks by status (default open; 'all'=any), priority first
  todo next                               id of the top open task (empty if none)
  todo show <id>                          print a task file
  todo edit <id> [-t "title"] [-b "body"] rewrite the title heading and/or the body
  todo prio <id> <N>                      reprioritize an existing task (priority 0-100)
  todo claim <id>                         mark open -> taken (exit 2 if not open)
  todo review <id> <pr>                   mark -> review (PR open, awaiting merge); records pr
  todo hold <id> [reason]                 mark -> held (needs a human: blocked/parked); records reason
  todo approve <id>                        greenlight: set approved:true + reopen (worker may download/install/network)
  todo done <id> [--force]                close it (only after the PR merges); stamps done_at.
                                          Refuses without a recorded pr: unless --force
                                          (nightshift direct-merge, won't-do)
  todo archive [days]                     move done tasks older than N days (default 30) into archive/ (off list; dashboard folds them)
  todo self-heal [status...]              close open/taken tasks whose recorded PR has merged (zombie sweep)
  todo release <id>                       taken/review/held -> open (un-claim / un-hold)
  todo reopen <id> [reason]               FORCE done -> open (work verified absent; regenerate)
  todo context <id>                       attach a synthesized "## Context" body section (reads stdin)
`

// cli holds one invocation's state: identity, streams, and the once-per-process
// derive/lease caches (todo.sh's DERIVE_READY / NOW_EPOCH globals).
type cli struct {
	dir     string // $DATA/projects/<project>/todo
	project string
	stdout  io.Writer
	stderr  io.Writer
	stdin   io.Reader

	deriveReady bool
	deriveOn    bool
	doneIDs     map[string]bool
	branchIDs   map[string]bool
	nowEpochSet bool
	nowEpoch    int64
}

// Run executes one todo verb. Exit codes mirror the script: 0 ok, 1 die,
// 2 claim-on-non-open.
func Run(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	cwd, _ := os.Getwd()
	project := projectkey.ProjectKey(cwd)
	if project == "" {
		project = "unknown"
	}
	c := &cli{
		dir:     filepath.Join(config.DataDir(), "projects", project, "todo"),
		project: project,
		stdout:  stdout,
		stderr:  stderr,
		stdin:   stdin,
	}
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "add":
		return c.add(args)
	case "list":
		return c.list(args)
	case "next":
		return c.next()
	case "show":
		return c.show(args)
	case "edit":
		return c.edit(args)
	case "prio", "reprioritize":
		return c.prio(args)
	case "claim":
		return c.claim(args)
	case "review":
		return c.review(args)
	case "hold":
		return c.hold(args)
	case "approve":
		return c.approve(args)
	case "note":
		return c.note(args)
	case "context":
		return c.context(args)
	case "done", "close":
		return c.doneVerb(args)
	case "archive":
		return c.archive(args)
	case "self-heal", "selfheal", "heal":
		return c.selfHeal(args)
	case "release", "unclaim":
		return c.release(args)
	case "reopen":
		return c.reopen(args)
	case "help", "-h", "--help":
		fmt.Fprint(c.stdout, helpText)
		return 0
	default:
		fmt.Fprint(c.stderr, helpText)
		return c.die("unknown command: " + cmd)
	}
}

func (c *cli) die(msg string) int {
	fmt.Fprintf(c.stderr, "todo: %s\n", msg)
	return 1
}

// nowStamp is `date -u +%Y-%m-%dT%H:%M:%SZ`.
func nowStamp() string {
	return Now().UTC().Format("2006-01-02T15:04:05Z")
}

// epochOf parses a nowStamp timestamp to unix seconds, 0 on failure.
func epochOf(s string) int64 {
	t, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// slugify: title -> 40-char kebab slug. Byte-wise like the tr/sed pipeline:
// upper->lower, space->dash, keep alnum+dash, collapse dashes, trim edge
// dashes, cap at 40 (a dash re-exposed by the cap is kept, like cut -c1-40).
func slugify(title string) string {
	kept := make([]byte, 0, len(title))
	for i := 0; i < len(title); i++ {
		ch := title[i]
		switch {
		case ch >= 'A' && ch <= 'Z':
			ch += 'a' - 'A'
		case ch == ' ':
			ch = '-'
		}
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' {
			kept = append(kept, ch)
		}
	}
	// collapse runs of dashes
	out := make([]byte, 0, len(kept))
	for _, ch := range kept {
		if ch == '-' && len(out) > 0 && out[len(out)-1] == '-' {
			continue
		}
		out = append(out, ch)
	}
	s := strings.Trim(string(out), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// onlyMatch ports DEVBRAIN_TODO_ONLY scoping: comma/space-separated tokens,
// each a full slug or a bare 4-digit number; unset/empty = no filter.
func onlyMatch(id string) bool {
	only := os.Getenv("DEVBRAIN_TODO_ONLY")
	if only == "" {
		return true
	}
	num := id
	if i := strings.Index(id, "-"); i >= 0 {
		num = id[:i]
	}
	for _, tok := range strings.Fields(strings.ReplaceAll(only, ",", " ")) {
		if tok == id || tok == num {
			return true
		}
	}
	return false
}

// fixedSetRepo returns the checkout path of a live fixed-set (--only) nightshift
// run — the dir holding WriteOnlySet's persistent marker (.nightshift/only.txt,
// non-empty), found walking up from cwd — or "" if none is active. only.txt
// deliberately outlives the run (the status emitter scopes counts to it), so the
// marker alone doesn't mean "run live": the dir must also hold a live
// orchestrator.pid, or every post-run add would be parked with no run left to
// release it. That dir IS the run's o.Opt.Repo, so a task parked with
// FenceNote(repo) is released only by that run's Unfence. Worker worktrees are
// SIBLINGS of the base repo (repo-wN), so the walk never crosses from one
// fleet's worktree into another's.
func fixedSetRepo() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, ".nightshift", "only.txt")); err == nil &&
			strings.TrimSpace(string(b)) != "" {
			if pid, ok := procutil.ReadPidfile(filepath.Join(dir, ".nightshift", "orchestrator.pid")); ok && procutil.Alive(pid) {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ── file helpers ─────────────────────────────────────────────────────────────

func (c *cli) taskPath(id string) string { return filepath.Join(c.dir, id+".md") }

func (c *cli) readTask(id string) (string, bool) {
	b, err := os.ReadFile(c.taskPath(id))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// writeTask writes content via tmp+rename (set_field's mktemp/mv), adding the
// trailing newline awk's `print` guarantees on non-empty output. The tmp name
// must be UNIQUE per writer (os.CreateTemp, like the legacy mktemp): a fixed
// `<id>.md.tmp` would let two concurrent writers — the orchestrator, a
// worker's own `devbrain todo`, a human — clobber each other's staging file.
func (c *cli) writeTask(id, content string) error {
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	f, err := os.CreateTemp(filepath.Dir(c.taskPath(id)), "."+id+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, c.taskPath(id)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// awkLines splits content the way awk sees lines: a trailing newline does not
// produce a final empty record.
func awkLines(content string) []string {
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// isFence matches awk /^---[[:space:]]*$/.
func isFence(line string) bool {
	return strings.HasPrefix(line, "---") && strings.TrimRight(line[3:], " \t\v\f\r") == ""
}

// allocFile allocates the next NNNN-slug id: scan the max NNNN, then an
// O_EXCL create loop beats a parallel writer to the slot.
func (c *cli) allocFile(slug string) (string, error) {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return "", err
	}
	seq := 0
	// Scan the live dir AND archive/ so a moved-away top id can't be reissued.
	for _, dir := range []string{c.dir, filepath.Join(c.dir, "archive")} {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			name := e.Name()
			if ok, _ := path.Match("[0-9][0-9][0-9][0-9]-*.md", name); !ok {
				continue
			}
			if n, err := strconv.Atoi(name[:4]); err == nil && n > seq {
				seq = n
			}
		}
	}
	for {
		seq++
		id := fmt.Sprintf("%04d-%s", seq, slug)
		f, err := os.OpenFile(c.taskPath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return id, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
}

// ── derived status (nightshift mode) ─────────────────────────────────────────

var (
	deriveDoneRe   = regexp.MustCompile(`^nightshift: merge todo/([0-9]{4}-[a-z0-9-]+) into nightshift$`)
	deriveBranchRe = regexp.MustCompile(`^refs/remotes/origin/todo/([0-9]{4}-[a-z0-9-]+)$`)
)

// gitOut runs git in the process cwd, returning trimmed stdout ("" on failure).
func gitOut(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitOK(args ...string) bool {
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}

// deriveInit primes done/review sets from origin's nightshift + todo/* refs
// (DEVBRAIN_TODO_DERIVE_GIT=1), fetching at most once per process and skipping
// the fetch entirely when FETCH_HEAD is fresher than DEVBRAIN_TODO_FETCH_TTL.
func (c *cli) deriveInit() {
	if c.deriveReady {
		return
	}
	c.deriveReady = true
	if os.Getenv("DEVBRAIN_TODO_DERIVE_GIT") != "1" {
		return
	}
	if !gitOK("rev-parse", "--is-inside-work-tree") {
		return
	}
	ttl, _ := strconv.Atoi(os.Getenv("DEVBRAIN_TODO_FETCH_TTL"))
	skip := false
	if ttl > 0 {
		fh := filepath.Join(gitOut("rev-parse", "--git-dir"), "FETCH_HEAD")
		if fi, err := os.Stat(fh); err == nil && Now().Unix()-fi.ModTime().Unix() < int64(ttl) {
			skip = true
		}
	}
	if !skip {
		cmd := exec.Command("git", "fetch", "-q", "origin",
			"refs/heads/nightshift:refs/remotes/origin/nightshift",
			"refs/heads/todo/*:refs/remotes/origin/todo/*")
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		_ = cmd.Run() // || true
	}
	if !gitOK("rev-parse", "--verify", "-q", "refs/remotes/origin/nightshift") {
		return
	}
	c.deriveOn = true
	c.doneIDs, c.branchIDs = map[string]bool{}, map[string]bool{}
	for _, s := range strings.Split(gitOut("log", "--format=%s", "refs/remotes/origin/nightshift"), "\n") {
		if m := deriveDoneRe.FindStringSubmatch(s); m != nil {
			c.doneIDs[m[1]] = true
		}
	}
	for _, s := range strings.Split(gitOut("for-each-ref", "--format=%(refname)", "refs/remotes/origin/todo"), "\n") {
		if m := deriveBranchRe.FindStringSubmatch(s); m != nil {
			c.branchIDs[m[1]] = true
		}
	}
}

// leaseAlive: claimed_at set and 0 <= now-claimed_at < DEVBRAIN_TODO_CLAIM_TTL
// (default 5400). The now epoch is cached once per process, like NOW_EPOCH.
func (c *cli) leaseAlive(t *task.Task) bool {
	ttl := int64(5400)
	if s := os.Getenv("DEVBRAIN_TODO_CLAIM_TTL"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return false // bash: non-numeric ttl fails the -lt test
		}
		ttl = v
	}
	ca := t.Raw("claimed_at")
	if ca == "" {
		return false
	}
	if !c.nowEpochSet {
		c.nowEpoch, c.nowEpochSet = Now().Unix(), true
	}
	age := c.nowEpoch - epochOf(ca)
	return age >= 0 && age < ttl
}

// effectiveStatus: held wins; with derive on, git evidence decides
// done/review, a live lease means taken, else open; otherwise the stored
// status (empty = open).
func (c *cli) effectiveStatus(t *task.Task, id string) string {
	st := t.Status
	if st == "held" {
		return "held"
	}
	c.deriveInit()
	if !c.deriveOn {
		if st == "" {
			return "open"
		}
		return st
	}
	if c.doneIDs[id] {
		return "done"
	}
	if c.branchIDs[id] {
		return "review"
	}
	if c.leaseAlive(t) {
		return "taken"
	}
	return "open"
}

// ── rows + sort ──────────────────────────────────────────────────────────────

type row struct{ prio, created, id, st, title string }

func (r row) line() string {
	return r.prio + "\t" + r.created + "\t" + r.id + "\t" + r.st + "\t" + r.title
}

// numVal parses a leading number the way `sort -n` does: optional blanks and
// minus sign, digits, optional decimal part; empty/non-numeric compare as 0.
func numVal(s string) float64 {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	j := i
	if j < len(s) && s[j] == '-' {
		j++
	}
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j < len(s) && s[j] == '.' {
		j++
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s[i:j]), 64)
	if err != nil {
		return 0
	}
	return v
}

// sortRows emulates `sort -t\t -k1,1nr -k2,2`: priority numeric desc, created
// string asc, then sort's whole-line last-resort comparison for remaining ties.
func sortRows(rows []row) {
	sort.Slice(rows, func(i, j int) bool {
		pi, pj := numVal(rows[i].prio), numVal(rows[j].prio)
		if pi != pj {
			return pi > pj
		}
		if rows[i].created != rows[j].created {
			return rows[i].created < rows[j].created
		}
		return rows[i].line() < rows[j].line()
	})
}

// rows lists tasks matching the status filter ("all" = any), sorted.
func (c *cli) rows(want string) []row {
	ents, err := os.ReadDir(c.dir)
	if err != nil {
		return nil
	}
	c.deriveInit() // prime ONCE per process, like the bash rows()
	var out []row
	for _, e := range ents {
		name := e.Name()
		// bash glob *.md: no dotfiles
		if !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if !onlyMatch(id) {
			continue
		}
		t, err := task.Load(c.taskPath(id), c.project)
		if err != nil {
			continue
		}
		st := c.effectiveStatus(t, id)
		if want != "all" && st != want {
			continue
		}
		out = append(out, row{
			prio:    t.Raw("priority"),
			created: t.Created,
			id:      id,
			st:      st,
			title:   t.Title,
		})
	}
	sortRows(out)
	return out
}

// ── verbs ────────────────────────────────────────────────────────────────────

func (c *cli) add(args []string) int {
	title, prio, body := "", "0", ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-p" || a == "--priority":
			if i+1 >= len(args) {
				return 1 // bash: unbound $2 under set -u
			}
			prio = args[i+1]
			i++
		case a == "-b" || a == "--body":
			if i+1 >= len(args) {
				return 1
			}
			body = args[i+1]
			i++
		case strings.HasPrefix(a, "-"):
			return c.die("unknown flag: " + a)
		default:
			if title == "" {
				title = a
			} else {
				title += " " + a
			}
		}
	}
	if title == "" {
		return c.die("add needs a title")
	}
	slug := slugify(title)
	if slug == "" {
		slug = "task"
	}
	id, err := c.allocFile(slug)
	if err != nil {
		return c.die(err.Error())
	}
	// A fixed-set (--only) nightshift run must keep its "only these tasks"
	// contract: a task added mid-run is parked as held+marked so `next` can't
	// hand it out. The reason is repo-tagged with FenceNote so only that run's
	// Unfence releases it (task.FenceRepo scoping) when the run stops.
	content := fmt.Sprintf("---\nid: %s\nstatus: open\npriority: %s\ncreated: %s\nclaimed_by:\nclaimed_at:\npr:\n",
		id, prio, nowStamp())
	if repo := fixedSetRepo(); repo != "" {
		content = fmt.Sprintf("---\nid: %s\nstatus: held\npriority: %s\ncreated: %s\nclaimed_by:\nclaimed_at:\npr:\nreason: %s\n",
			id, prio, nowStamp(), task.FenceNote(repo))
	}
	content += fmt.Sprintf("---\n\n# %s\n", title)
	if body != "" {
		content += "\n" + body + "\n"
	}
	if err := os.WriteFile(c.taskPath(id), []byte(content), 0o644); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintln(c.stdout, id)
	return 0
}

func (c *cli) list(args []string) int {
	want := "open"
	if len(args) > 0 && args[0] != "" {
		want = args[0]
	}
	if want != "all" && !slices.Contains(task.Statuses, want) {
		return c.die(fmt.Sprintf("list: bad status: %s (open|taken|review|held|done|all)", want))
	}
	hdr := "queue: " + c.project
	if want != "open" {
		hdr += " (" + want + ")"
	}
	fmt.Fprintln(c.stdout, hdr)
	rows := c.rows(want)
	if len(rows) == 0 {
		fmt.Fprintln(c.stdout, "  (empty)")
		return 0
	}
	for _, r := range rows {
		if want == "open" {
			fmt.Fprintf(c.stdout, "  [%3s] %-32s %s\n", r.prio, r.id, r.title)
		} else {
			fmt.Fprintf(c.stdout, "  [%3s] %-7s %-32s %s\n", r.prio, r.st, r.id, r.title)
		}
	}
	return 0
}

func (c *cli) next() int {
	rows := c.rows("open")
	if len(rows) > 0 {
		fmt.Fprintln(c.stdout, rows[0].id)
	}
	return 0
}

// argID sanitizes the id argument (devbrain_sanitize); "" means missing.
func argID(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return projectkey.Sanitize(args[0])
}

func (c *cli) show(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("show needs an id")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	io.WriteString(c.stdout, content)
	return 0
}

// editBody ports awk 'p&&NF{f=1} f{print} /^# /{p=1}' plus the command
// substitution's trailing-newline strip: the body starting at the first
// non-blank line after the `# ` title.
func editBody(content string) string {
	p, f := false, false
	var out []string
	for _, line := range awkLines(content) {
		if p && strings.Trim(line, " \t") != "" {
			f = true
		}
		if f {
			out = append(out, line)
		}
		if strings.HasPrefix(line, "# ") {
			p = true
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func (c *cli) edit(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("edit needs an id")
	}
	args = args[1:]
	nt, nb := "", ""
	st, sb := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-t", "--title":
			if i+1 >= len(args) {
				return 1
			}
			nt, st = args[i+1], true
			i++
		case "-b", "--body":
			if i+1 >= len(args) {
				return 1
			}
			nb, sb = args[i+1], true
			i++
		default:
			return c.die("edit: bad flag: " + args[i])
		}
	}
	if !st && !sb {
		return c.die("edit needs -t and/or -b")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	if !st {
		nt = task.Parse(content, c.project).Title
	}
	if !sb {
		nb = editBody(content)
	}
	var b strings.Builder
	n := 0
	for _, line := range awkLines(content) { // frontmatter, verbatim
		b.WriteString(line + "\n")
		if isFence(line) {
			n++
			if n == 2 {
				break
			}
		}
	}
	b.WriteString("\n# " + nt + "\n")
	if nb != "" {
		b.WriteString("\n" + nb + "\n")
	}
	if err := c.writeTask(id, b.String()); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintln(c.stdout, "edited "+id)
	return 0
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func (c *cli) prio(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("prio needs an id")
	}
	p := ""
	if len(args) > 1 {
		p = args[1]
	}
	if !allDigits(p) {
		return c.die("prio needs a number 0-100")
	}
	if v, err := strconv.ParseInt(p, 10, 64); err != nil || v > 100 {
		return c.die(fmt.Sprintf("prio out of range: %s (must be 0-100)", p))
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	if err := c.writeTask(id, frontmatter.SetField(content, "priority", p)); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintf(c.stdout, "prio %s -> %s\n", id, p)
	return 0
}

func claimedBy() string {
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	} else {
		who = os.Getenv("USER")
	}
	host := "host"
	if h, err := os.Hostname(); err == nil && h != "" {
		host = strings.SplitN(h, ".", 2)[0] // hostname -s
	}
	return who + "@" + host
}

func (c *cli) claim(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("claim needs an id")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	if st := c.effectiveStatus(task.Parse(content, c.project), id); st != "open" {
		fmt.Fprintf(c.stderr, "todo: %s is %s\n", id, st)
		return 2
	}
	content = frontmatter.SetField(content, "status", "taken")
	content = frontmatter.SetField(content, "claimed_by", claimedBy())
	content = frontmatter.SetField(content, "claimed_at", nowStamp())
	if err := c.writeTask(id, content); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintln(c.stdout, "claimed "+id)
	return 0
}

func (c *cli) review(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("review needs an id")
	}
	pr := ""
	if len(args) > 1 {
		pr = args[1]
	}
	if pr == "" {
		return c.die("review records the PR: todo review <id> <pr-url> — open the PR first")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	content = frontmatter.SetField(content, "status", "review")
	content = frontmatter.SetField(content, "pr", pr)
	if err := c.writeTask(id, content); err != nil {
		return c.die(err.Error())
	}
	out := "review " + id
	if pr != "" {
		out += " (pr " + pr + ")"
	}
	fmt.Fprintln(c.stdout, out)
	return 0
}

func (c *cli) hold(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("hold needs an id")
	}
	reason := strings.Join(args[1:], " ")
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	content = frontmatter.SetField(content, "status", "held")
	if reason != "" {
		content = frontmatter.SetField(content, "reason", reason)
	}
	if err := c.writeTask(id, content); err != nil {
		return c.die(err.Error())
	}
	out := "held " + id
	if reason != "" {
		out += " (" + reason + ")"
	}
	fmt.Fprintln(c.stdout, out)
	return 0
}

func (c *cli) approve(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("approve needs an id")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	content = frontmatter.SetField(content, "approved", "true")
	content = frontmatter.SetField(content, "status", "open")
	content = frontmatter.SetField(content, "claimed_by", "")
	// Clear the old merged-PR record + done stamp so the self-heal sweep
	// doesn't re-close this reopened task as a zombie.
	content = frontmatter.SetField(content, "pr", "")
	content = frontmatter.SetField(content, "done_at", "")
	if err := c.writeTask(id, content); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintf(c.stdout, "approved %s — unattended execution authorized; back to open\n", id)
	return 0
}

func (c *cli) note(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("note needs an id")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	content = frontmatter.SetField(content, "last_failure", strings.Join(args[1:], " "))
	if err := c.writeTask(id, content); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintln(c.stdout, "noted "+id)
	return 0
}

func (c *cli) context(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("context needs an id")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	if f, isFile := c.stdin.(*os.File); isFile {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return c.die("context reads the body on stdin (pipe or heredoc it)") // don't hang on a tty
		}
	}
	data, _ := io.ReadAll(c.stdin)
	ctx := strings.TrimRight(string(data), "\n") // $(cat)
	if ctx == "" {
		return c.die("context needs the body on stdin")
	}
	// task minus any prior block; trailing blank lines stripped like $(awk …)
	var kept []string
	for _, line := range awkLines(content) {
		if strings.HasPrefix(line, "## Context (synthesized ") {
			break
		}
		kept = append(kept, line)
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	out := fmt.Sprintf("%s\n\n## Context (synthesized %s)\n\n%s\n", body, nowStamp(), ctx)
	if err := os.WriteFile(c.taskPath(id), []byte(out), 0o644); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintln(c.stdout, "context "+id)
	return 0
}

func (c *cli) doneVerb(args []string) int {
	force := false
	rest := args[:0:0]
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		rest = append(rest, a)
	}
	id := argID(rest)
	if id == "" {
		return c.die("done needs an id")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	// Done means the PR merged. A close with no recorded PR is the drift this
	// guards against (work pushed or merged without review); --force is the
	// deliberate exception (nightshift direct-merge, won't-do).
	if !force && task.Parse(content, c.project).PR == "" {
		return c.die(id + " has no recorded PR — open one and `todo review " + id + " <pr-url>` first, or `todo done " + id + " --force` for work that legitimately has none")
	}
	content = frontmatter.SetField(content, "status", "done")
	content = frontmatter.SetField(content, "done_at", nowStamp())
	if err := c.writeTask(id, content); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintln(c.stdout, "done "+id)
	return 0
}

// archive moves done tasks whose done_at is older than N days (default 30) into
// c.dir/archive/. `list` reads todo/ non-recursively so archived tasks drop off
// the CLI; the dashboard still serves them (AllTasks scans archive/) folded
// under their own "archived" section, so they stay one click away and
// searchable. Operates on the STORED status + done_at (the reliable raw done
// signal, not the git-derived view) so a monthly pass is deterministic. Undated
// or too-recent dones are left in place; ids stay counted by allocFile.
func (c *cli) archive(args []string) int {
	days := 30
	if len(args) > 0 && args[0] != "" {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 {
			return c.die("archive days must be a non-negative integer")
		}
		days = n
	}
	cutoff := Now().UTC().AddDate(0, 0, -days).Unix()
	dest := filepath.Join(c.dir, "archive")
	ents, _ := os.ReadDir(c.dir)
	moved := 0
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || strings.HasPrefix(name, ".") {
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		content, ok := c.readTask(id)
		if !ok {
			continue
		}
		t := task.Parse(content, c.project)
		if t.Status != "done" || t.DoneAt == "" {
			continue
		}
		if da := epochOf(t.DoneAt); da == 0 || da >= cutoff {
			continue // undated or not old enough — leave on the board
		}
		destPath := filepath.Join(dest, name)
		if _, err := os.Stat(destPath); err == nil {
			fmt.Fprintf(c.stderr, "todo: archive/%s already exists — skipping\n", name)
			continue // never clobber an already-archived id
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return c.die(err.Error())
		}
		if err := os.Rename(c.taskPath(id), destPath); err != nil {
			return c.die(err.Error())
		}
		fmt.Fprintln(c.stdout, "archive: "+id)
		moved++
	}
	fmt.Fprintf(c.stdout, "archive: %d task(s) archived\n", moved)
	return 0
}

// prState: merge-state of a PR ref -> MERGED|OPEN|CLOSED|"". Overridable via
// DEVBRAIN_PR_STATE_CMD (a single executable path, run directly with the pr
// ref as its one argument).
func (c *cli) prState(pr string) string {
	var cmd *exec.Cmd
	if custom := os.Getenv("DEVBRAIN_PR_STATE_CMD"); custom != "" {
		cmd = exec.Command(custom, pr)
		cmd.Stderr = c.stderr // bash leaves the custom cmd's stderr attached
	} else {
		cmd = exec.Command("gh", "pr", "view", pr, "--json", "state", "-q", ".state")
		cmd.Stderr = io.Discard // 2>/dev/null
	}
	out, _ := cmd.Output()
	return strings.TrimRight(string(out), "\n")
}

func (c *cli) selfHeal(args []string) int {
	if os.Getenv("DEVBRAIN_PR_STATE_CMD") == "" {
		if _, err := exec.LookPath("gh"); err != nil {
			return c.die("self-heal needs gh (GitHub CLI)")
		}
	}
	statuses := strings.Fields(strings.Join(args, " "))
	if len(statuses) == 0 {
		statuses = []string{"open", "taken"}
	}
	healed := 0
	for _, st := range statuses {
		for _, r := range c.rows(st) {
			content, ok := c.readTask(r.id)
			if !ok {
				continue
			}
			pr := task.Parse(content, c.project).PR
			if pr == "" || c.prState(pr) != "MERGED" {
				continue
			}
			content = frontmatter.SetField(content, "status", "done")
			content = frontmatter.SetField(content, "done_at", nowStamp())
			if err := c.writeTask(r.id, content); err != nil {
				return c.die(err.Error())
			}
			fmt.Fprintf(c.stdout, "self-heal: closed %s (pr merged: %s)\n", r.id, pr)
			healed++
		}
	}
	fmt.Fprintf(c.stdout, "self-heal: %d task(s) closed\n", healed)
	return 0
}

// clearOnReopen wipes claim/pr/done/reason state when a task goes back to open.
func clearOnReopen(content string) string {
	content = frontmatter.SetField(content, "status", "open")
	content = frontmatter.SetField(content, "claimed_by", "")
	content = frontmatter.SetField(content, "claimed_at", "")
	content = frontmatter.SetField(content, "pr", "")
	content = frontmatter.SetField(content, "done_at", "")
	content = frontmatter.SetField(content, "reason", "")
	return content
}

func (c *cli) release(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("release needs an id")
	}
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	// `done` is terminal: never reopen a completed task.
	if task.Parse(content, c.project).Status == "done" {
		fmt.Fprintf(c.stderr, "todo: %s already done — not releasing\n", id)
		return 0
	}
	if err := c.writeTask(id, clearOnReopen(content)); err != nil {
		return c.die(err.Error())
	}
	fmt.Fprintln(c.stdout, "released "+id)
	return 0
}

func (c *cli) reopen(args []string) int {
	id := argID(args)
	if id == "" {
		return c.die("reopen needs an id")
	}
	reason := strings.Join(args[1:], " ")
	content, ok := c.readTask(id)
	if !ok {
		return c.die("no such todo: " + id)
	}
	content = clearOnReopen(content)
	if reason != "" {
		content = frontmatter.SetField(content, "last_failure", reason)
	}
	if err := c.writeTask(id, content); err != nil {
		return c.die(err.Error())
	}
	out := "reopened " + id
	if reason != "" {
		out += " (" + reason + ")"
	}
	fmt.Fprintln(c.stdout, out)
	return 0
}
