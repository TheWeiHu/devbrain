package nightshift

// The `devbrain nightshift <verb>` CLI — the port of scripts/nightshift.
// start daemonizes the orchestrator by re-exec (`nightshift run`) since Go
// can't fork; watch keeps status.json fresh via a detached `nightshift emit`
// loop and wires the queue dashboard; stop reaps everything a hard-killed
// run can leave behind.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TheWeiHu/devbrain/internal/nightshift/status"
	"github.com/TheWeiHu/devbrain/internal/procutil"
	"github.com/TheWeiHu/devbrain/internal/todo"
)

const cliHelp = `nightshift — autonomous overnight loop for devbrain. One verb, no path-pasting.
Run it through the devbrain CLI:  devbrain nightshift <verb>

  start [REPO] [opts]       launch the fleet (forever) + open the dashboard
                            (add --no-watch to skip auto-opening it, e.g. headless/cron)
                            (add --only ID,ID to run ONLY those tasks then stop — no new
                             tasks; or just drag a selection onto the 🌙 in the dashboard)
  watch                     (re)open the live browser dashboard
  status                    quick text status
  review                    held tasks + why (need you) + what to do
  release <id>              un-hold a task (after you provide its dep)
  approve <id>  (alias: go) greenlight: let the fleet do its downloads/installs/network unattended
  drop <id>                 discard a held task (won't-do)
  say <i> <msg…>            steer worker i        (--tmux only)
  attach <i>                drop into worker i's session (--tmux only)
  stop                      stop the fleet + dashboard

Backends — how each worker runs claude (chosen at start):
  headless  (DEFAULT) — one claude -p per turn. The process IS the turn: simplest and
            most robust. Runs under your Claude Code subscription. Use this.
  --tmux    (fallback) — persistent interactive sessions you can attach + steer.
            Kept for ONE reason: if Anthropic ever bills claude -p separately from
            your subscription, interactive sessions keep workers on the plan.

REPO is remembered after start, so later verbs need no argument.
`

// RunCLI dispatches a nightshift verb. args excludes the leading "nightshift".
func RunCLI(args []string, stdout, stderr io.Writer) int {
	verb := "help"
	if len(args) > 0 {
		verb = args[0]
		args = args[1:]
	}
	switch verb {
	case "start":
		return cliStart(args, stdout, stderr)
	case "run": // plumbing: the orchestrator itself (foreground-capable)
		return cliRun(args, stdout, stderr)
	case "watch":
		return cliWatch(args, stdout, stderr)
	case "emit": // plumbing: the status emit loop (or --once)
		return cliEmit(args, stderr)
	case "status":
		return cliStatus(args, stdout, stderr)
	case "review":
		return cliReview(stdout, stderr)
	case "release", "drop", "approve", "go":
		return cliTaskVerb(verb, args, stdout, stderr)
	case "say", "attach":
		return cliTmuxVerb(verb, args, stdout, stderr)
	case "stop":
		return cliStop(args, stdout, stderr)
	case "internal":
		return RunInternal(args, stdout, stderr)
	default:
		fmt.Fprint(stdout, cliHelp)
		return 0
	}
}

// splitRepoArg pops a leading path argument (the script's /*|./*|~* case).
func splitRepoArg(args []string) (string, []string) {
	if len(args) > 0 {
		a := args[0]
		if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "./") || strings.HasPrefix(a, "~") {
			return a, args[1:]
		}
	}
	return "", args
}

// legacyOrchAlive detects a still-running bash orchestrator for the repo.
func legacyOrchAlive(repo string) bool {
	return exec.Command("pgrep", "-f", "nightshift-orchestrate.sh --repo "+repo).Run() == nil
}

// orchAlive reports whether ANY orchestrator (Go pidfile or legacy) runs on repo.
func orchAlive(repo string) bool {
	if pid, ok := procutil.ReadPidfile(filepath.Join(repo, ".nightshift", "orchestrator.pid")); ok && procutil.Alive(pid) {
		return true
	}
	return legacyOrchAlive(repo)
}

func cliStart(args []string, stdout, stderr io.Writer) int {
	repoArg, rest := splitRepoArg(args)
	repo := ResolveRepo(repoArg)
	if repo == "" {
		fmt.Fprintln(stderr, "nightshift: pass a repo: devbrain nightshift start <path>")
		return 1
	}
	SaveRepo(repo)
	mode, watch := "headless", true
	var oargs []string
	for _, a := range rest {
		switch a {
		case "--tmux":
			mode = "tmux"
			oargs = append(oargs, a)
		case "--headless":
			mode = "headless"
			oargs = append(oargs, a)
		case "--no-watch":
			watch = false
		case "--watch":
			watch = true
		default:
			oargs = append(oargs, a)
		}
	}
	if orchAlive(repo) {
		fmt.Fprintf(stdout, "already running on %s\n", repo)
		return 0
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "nightshift: %v\n", err)
		return 1
	}
	os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755)
	logF, err := os.OpenFile(filepath.Join(repo, ".nightshift", "orchestrator.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(stderr, "nightshift: %v\n", err)
		return 1
	}
	cmd := exec.Command(self, append([]string{"nightshift", "run", "--repo", repo}, oargs...)...)
	cmd.Stdout, cmd.Stderr = logF, logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach: survive this CLI exiting
	if err := cmd.Start(); err != nil {
		logF.Close()
		fmt.Fprintf(stderr, "nightshift: could not launch the orchestrator: %v\n", err)
		return 1
	}
	logF.Close()
	go cmd.Wait() // reap if it exits while we're still alive
	// A detached fail-fast exit (base red, no interpreter…) would die silently —
	// give it a beat, then surface WHY from its log.
	time.Sleep(2 * time.Second)
	if !procutil.Alive(cmd.Process.Pid) && !orchAlive(repo) {
		fmt.Fprintf(stderr, "🌙 nightshift FAILED to start on %s — the orchestrator exited immediately:\n", repo)
		if b, err := os.ReadFile(filepath.Join(repo, ".nightshift", "orchestrator.log")); err == nil {
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(lines) > 6 {
				lines = lines[len(lines)-6:]
			}
			for _, l := range lines {
				fmt.Fprintf(stderr, "   %s\n", l)
			}
		}
		return 1
	}
	if mode == "tmux" {
		fmt.Fprintf(stdout, "🌙 nightshift started on %s  ·  backend: interactive tmux\n", repo)
		fmt.Fprintln(stdout, "   why --tmux (the fallback): kept for ONE case — a future claude -p pricing change.")
		fmt.Fprintln(stdout, "   (bonus: devbrain nightshift attach <i> to watch/steer a worker live.)")
	} else {
		fmt.Fprintf(stdout, "🌙 nightshift started on %s  ·  backend: headless (claude -p)  [default]\n", repo)
		fmt.Fprintln(stdout, "   why -p: each turn is one claude -p — the process IS the turn: no tmux,")
		fmt.Fprintln(stdout, "      no turn-marker hook, no screen-scraping. Simplest + most robust.")
		fmt.Fprintln(stdout, "   not -p? add --tmux (run devbrain nightshift with no args for when you'd want that).")
	}
	if watch {
		fmt.Fprintln(stdout, "   opening dashboard…")
		return cliWatch([]string{repo}, stdout, stderr)
	}
	fmt.Fprintln(stdout, "   watch it:  devbrain nightshift watch   (auto-open skipped: --no-watch)")
	return 0
}

func cliRun(args []string, stdout, stderr io.Writer) int {
	opt, err := ParseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "orch: %v\n", err)
		return 1
	}
	if opt.Mode == "tmux" {
		if _, err := exec.LookPath("tmux"); err != nil {
			fmt.Fprintln(stderr, "orch: tmux not found (required for --tmux mode)")
			return 1
		}
	}
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintln(stderr, "orch: claude not found")
		return 1
	}
	o := NewOrch(opt, stdout)
	if err := o.ParseOnly(opt.Only); opt.OnlyGiven && err != nil {
		fmt.Fprintf(stderr, "orch: %v\n", err)
		return 1
	}
	return NewRunner(o).Run()
}

func cliEmit(args []string, stderr io.Writer) int {
	once := false
	var repo string
	for _, a := range args {
		if a == "--once" {
			once = true
		} else if repo == "" {
			repo = a
		}
	}
	repo = ResolveRepo(repo)
	if repo == "" {
		fmt.Fprintln(stderr, "usage: devbrain nightshift emit [--once] <repo>")
		return 2
	}
	e := status.NewEmitter(repo)
	if once {
		retire, err := e.Emit()
		if err != nil {
			return 1
		}
		if retire {
			return 3 // frozen contract: "fleet stopped >10 min"
		}
		return 0
	}
	pidfile := filepath.Join(repo, ".nightshift", ".emit.pid")
	defer os.Remove(pidfile)
	for {
		retire, _ := e.Emit()
		if retire {
			return 0 // retire quietly; pidfile removed by the defer
		}
		time.Sleep(2 * time.Second)
	}
}

func cliWatch(args []string, stdout, stderr io.Writer) int {
	repoArg, _ := splitRepoArg(args)
	if repoArg == "" && len(args) > 0 {
		repoArg = args[0]
	}
	repo := ResolveRepo(repoArg)
	if repo == "" {
		fmt.Fprintln(stderr, "nightshift: no repo — run 'devbrain nightshift start <path>' first")
		return 1
	}
	SaveRepo(repo)
	nsDir := filepath.Join(repo, ".nightshift")
	os.MkdirAll(nsDir, 0o755)
	// keep status.json fresh via a detached emit loop (idempotent)
	pidfile := filepath.Join(nsDir, ".emit.pid")
	if pid, ok := procutil.ReadPidfile(pidfile); !ok || !procutil.Alive(pid) {
		if self, err := os.Executable(); err == nil {
			cmd := exec.Command(self, "nightshift", "emit", repo)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if cmd.Start() == nil {
				os.WriteFile(pidfile, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644)
				go cmd.Wait()
			}
		}
	}
	status.NewEmitter(repo).Emit() // seed once
	qport := 8799
	if p, err := strconv.Atoi(os.Getenv("DEVBRAIN_QUEUE_PORT")); err == nil && p > 0 {
		qport = p
	}
	RegisterRun(repo, qport) // makes the 🌙 toggle appear
	reapForeignQueue(qport, stdout)
	if !queueAnswers(qport) { // launch queue if needed
		if self, err := os.Executable(); err == nil {
			cmd := exec.Command(self, "queue", "--no-open", "--port", strconv.Itoa(qport))
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if cmd.Start() == nil {
				go cmd.Wait()
			}
			time.Sleep(1 * time.Second)
		}
	}
	key := filepath.Base(DataProjectDir(repo))
	url := fmt.Sprintf("http://127.0.0.1:%d/?project=%s", qport, key)
	if os.Getenv("NIGHTSHIFT_NO_OPEN") == "1" {
		// registered without popping a tab (dashboard-triggered launches)
	} else if _, err := exec.LookPath("open"); err == nil {
		exec.Command("open", url).Start()
	} else {
		fmt.Fprintf(stdout, "open: %s\n", url)
	}
	fmt.Fprintf(stdout, "🌙 dashboard → %s  (queue + nightshift monitor — toggle 🌙 Nightshift)\n", url)
	return 0
}

func queueAnswers(port int) bool {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/todos", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// reapForeignQueue kills a devbrain queue squatting our port with a DIFFERENT
// data dir (a stale server from another session serves a dead dashboard).
// Only reaps on a positively-identified mismatch.
func reapForeignQueue(port int, stdout io.Writer) {
	if !queueAnswers(port) {
		return
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/whoami", port))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var who struct {
		Server string `json:"server"`
		Data   string `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&who) != nil || who.Server != "devbrain-queue" || who.Data == "" {
		return
	}
	theirs, err := filepath.EvalSymlinks(who.Data)
	if err != nil {
		theirs = who.Data
	}
	dataDir := os.Getenv("DEVBRAIN_DATA")
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, "devbrain-data")
	}
	mine, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		mine = dataDir
	}
	if theirs == mine {
		return
	}
	fmt.Fprintf(stdout, "🌙 a foreign devbrain queue (data=%s) is squatting port %d — reaping it\n", theirs, port)
	out, _ := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port)).Output()
	for _, p := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(p); err == nil {
			syscall.Kill(pid, syscall.SIGTERM)
		}
	}
	time.Sleep(1 * time.Second)
}

func cliStatus(args []string, stdout, stderr io.Writer) int {
	repoArg, _ := splitRepoArg(args)
	if repoArg == "" && len(args) > 0 {
		repoArg = args[0]
	}
	repo := ResolveRepo(repoArg)
	if repo == "" {
		fmt.Fprintln(stderr, "no repo set")
		return 1
	}
	status.NewEmitter(repo).Emit()
	b, err := os.ReadFile(filepath.Join(repo, ".nightshift", "status.json"))
	if err != nil {
		fmt.Fprintln(stdout, "no status yet")
		return 0
	}
	var d status.Doc
	if json.Unmarshal(b, &d) != nil {
		fmt.Fprintln(stdout, "no status yet")
		return 0
	}
	state := "STOPPED"
	if d.Running {
		state = "running"
	}
	fmt.Fprintf(stdout, "🌙 %s  ·  %s\n", d.Project, state)
	fmt.Fprintf(stdout, "   queue: %d open · %d merged · %d in review\n", d.Queue.Open, d.Queue.Done, d.Queue.Review)
	for _, w := range d.Workers {
		fmt.Fprintf(stdout, "   w%d: %-7s %s\n", w.I, w.State, w.Task)
	}
	if len(d.Parked) > 0 {
		ids := make([]string, len(d.Parked))
		for i, p := range d.Parked {
			ids[i] = p.ID
		}
		fmt.Fprintf(stdout, "   ⚠ %d HELD — need you: %s  (nightshift review)\n", len(d.Parked), strings.Join(ids, ", "))
	}
	return 0
}

// repoTodo runs one devbrain-todo verb from the repo (identity = its remote).
func repoTodo(repo string, args []string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err == nil {
		defer os.Chdir(cwd)
	}
	if err := os.Chdir(repo); err != nil {
		fmt.Fprintf(stderr, "nightshift: %v\n", err)
		return 1
	}
	return todo.Run(args, stdout, stderr, strings.NewReader(""))
}

func cliReview(stdout, stderr io.Writer) int {
	repo := ResolveRepo("")
	if repo == "" {
		fmt.Fprintln(stderr, "no repo set")
		return 1
	}
	fmt.Fprintln(stdout, "== held — need you (provision the dep + release, or drop) ==")
	var list strings.Builder
	repoTodo(repo, []string{"list", "held"}, &list, io.Discard)
	for _, id := range listIDsLoose(list.String()) {
		var show strings.Builder
		repoTodo(repo, []string{"show", id}, &show, io.Discard)
		reason := "(no reason recorded)"
		for _, l := range strings.Split(show.String(), "\n") {
			if strings.HasPrefix(l, "reason:") {
				if v := strings.TrimSpace(l[7:]); v != "" {
					reason = v
				}
				break
			}
		}
		fmt.Fprintf(stdout, "  %s\n      %s\n", id, reason)
	}
	fmt.Fprintln(stdout, "  → nightshift release <id>   (after you provide the dep)")
	fmt.Fprintln(stdout, "  → nightshift drop <id>      (discard / won't-do)")
	return 0
}

func cliTaskVerb(verb string, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintf(stderr, "nightshift %s: needs a task id\n", verb)
		return 1
	}
	repo := ResolveRepo("")
	if repo == "" {
		fmt.Fprintln(stderr, "no repo set")
		return 1
	}
	switch verb {
	case "release":
		return repoTodo(repo, []string{"release", args[0]}, stdout, stderr)
	case "drop":
		rc := repoTodo(repo, []string{"done", args[0]}, stdout, stderr)
		if rc == 0 {
			fmt.Fprintf(stdout, "dropped %s (won't-do)\n", args[0])
		}
		return rc
	default: // approve | go
		rc := repoTodo(repo, []string{"approve", args[0]}, stdout, stderr)
		if rc == 0 {
			fmt.Fprintf(stdout, "🟢 greenlit %s — the fleet will pick it up and run it unattended (downloads/installs allowed)\n", args[0])
		}
		return rc
	}
}

func cliTmuxVerb(verb string, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintf(stderr, "nightshift %s: needs a worker index\n", verb)
		return 1
	}
	sess := "ns-w" + args[0]
	if exec.Command("tmux", "has-session", "-t", sess).Run() != nil {
		if verb == "say" {
			fmt.Fprintf(stderr, "no tmux session %s — say/attach work only with the --tmux backend\n", sess)
		} else {
			fmt.Fprintf(stderr, "no tmux session %s — attach works only with the --tmux backend (default is headless claude -p)\n", sess)
		}
		return 1
	}
	if verb == "say" {
		exec.Command("tmux", "send-keys", "-t", sess, "-l", strings.Join(args[1:], " ")).Run()
		exec.Command("tmux", "send-keys", "-t", sess, "Enter").Run()
		fmt.Fprintf(stdout, "→ w%s\n", args[0])
		return 0
	}
	// attach replaces the process, like `exec tmux attach`
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		fmt.Fprintln(stderr, "tmux not found")
		return 1
	}
	syscall.Exec(tmuxPath, []string{"tmux", "attach", "-t", sess}, os.Environ())
	return 1 // only reached if exec failed
}

func cliStop(args []string, stdout, stderr io.Writer) int {
	repoArg, _ := splitRepoArg(args)
	if repoArg == "" && len(args) > 0 {
		repoArg = args[0]
	}
	repo := ResolveRepo(repoArg)
	stopped := false
	if repo != "" {
		if pid, ok := procutil.ReadPidfile(filepath.Join(repo, ".nightshift", "orchestrator.pid")); ok && procutil.Alive(pid) {
			syscall.Kill(pid, syscall.SIGTERM)
			for i := 0; i < 50 && procutil.Alive(pid); i++ {
				time.Sleep(100 * time.Millisecond) // let its cleanup release tasks
			}
			// A wedged orchestrator can ignore SIGTERM (blocked in the loop); after
			// the grace window, SIGKILL so `stop` always kills it rather than lying.
			if procutil.Alive(pid) {
				syscall.Kill(pid, syscall.SIGKILL)
				fmt.Fprintln(stderr, "orchestrator ignored SIGTERM — escalated to SIGKILL")
			}
			stopped = true
		}
	}
	// legacy bash orchestrator, and any Go run started without a saved repo
	pat := "nightshift-orchestrate.sh"
	if repo != "" {
		pat += " --repo " + repo
	}
	if exec.Command("pkill", "-f", pat).Run() == nil {
		stopped = true
	}
	if stopped {
		fmt.Fprintln(stdout, "stopped orchestrator")
	} else {
		fmt.Fprintln(stdout, "(orchestrator not running)")
	}
	// tmux workers
	out, _ := exec.Command("tmux", "ls").Output()
	for _, l := range strings.Split(string(out), "\n") {
		if name, _, ok := strings.Cut(l, ":"); ok && strings.Contains(name, "ns-w") {
			exec.Command("tmux", "kill-session", "-t", name).Run()
		}
	}
	if repo != "" {
		// Backstop for a HARD kill that skipped the orchestrator's own cleanup:
		// reap in-flight headless turns via the per-worktree turn.pid files.
		matches, _ := filepath.Glob(repo + "-w*/.nightshift/turn.pid")
		for _, f := range matches {
			if pid, ok := procutil.ReadPidfile(f); ok {
				procutil.KillGroup(pid, syscall.SIGTERM)
			}
			os.Remove(f)
		}
		for _, p := range []string{"emit", "http"} {
			f := filepath.Join(repo, ".nightshift", "."+p+".pid")
			if pid, ok := procutil.ReadPidfile(f); ok {
				syscall.Kill(pid, syscall.SIGTERM)
			}
			os.Remove(f)
		}
		UnregisterRun(repo)
	}
	fmt.Fprintln(stdout, "🌙 nightshift stopped.")
	return 0
}
