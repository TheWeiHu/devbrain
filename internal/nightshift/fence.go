package nightshift

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/nightshift/plan"
	"github.com/TheWeiHu/devbrain/internal/task"
)

// fence.go — fixed-set (--only) machinery: normalization/validation of the
// selection, the fail-CLOSED fence that parks out-of-set tasks, the landing
// ledger (landed.tsv), and the wind-down/verify logic.
//
// DEVBRAIN_TODO_ONLY only works if the installed todo honors it AND the env
// propagates to every worker — a stale install or a dropped env silently
// FAILS OPEN (drains the whole queue). The fence removes that dependency: at
// boot we HOLD every open task not in the set, so `next` (any todo version,
// no env needed) can only ever hand out the chosen subset. Released on exit.

// The hold reason doubles as the recovery MARKER (prefix-matched), so unfence
// never depends on a file or the clone surviving — the marker lives on the
// task in the persistent queue. The reason also carries THIS run's checkout
// path (`by <repo>`) so a concurrent fleet on a DIFFERENT checkout of the same
// project only releases its own holds, not the other run's. The marker/format
// lives in internal/task so the todo CLI (which parks tasks added mid-run)
// builds byte-identical reasons this Unfence recognizes.
const FenceMark = task.FenceMark

// fenceNote builds this run's park reason, tagged with the repo checkout path.
func (o *Orch) fenceNote() string { return task.FenceNote(o.Opt.Repo) }

// fenceRepo extracts the checkout path a fence reason was tagged with, or ""
// for an untagged (legacy / self-heal) marker that any run may release.
func fenceRepo(reason string) string { return task.FenceRepo(reason) }

// ParseOnly validates a present --only against the live queue: normalizes,
// FATALs on an empty fence (reads as "only these" but means "everything,
// forever"), resolves every token, warns on unknowns, and FATALs when NONE
// exist. On success it arms fixed-set mode on o.Opt (normalized Only,
// FixedSet, Forever=false, workers capped to the task count) and echoes the
// resolved fence. Returns an error for the two FATAL conditions.
func (o *Orch) ParseOnly(raw string) error {
	toks := plan.NormalizeOnly(raw)
	o.Opt.Only = strings.Join(toks, ",")
	if len(toks) == 0 {
		fmt.Fprintln(o.Err(), "orch: FATAL — --only given but resolved to 0 task ids — refusing to start an unfenced run.")
		fmt.Fprintln(o.Err(), "orch:   (an empty fence reads as 'only these' but would run the whole queue forever.)")
		return fmt.Errorf("empty --only")
	}
	// Validate every token against the live queue; warn on unknowns, FATAL if NONE exist.
	out, _ := o.todoAll("list", "all")
	var ids []string
	for _, row := range plan.ListStatusIDs(out) {
		ids = append(ids, row[1])
	}
	resolved, unknown := plan.ResolveOnly(toks, ids)
	if len(unknown) > 0 {
		fmt.Fprintf(o.Out, "orch: ⚠ --only: no such task(s) in the queue: %s (ignored)\n", strings.Join(unknown, " "))
	}
	if len(resolved) == 0 {
		fmt.Fprintf(o.Err(), "orch: FATAL — --only resolved to 0 EXISTING task ids: %s — refusing to start an unfenced run.\n", strings.Join(unknown, " "))
		return fmt.Errorf("no existing --only ids")
	}
	// Echo the resolved fence so an empty/wrong selection is visible immediately.
	fmt.Fprintf(o.Out, "orch: ✅ fixed set: %s\n", strings.Join(resolved, " "))
	o.Opt.FixedSet = true
	o.Opt.Forever = false
	// Never spin up more workers than there are tasks.
	if n := len(toks); n > 0 && o.Opt.Workers > n {
		fmt.Fprintf(o.Out, "orch: capping workers %d → %d (only %d task(s) selected)\n", o.Opt.Workers, n, n)
		o.Opt.Workers = n
	}
	fmt.Fprintf(o.Out, "orch: 🌙 fixed-set mode — draining only: %s (no planning turns, %d worker(s))\n", o.Opt.Only, o.Opt.Workers)
	return nil
}

// Fence parks every OPEN task not in the set so `next` can only return the
// chosen subset.
func (o *Orch) Fence() {
	n := 0
	out, _ := o.todoAll("list")
	for _, id := range plan.ListIDs(out) {
		if plan.InOnly(o.Opt.Only, id) {
			continue
		}
		// Never park a DONE task: under derive-git a done task whose merge lived
		// in a since-reset nightshift branch reads as "open" in `list`, but
		// parking it (then unfencing via `release`) wipes its done_at and
		// corrupts the queue. done_at is the reliable raw done signal.
		if show, err := o.todo("show", id); err == nil {
			if v := taskField(show, "done_at"); v != "" && v[0] >= '0' && v[0] <= '9' {
				continue
			}
		}
		if _, err := o.todo("hold", id, o.fenceNote()); err == nil {
			n++
		}
	}
	fmt.Fprintf(o.Out, "orch: fixed-set fence — parked %d out-of-set task(s); the fleet can only see your chosen subset\n", n)
}

// WriteOnlySet persists this run's fixed-set to .nightshift/only.txt (or clears
// a stale one for a full-drain run) so the standalone status emitter scopes its
// OPEN/DONE/REVIEW counts to the launched subset. Without it a --only run's card
// shows the whole project backlog — and reverts to it the moment Unfence runs on
// stop, since the fence is what otherwise hid out-of-set tasks from the count.
func (o *Orch) WriteOnlySet() {
	f := o.Opt.OnlyFile()
	if !o.Opt.FixedSet {
		os.Remove(f)
		return
	}
	os.WriteFile(f, []byte(o.Opt.Only+"\n"), 0o644)
}

// Unfence releases the tasks THIS run parked — identified by the hold MARKER,
// so it self-heals after an unclean shutdown of THIS checkout. Only reasons
// starting with FenceMark are touched (human holds safe); of those, a reason
// tagged with a DIFFERENT checkout path is left alone so a concurrent fleet on
// another checkout of the same project keeps its own holds — the cost is that
// holds tagged by a deleted clone need a manual `todo release`. Untagged legacy
// marks match any run.
func (o *Orch) Unfence() {
	out, _ := o.todoAll("list", "held")
	for _, row := range plan.ListStatusIDs(out) {
		id := row[1]
		show, err := o.todo("show", id)
		if err != nil {
			continue
		}
		reason := taskField(show, "reason")
		if !strings.HasPrefix(reason, FenceMark) {
			continue
		}
		if r := fenceRepo(reason); r != "" && !task.SameCheckout(r, o.Opt.Repo) {
			continue
		}
		o.todo("release", id)
	}
}

// Unresolved counts SELECTED tasks not yet terminal (open|taken|review) —
// drives the fixed-set wind-down. Scoped to the chosen set so an unrelated
// `review` task can't keep the fleet alive.
func (o *Orch) Unresolved() int {
	out, _ := o.todoAll("list", "all")
	rows := plan.ListStatusIDs(out)
	n := 0
	for _, tok := range strings.Split(o.Opt.Only, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		st, _ := plan.MatchRow(rows, tok)
		switch st {
		case "open", "taken", "review":
			n++
		}
	}
	return n
}

// ── completion post-condition: landed.tsv ─────────────────────────────────────
// The queue's `done` is decoupled from "the work is on the branch": a base
// reset can leave tasks `done` while wiping their commits. Record the SHA each
// task landed at and, at completion, assert it's still an ancestor of
// origin/nightshift — a reset drops those SHAs, surfacing the loss loudly.

// RecordLanded stamps the current origin/nightshift SHA as id's landing point.
func (o *Orch) RecordLanded(id string) {
	sha := o.Base.RevParse("origin/nightshift")
	if sha == "" {
		return
	}
	f, err := os.OpenFile(o.Opt.LandedFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\n", id, sha)
}

// LandedSHA returns id's LATEST recorded landing SHA (last wins), or "".
func (o *Orch) LandedSHA(id string) string {
	b, err := os.ReadFile(o.Opt.LandedFile())
	if err != nil {
		return ""
	}
	sha := ""
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && parts[0] == id {
			sha = parts[1]
		}
	}
	return sha
}

// Verify is fixedset_verify: every selected `done` task's work must still be
// present on origin/nightshift. Returns the absent ids (nil = verified).
func (o *Orch) Verify() (missing []string, ok bool) {
	o.Base.Fetch()
	out, _ := o.todoAll("list", "all")
	rows := plan.ListStatusIDs(out)
	doneN, present := 0, 0
	for _, tok := range strings.Split(o.Opt.Only, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		st, id := plan.MatchRow(rows, tok)
		if st != "done" || id == "" {
			continue
		}
		doneN++
		sha := o.LandedSHA(id)
		if sha != "" && o.Base.IsAncestor(sha, "origin/nightshift") {
			present++
		} else {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(o.Out, "orch: ⚠ INCOMPLETE: %d/%d done task(s) present on nightshift; absent: %s\n", present, doneN, strings.Join(missing, " "))
		return missing, false
	}
	fmt.Fprintf(o.Out, "orch: ✅ verified — all %d done task(s) present on nightshift\n", doneN)
	return nil, true
}

// ReopenAbsent implements the wind-down's reopen-once step: each verified-
// absent done task is reopened (plain reopen — the work is GONE, so the
// worker must rebuild) at most once per run, its retry counter cleared.
// `already` tracks the ids reopened earlier (the FS_REOPENED guard); the
// return lists the ids reopened THIS call.
func (o *Orch) ReopenAbsent(missing []string, already map[string]bool) []string {
	var again []string
	for _, id := range missing {
		if already[id] {
			continue // already regenerated once and still missing — don't loop forever
		}
		if _, err := o.todo("reopen", id); err == nil {
			os.Remove(filepath.Join(o.Opt.RetryDir(), id))
			already[id] = true
			again = append(again, id)
		}
	}
	return again
}
