package nightshift

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// task in the persistent queue.
const (
	FenceMark = "fixed-set: parked"
	FenceNote = FenceMark + " while nightshift runs your selected tasks — auto-released when it finishes"
)

var (
	// ids come from the FIRST column of `list` (the id field), not the title,
	// so a task whose title happens to contain an NNNN-word pattern can't be
	// mistaken for a task id.
	listIDRe       = regexp.MustCompile(`^[ \t]*\[[^\]]*\][ \t]+([0-9]{4}-[a-z0-9-]+)`)
	listStatusIDRe = regexp.MustCompile(`^[ \t]*\[[^\]]*\][ \t]+([a-z]+)[ \t]+([0-9]{4}-[a-z0-9-]+)`)
	wsRe           = regexp.MustCompile(`[ \t\r\n\v\f]`)
)

// listIDs extracts task ids from `todo list` (open) output.
func listIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if m := listIDRe.FindStringSubmatch(line); m != nil {
			ids = append(ids, m[1])
		}
	}
	return ids
}

// listStatusIDs extracts (status, id) pairs from `todo list <status>|all`.
func listStatusIDs(out string) [][2]string {
	var rows [][2]string
	for _, line := range strings.Split(out, "\n") {
		if m := listStatusIDRe.FindStringSubmatch(line); m != nil {
			rows = append(rows, [2]string{m[1], m[2]})
		}
	}
	return rows
}

// NormalizeOnly ports the --only normalization: split on commas, strip ALL
// whitespace per token, drop empty tokens.
func NormalizeOnly(raw string) []string {
	var toks []string
	for _, t := range strings.Split(raw, ",") {
		t = wsRe.ReplaceAllString(t, "")
		if t != "" {
			toks = append(toks, t)
		}
	}
	return toks
}

// ResolveOnly matches each token (full slug or bare 4-digit number) against
// the live queue ids; first match wins. Returns canonical ids and the
// unmatched tokens.
func ResolveOnly(tokens, ids []string) (resolved, unknown []string) {
	for _, tok := range tokens {
		num := tok
		if i := strings.Index(tok, "-"); i >= 0 {
			num = tok[:i]
		}
		match := ""
		for _, id := range ids {
			if id == tok || (len(id) >= 4 && id[:4] == num) {
				match = id
				break
			}
		}
		if match != "" {
			resolved = append(resolved, match)
		} else {
			unknown = append(unknown, tok)
		}
	}
	return resolved, unknown
}

// InOnly ports in_only: id (full slug or bare 4-digit) is in the --only set
// when a token matches the full slug, the bare number, or shares the leading
// 4-digit number from either side.
func InOnly(only, id string) bool {
	num := id
	if i := strings.Index(id, "-"); i >= 0 {
		num = id[:i]
	}
	for _, tok := range strings.Split(only, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		tokNum := tok
		if i := strings.Index(tok, "-"); i >= 0 {
			tokNum = tok[:i]
		}
		if tok == id || tok == num || tokNum == num {
			return true
		}
	}
	return false
}

// ParseOnly validates a present --only against the live queue: normalizes,
// FATALs on an empty fence (reads as "only these" but means "everything,
// forever"), resolves every token, warns on unknowns, and FATALs when NONE
// exist. On success it arms fixed-set mode on o.Opt (normalized Only,
// FixedSet, Forever=false, workers capped to the task count) and echoes the
// resolved fence. Returns an error for the two FATAL conditions.
func (o *Orch) ParseOnly(raw string) error {
	toks := NormalizeOnly(raw)
	o.Opt.Only = strings.Join(toks, ",")
	if len(toks) == 0 {
		fmt.Fprintln(o.Err(), "orch: FATAL — --only given but resolved to 0 task ids — refusing to start an unfenced run.")
		fmt.Fprintln(o.Err(), "orch:   (an empty fence reads as 'only these' but would run the whole queue forever.)")
		return fmt.Errorf("empty --only")
	}
	// Validate every token against the live queue; warn on unknowns, FATAL if NONE exist.
	out, _ := o.todoAll("list", "all")
	var ids []string
	for _, row := range listStatusIDs(out) {
		ids = append(ids, row[1])
	}
	resolved, unknown := ResolveOnly(toks, ids)
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
	for _, id := range listIDs(out) {
		if InOnly(o.Opt.Only, id) {
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
		if _, err := o.todo("hold", id, FenceNote); err == nil {
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

// Unfence releases every task parked by ANY fixed-set run — identified by the
// hold MARKER, so it self-heals after an unclean shutdown or a removed clone.
// Only tasks whose reason starts with FenceMark are touched (human holds safe).
func (o *Orch) Unfence() {
	out, _ := o.todoAll("list", "held")
	for _, row := range listStatusIDs(out) {
		id := row[1]
		show, err := o.todo("show", id)
		if err != nil {
			continue
		}
		if strings.HasPrefix(taskField(show, "reason"), FenceMark) {
			o.todo("release", id)
		}
	}
}

// Unresolved counts SELECTED tasks not yet terminal (open|taken|review) —
// drives the fixed-set wind-down. Scoped to the chosen set so an unrelated
// `review` task can't keep the fleet alive.
func (o *Orch) Unresolved() int {
	out, _ := o.todoAll("list", "all")
	rows := listStatusIDs(out)
	n := 0
	for _, tok := range strings.Split(o.Opt.Only, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		st, _ := matchRow(rows, tok)
		switch st {
		case "open", "taken", "review":
			n++
		}
	}
	return n
}

// matchRow finds the first (status, id) row whose id equals the token or
// shares its leading 4-digit number (the awk `$2==t || substr($2,1,4)==num`).
func matchRow(rows [][2]string, tok string) (status, id string) {
	num := tok
	if i := strings.Index(tok, "-"); i >= 0 {
		num = tok[:i]
	}
	for _, r := range rows {
		if r[1] == tok || (len(r[1]) >= 4 && r[1][:4] == num) {
			return r[0], r[1]
		}
	}
	return "", ""
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
	rows := listStatusIDs(out)
	doneN, present := 0, 0
	for _, tok := range strings.Split(o.Opt.Only, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		st, id := matchRow(rows, tok)
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
