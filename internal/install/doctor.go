package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/importer"
	"github.com/TheWeiHu/devbrain/internal/jsonedit"
	"time"

	"github.com/TheWeiHu/devbrain/internal/sweep"
)

// Doctor audits the capture wiring the way it actually runs: are the hook
// commands in ~/.claude/settings.json pointed at a binary that still exists and
// runs, and does the resolved data dir look sane? A stale ABSOLUTE path — the
// binary moved or was replaced after an upgrade — is the usual reason capture
// silently stops (the harness never surfaces a hook-exec failure). Report-only
// (never writes) unless --fix, which re-points the registered hooks at the
// current binary.
func Doctor(args []string, stdout, stderr io.Writer) int {
	fix, noBackfill := false, false
	for _, a := range args {
		switch a {
		case "--fix":
			fix = true
		case "--no-backfill":
			noBackfill = true
		case "-h", "--help":
			fmt.Fprintln(stdout, "devbrain doctor [--fix] [--no-backfill]\n  audit the capture hook wiring; --fix re-points hooks at the current binary\n  and backfills the down days from existing history (--no-backfill to skip)")
			return 0
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "doctor: %v\n", err)
		return 1
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	bin := BinaryPath()

	fmt.Fprintf(stdout, "devbrain doctor — capture wiring (%s)\n\n", display(settings, home))
	fmt.Fprintf(stdout, "  current binary: %s\n", bin)
	if !pathResolvable(bin) {
		fmt.Fprintln(stdout, "    ⚠ not found via PATH — a fixed path here breaks silently if the binary moves; brew gives a stable one")
	}
	fmt.Fprintln(stdout)

	rows, all, missing, perr := readOurHooks(settings)
	// A file we can't read or parse is its own failure — never claim a repair
	// (or a clean bill) we didn't actually make.
	if missing {
		fmt.Fprintf(stdout, "  ✗ no %s — capture is not wired. Run 'devbrain install'.\n", display(settings, home))
		return 1
	}
	if perr != nil {
		fmt.Fprintf(stdout, "  ✗ %s is not valid JSON (%v) — fix it by hand or re-run 'devbrain install'.\n", display(settings, home), perr)
		return 1
	}

	if fix {
		if len(all) == 0 {
			fmt.Fprintln(stdout, "  ✗ no devbrain hooks registered — nothing to re-point. Run 'devbrain install'.")
			return 1
		}
		backup(settings) // multi-step edit below; keep a restore point
		if _, err := repairHooks(settings, bin, rows, all); err != nil {
			fmt.Fprintf(stderr, "doctor: repair failed: %v\n", err)
			return 1
		}
		// Re-read so the table below reflects the repaired state honestly.
		rows, all, missing, perr = readOurHooks(settings)
		if missing || perr != nil {
			fmt.Fprintf(stderr, "doctor: settings unreadable after repair\n")
			return 1
		}
	}

	problems := auditTable(stdout, home, bin, rows)
	fmt.Fprintln(stdout)

	if problems == 0 {
		if fix {
			fmt.Fprintf(stdout, "✓ re-pointed hooks at %s — capture restored\n", bin)
			// The down days aren't lost — Claude Code keeps its own transcripts.
			// Run the same backfill first-install uses; it's gap-safe (skips
			// already-captured sessions, idempotent), so it only refills the hole.
			backfill(stdout, stderr, noBackfill)
		} else {
			fmt.Fprintln(stdout, "✓ capture wiring healthy")
		}
		return 0
	}
	if fix {
		// e.g. a data dir that isn't a directory — repairHooks can't fix that.
		fmt.Fprintf(stdout, "✗ %d problem(s) remain after --fix (see above)\n", problems)
		return 1
	}
	fmt.Fprintf(stdout, "✗ %d problem(s) — run 'devbrain doctor --fix' to re-point hooks at the current binary\n", problems)
	return 1
}

// backfill refills the days capture was down from ~/.claude transcripts, using
// the same gap-safe importer first-install uses (skips already-captured
// sessions, idempotent). A backfill hiccup is non-fatal — the hooks are already
// repaired, so capture is restored regardless.
func backfill(stdout, stderr io.Writer, skip bool) {
	if skip {
		fmt.Fprintln(stdout, "  ↳ skipped backfill (--no-backfill); run later:  devbrain import --apply")
		return
	}
	// Same full harvest first-install runs — Claude + Codex history, memory, and
	// token sidecars — so a recovered install is as whole as a fresh one.
	fmt.Fprintln(stdout, "\n  backfilling the days capture was down from existing history (devbrain import --apply)…")
	if rc := importer.Run([]string{"--apply"}, stdout, stderr); rc != 0 {
		fmt.Fprintln(stdout, "  ↳ backfill had an issue — run it manually:  devbrain import --apply")
	}
}

// auditTable prints one row per registered hook plus the data-dir row and
// returns the problem count. Read-only: it Stats paths, never writes.
func auditTable(w io.Writer, home, bin string, rows []hookRow) int {
	problems := 0
	if len(rows) == 0 {
		fmt.Fprintln(w, "  ✗ no devbrain hooks registered — run 'devbrain install'.")
		problems++
	}

	// Capture is sweep-based: a problem only when transcripts are WAITING and
	// the sweep isn't keeping up (never ran, or stale by more than an hour).
	switch last, pending := sweep.Status(); {
	case pending && last.IsZero():
		fmt.Fprintf(w, "  %-16s %-10s FAIL   → transcripts on disk but the sweep never ran — is the flusher installed? (devbrain install, or run 'devbrain sweep')\n", "capture sweep", "")
		problems++
	case pending && time.Since(last) > time.Hour:
		fmt.Fprintf(w, "  %-16s %-10s STALE  → last sweep %s ago with transcripts waiting — is the flusher running?\n", "capture sweep", "", time.Since(last).Round(time.Minute))
		problems++
	case last.IsZero():
		fmt.Fprintf(w, "  %-16s %-10s PASS (nothing to sweep yet)\n", "capture sweep", "")
	default:
		fmt.Fprintf(w, "  %-16s %-10s PASS (last sweep %s)\n", "capture sweep", "", last.Format("2006-01-02 15:04"))
	}
	for _, r := range rows {
		switch {
		case !fileRuns(r.prog):
			fmt.Fprintf(w, "  %-16s %-10s STALE  → %s (missing/not executable)\n", r.event, r.name, r.prog)
			problems++
		case r.prog != bin:
			fmt.Fprintf(w, "  %-16s %-10s DRIFT  → %s (a different binary than the current one)\n", r.event, r.name, r.prog)
			problems++
		default:
			fmt.Fprintf(w, "  %-16s %-10s PASS\n", r.event, r.name)
		}
	}

	// The hooks MkdirAll the data dir at capture time, so a missing dir is fine;
	// only an existing non-directory is a real problem. Stat only — no writes.
	data := config.DataDir()
	switch fi, err := os.Stat(data); {
	case err == nil && fi.IsDir():
		fmt.Fprintf(w, "  %-16s %-10s PASS\n", "data dir", display(data, home))
	case os.IsNotExist(err):
		fmt.Fprintf(w, "  %-16s %-10s PASS (created at first capture)\n", "data dir", display(data, home))
	case err == nil:
		fmt.Fprintf(w, "  %-16s %-10s FAIL   → %s (not a directory)\n", "data dir", "", display(data, home))
		problems++
	default:
		fmt.Fprintf(w, "  %-16s %-10s FAIL   → %s (%v)\n", "data dir", "", display(data, home), err)
		problems++
	}
	return problems
}

// hookRow is one registered devbrain hook: which event fires it, the hook name,
// and the binary the command invokes.
type hookRow struct{ event, name, prog string }

// readOurHooks parses settings.json (via the same jsonedit reader the installer
// uses) and returns one row per registered devbrain hook, the deduped list of
// all matching command strings (for stripping), whether the file is missing,
// and any parse error. Only commands isOurHookCommand accepts are returned, so a
// third-party `<other> hook capture` is never touched.
func readOurHooks(path string) (rows []hookRow, all []string, missing bool, parseErr error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, true, nil
		}
		return nil, nil, false, err
	}
	obj, err := jsonedit.Parse(b)
	if err != nil {
		return nil, nil, false, err
	}
	hooks := obj.Get("hooks")
	if hooks == nil || hooks.Kind != jsonedit.Object {
		return nil, nil, false, nil
	}
	seen := map[string]bool{}
	for _, m := range hooks.Obj {
		event := m.Key
		if m.Val.Kind != jsonedit.Array {
			continue
		}
		for _, entry := range m.Val.Arr {
			inner := entry.Get("hooks")
			if inner == nil || inner.Kind != jsonedit.Array {
				continue
			}
			for _, h := range inner.Arr {
				cv := h.Get("command")
				if cv == nil || cv.Kind != jsonedit.String || !isOurHookCommand(cv.Str) {
					continue
				}
				rows = append(rows, hookRow{event: event, name: hookNameOf(cv.Str), prog: hookProgOf(cv.Str)})
				if !seen[cv.Str] {
					seen[cv.Str] = true
					all = append(all, cv.Str)
				}
			}
		}
	}
	return rows, all, false, nil
}

// repairHooks strips every registered devbrain hook (all stale duplicates) and
// re-adds each distinct (event, hook) at the current binary, preserving exactly
// which events were wired so opted-out components are not re-enabled.
func repairHooks(settings, bin string, rows []hookRow, all []string) (int, error) {
	if len(all) > 0 {
		if err := jsonedit.UnregisterHook(settings, all); err != nil {
			return 0, err
		}
	}
	specByHook := map[string]hookSpec{}
	for _, s := range hookSpecs {
		specByHook[s.hook] = s
	}
	seen := map[string]bool{}
	n := 0
	for _, r := range rows {
		key := r.event + "\x00" + r.name
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := jsonedit.RegisterHook(settings, r.event, specByHook[r.name].matcher, bin+" hook "+r.name); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// hookProgOf / hookNameOf split a `[DEVBRAIN_HARNESS=codex ]<prog> hook <name>`
// command using the installer's own shape regex, so an env-prefixed command
// still yields the right binary path.
func hookProgOf(cmd string) string {
	if m := goHookShapeRe.FindStringSubmatch(cmd); m != nil {
		return m[2]
	}
	return ""
}

func hookNameOf(cmd string) string {
	if i := strings.Index(cmd, " hook "); i >= 0 {
		return strings.TrimSpace(cmd[i+len(" hook "):])
	}
	return ""
}

// pathResolvable reports whether `devbrain` on PATH is the same file as bin —
// i.e. the recorded path is stable (survives the binary moving within its dir).
func pathResolvable(bin string) bool {
	lp, err := exec.LookPath("devbrain")
	if err != nil {
		return false
	}
	a, e1 := os.Stat(lp)
	b, e2 := os.Stat(bin)
	return e1 == nil && e2 == nil && os.SameFile(a, b)
}

func fileRuns(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func display(p, home string) string {
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}
