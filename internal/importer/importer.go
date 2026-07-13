// Package importer is the Go port of the retired import.py — the one-time
// backfill that seeds the devbrain data repo from existing Claude Code /
// Codex caches so a fresh install has value on day one. Safe by
// construction: redacts secrets, skips sessions already captured live,
// idempotent, and DRY-RUNS by default.
//
// Routing: identity is the git remote of a still-present dir, else a
// user-declared alias for the trailing dir name, else a strict path match
// against existing projects (dead worktrees), else miscellaneous.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/projectkey"
	"github.com/TheWeiHu/devbrain/internal/redact"
	"github.com/TheWeiHu/devbrain/internal/transcript"
)

// Banner marks a backfilled (not live-captured) log file.
const Banner = "> ⚠️ BACKFILLED from ~/.claude (history.jsonl + transcripts); not captured live.\n"

var (
	sanitizeRe = regexp.MustCompile(`[^a-z0-9._-]`)
	suffixRe   = regexp.MustCompile(`-(w\d+|v\d+)$`)
	nsPathRe   = regexp.MustCompile(`/(nightshift|drain)/`)
	workerRe   = regexp.MustCompile(`-w\d+(/|$)`)
)

func sanitize(s string) string {
	return sanitizeRe.ReplaceAllString(strings.ReplaceAll(strings.ToLower(s), " ", "-"), "")
}

// gitRemoteTimeout bounds the per-directory `git remote get-url` probe. import
// walks every cwd on disk; a single git call that blocks (a stalled network
// mount, a credential prompt) would otherwise hang the whole run — the retired
// import.py guarded this with timeout=5.
var gitRemoteTimeout = 5 * time.Second

func gitRemote(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitRemoteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", path, "remote", "get-url", "origin")
	// Never let git block on an interactive credential/askpass prompt.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// WaitDelay forces the stdout pipe closed after the timeout kill, so a child
	// git may have spawned that still holds the pipe can't keep Output() blocked.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// matchKnown routes a DEAD worktree (no live remote) to an existing project
// by matching its path segments against `known` ({repo-name: key}). Strict —
// exact or `repo-` prefix only; strips nightshift/drain -w<N> + -v<N>
// suffixes; longest repo wins. Never overrides a live remote.
func matchKnown(cwd string, known map[string]string, renames map[string]string) string {
	repos := make([]string, 0, len(known))
	for r := range known {
		repos = append(repos, r)
	}
	// longest first; name tiebreak for determinism
	sort.Slice(repos, func(i, j int) bool {
		if len(repos[i]) != len(repos[j]) {
			return len(repos[i]) > len(repos[j])
		}
		return repos[i] < repos[j]
	})
	bestKey, bestLen := "", -1
	for _, segment := range strings.Split(strings.Trim(cwd, "/"), "/") {
		bare := suffixRe.ReplaceAllString(segment, "") // drop worker/variant suffix
		// a user rename maps an old dir name straight to a project key
		renamed := renames[segment]
		if renamed == "" {
			renamed = renames[bare]
		}
		if renamed != "" {
			if len(segment) > bestLen {
				bestKey, bestLen = renamed, len(segment)
			}
			continue
		}
		// otherwise match against an existing repo-name (longest wins)
		for _, repo := range repos {
			if segment == repo || bare == repo ||
				strings.HasPrefix(segment, repo+"-") || strings.HasPrefix(bare, repo+"-") {
				if len(repo) > bestLen {
					bestKey, bestLen = known[repo], len(repo)
				}
				break
			}
		}
	}
	return bestKey
}

// route returns (key, confidence): git remote first, then a user alias, then
// (for dead worktrees) a strict match against known projects, else the
// shared miscellaneous bucket.
func route(cwd string, aliases, known map[string]string) (string, string) {
	if fi, err := os.Stat(cwd); err == nil && fi.IsDir() {
		if k := projectkey.RemoteToKey(gitRemote(cwd)); k != "" {
			return k, "high"
		}
	}
	seg := filepath.Base(strings.TrimRight(cwd, "/"))
	if k, ok := aliases[seg]; ok {
		return k, "high"
	}
	if len(known) > 0 {
		if k := matchKnown(cwd, known, aliases); k != "" {
			return k, "medium"
		}
	}
	return "miscellaneous", "low"
}

// pyISO parses an ISO-ish timestamp (Z tolerated); wall-clock fields are
// kept in the parsed offset, like Python fromisoformat + strftime.
func pyISO(s string) (time.Time, error) {
	s = strings.ReplaceAll(s, "Z", "+00:00")
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
	return time.Time{}, errors.New("bad iso: " + s)
}

func isoOr(s string, fallback time.Time) time.Time {
	if t, err := pyISO(s); err == nil {
		return t
	}
	return fallback
}

var epoch0 = time.Unix(0, 0).UTC()

// turn is one parse_transcript() row.
type turn struct {
	dt, respDT                          time.Time
	cwd, prompt, summary, meta          string
	input, output, cacheCreate, cacheRd int
	model                               string
	turnKey                             string // transcript.TurnKey(c.DT); "" when the turn has no timestamp
}

// parseTranscript maps transcript.Turns onto import rows: redacted prompt,
// closing-sentence recap, touched/tools meta, token counts.
func parseTranscript(path string) []turn {
	return mapTurns(transcript.Turns(path, 0, true))
}

// parseAgentTranscript is parseTranscript for SUBAGENT files, whose events
// are all sidechain and would otherwise parse to zero turns.
func parseAgentTranscript(path string) []turn {
	return mapTurns(transcript.AgentTurns(path, 0))
}

func mapTurns(cs []transcript.Turn) []turn {
	var out []turn
	for _, c := range cs {
		var meta []string
		if c.Files != nil && len(c.Files.Keys()) > 0 {
			meta = append(meta, "touched: "+strings.Join(c.Files.Keys(), ", "))
		}
		if c.Tools != nil && c.Tools.Len() > 0 {
			parts := make([]string, 0, c.Tools.Len())
			for _, k := range c.Tools.Keys() {
				parts = append(parts, k+"×"+strconv.Itoa(c.Tools.Get(k)))
			}
			meta = append(meta, "tools: "+strings.Join(parts, ", "))
		}
		dt := isoOr(c.DT, epoch0)
		out = append(out, turn{
			dt: dt, cwd: c.CWD, prompt: redact.Redact(c.Prompt),
			turnKey: transcript.TurnKey(c.DT),
			respDT:  isoOr(c.TurnTS, dt),
			summary: redact.Redact(transcript.Recap(c.Texts)),
			meta:    redact.Redact(strings.Join(meta, "  ·  ")),
			input:   c.Input, output: c.Output,
			cacheCreate: c.CacheCreate, cacheRd: c.CacheRead, model: c.Model,
		})
	}
	return out
}

// liveSessions returns two views of the already-live logs, keyed off
// non-BACKFILLED .md files: session UUIDs with ANY live log (guards the
// history.jsonl fallback) and (uuid, day) pairs (the transcript harvest
// gates per-day, so a partially-live multi-day session still backfills its
// missing days).
func liveSessions(data string) (map[string]bool, map[[2]string]bool) {
	live, liveDays := map[string]bool{}, map[[2]string]bool{}
	files, _ := filepath.Glob(filepath.Join(data, "projects", "*", "log", "*", "*.md"))
	for _, f := range files {
		stem := strings.TrimSuffix(filepath.Base(f), ".md")
		sid := stem
		if _, after, found := strings.Cut(stem, "."); found {
			sid = after
		}
		day := filepath.Base(filepath.Dir(f))
		head, err := readHeadRunes(f, 600)
		if err != nil {
			continue
		}
		if !strings.Contains(head, "BACKFILLED") {
			live[sid] = true
			liveDays[[2]string{sid, day}] = true
		}
	}
	return live, liveDays
}

// readHeadRunes reads up to n characters (Python text-mode read(n)).
func readHeadRunes(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 4*n)
	got, rerr := f.Read(buf)
	if got == 0 && rerr != nil && rerr != io.EOF {
		return "", rerr
	}
	s := string(buf[:got])
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s, nil
}

// entry is one prompt landed in a (project, worktree, session, day) log group.
type entry struct {
	dt, respDT    time.Time
	prompt        string
	summary, meta string
}

type groupKey struct{ key, wt, sid, day, cwd string }

// tokenRow is one sidecar record; serialized like Python json.dumps with
// ", "/": " separators.
type tokenRow struct {
	ts, session, model              string
	in, out, cacheCreate, cacheRead int
	auto                            bool
	turn                            string // stable turn identity (transcript.TurnKey)
}

func (r tokenRow) json() string {
	return `{"ts": ` + pyQuote(r.ts) + `, "session": ` + pyQuote(r.session) +
		`, "model": ` + pyQuote(r.model) + `, "in": ` + strconv.Itoa(r.in) +
		`, "out": ` + strconv.Itoa(r.out) + `, "cache_create": ` + strconv.Itoa(r.cacheCreate) +
		`, "cache_read": ` + strconv.Itoa(r.cacheRead) + `, "auto": ` + pyBool(r.auto) +
		`, "turn": ` + pyQuote(r.turn) + "}"
}

func pyBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// pyQuote escapes like Python json.dumps (ensure_ascii=False default set of
// short escapes; control chars as \u00XX; non-ASCII raw).
func pyQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// stringList collects repeatable --alias flags.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

var confOrder = map[string]int{"high": 3, "medium": 2, "low": 1}

// Run is the `devbrain import` verb.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("devbrain import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home, _ := os.UserHomeDir()
	defaultData := os.Getenv("DEVBRAIN_DATA")
	if defaultData == "" {
		defaultData = filepath.Join(home, "devbrain-data")
	}
	defaultCodex := os.Getenv("CODEX_HOME")
	if defaultCodex == "" {
		defaultCodex = filepath.Join(home, ".codex")
	}
	apply := fs.Bool("apply", false, "write into the data repo (default: dry-run)")
	data := fs.String("data", defaultData, "devbrain data repo")
	claude := fs.String("claude", filepath.Join(home, ".claude"), "Claude Code home")
	codex := fs.String("codex", defaultCodex, "Codex home")
	exclude := fs.String("exclude", "", "comma-separated project keys to skip")
	var aliasFlags stringList
	fs.Var(&aliasFlags, "alias", "OLD=key rename (repeatable)")
	noMemory := fs.Bool("no-memory", false, "skip the memory/ harvest")
	tokensOnly := fs.Bool("tokens-only", false,
		"only write the token sidecars (no prompt logs / memory)")
	newerMtime := fs.Int64("newer-mtime", 0,
		"only harvest source files modified after this unix time (the sweep's incremental cursor)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "devbrain import — seed devbrain from existing Claude Code caches\n\n"+
			"usage: devbrain import [--apply] [--data D] [--claude D] [--codex D]\n"+
			"                       [--exclude a,b] [--alias OLD=key]... [--no-memory] [--tokens-only]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	excluded := map[string]bool{}
	for _, x := range strings.Split(*exclude, ",") {
		if x != "" {
			excluded[x] = true
		}
	}

	// Incremental gate for the sweep: with --newer-mtime only source files
	// written after the cursor are re-harvested; everything else on disk is
	// already in the data repo from a prior run.
	fresh := func(path string) bool {
		if *newerMtime == 0 {
			return true
		}
		st, err := os.Stat(path)
		return err == nil && st.ModTime().Unix() > *newerMtime
	}

	// Aliases for renames the git remote can't show. Persistent ones live in
	// $DATA/import-aliases (legacy hidden .import-aliases fallback); --alias wins.
	aliases := map[string]string{}
	aliasFile := filepath.Join(*data, "import-aliases")
	if _, err := os.Stat(aliasFile); err != nil {
		legacy := filepath.Join(*data, ".import-aliases")
		if _, err := os.Stat(legacy); err == nil {
			aliasFile = legacy
		}
	}
	if raw, err := os.ReadFile(aliasFile); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line, _, _ = strings.Cut(line, "#")
			line = strings.TrimSpace(line)
			if o, k, found := strings.Cut(line, "="); found {
				aliases[strings.TrimSpace(o)] = strings.TrimSpace(k)
			}
		}
	}
	for _, a := range aliasFlags {
		if o, k, found := strings.Cut(a, "="); found {
			aliases[o] = k
		}
	}

	live, liveDays := liveSessions(*data)
	existing := map[string]bool{}
	if des, err := os.ReadDir(filepath.Join(*data, "projects")); err == nil {
		for _, de := range des {
			existing[de.Name()] = true
		}
	}
	// Vocabulary for routing dead worktrees: {repo-name: <owner>__<repo>}.
	known := map[string]string{}
	for d := range existing {
		if _, after, found := strings.Cut(d, "__"); found {
			known[after] = d
		}
	}

	groups := map[groupKey][]entry{}
	var groupOrder []groupKey
	nPrompts, nResp, nMem := map[string]int{}, map[string]int{}, map[string]int{}
	confOf := map[string]string{}
	conf := func(k string) string {
		if c, ok := confOf[k]; ok {
			return c
		}
		return "low"
	}
	doneSessions := map[string]bool{}

	addEntry := func(cwd, sid string, dt time.Time, prompt string, respDT time.Time, summary, meta string) {
		key, kconf := route(cwd, aliases, known)
		wt := sanitize(filepath.Base(strings.TrimRight(cwd, "/")))
		if wt == "" {
			wt = "unknown"
		}
		ssid := sanitize(sid)
		if ssid == "" {
			ssid = "nosession"
		}
		gk := groupKey{key, wt, ssid, dt.Format("2006-01-02"), cwd}
		if _, ok := groups[gk]; !ok {
			groupOrder = append(groupOrder, gk)
		}
		groups[gk] = append(groups[gk], entry{dt, respDT, prompt, summary, meta})
		nPrompts[key]++
		if summary != "" {
			nResp[key]++
		}
		if confOrder[kconf] > confOrder[conf(key)] {
			confOf[key] = kconf
		}
	}

	// ---- harvest logs (transcripts primary, history.jsonl fallback) ----
	// The LOG harvest is gated per (session, DAY); the TOKEN harvest is NOT
	// gated (token logging is newer than most live logs, so a live prompt
	// log says nothing about whether the tokens were recorded).
	tokenRecs := map[string][]tokenRow{}
	var tokenOrder []string
	addToken := func(key string, r tokenRow) {
		if _, ok := tokenRecs[key]; !ok {
			tokenOrder = append(tokenOrder, key)
		}
		tokenRecs[key] = append(tokenRecs[key], r)
	}

	claudeTranscripts, _ := filepath.Glob(filepath.Join(*claude, "projects", "*", "*.jsonl"))
	seenSid := map[string]string{} // sid -> path; later glob entries overwrite
	var sidOrder []string
	for _, f := range claudeTranscripts {
		// A parent counts as fresh when it OR any of its subagent transcripts
		// changed: a subagent can finish writing after the parent's last write,
		// and its tokens are only discovered through the parent.
		if !fresh(f) && !anyFreshGlob(fresh, filepath.Join(strings.TrimSuffix(f, ".jsonl"), "subagents", "agent-*.jsonl")) {
			continue
		}
		sid := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		if _, ok := seenSid[sid]; !ok {
			sidOrder = append(sidOrder, sid)
		}
		seenSid[sid] = f
	}
	claudeReplace := map[string]bool{}
	for _, sid := range sidOrder {
		turns := parseTranscript(seenSid[sid])
		if len(turns) == 0 {
			continue
		}
		doneSessions[sid] = true // transcript is authoritative -> history fallback skips it
		claudeReplace[sid] = true
		for _, t := range turns {
			if !liveDays[[2]string{sid, t.dt.Format("2006-01-02")}] {
				addEntry(t.cwd, sid, t.dt, t.prompt, t.respDT, t.summary, t.meta)
			}
			if t.input != 0 || t.output != 0 || t.cacheCreate != 0 || t.cacheRd != 0 {
				key, _ := route(t.cwd, aliases, known)
				auto := nsPathRe.MatchString(t.cwd) || workerRe.MatchString(t.cwd)
				addToken(key, tokenRow{
					ts: t.respDT.Format("2006-01-02T15:04:05Z"), session: sid,
					model: t.model, in: t.input, out: t.output,
					cacheCreate: t.cacheCreate, cacheRead: t.cacheRd, auto: auto,
					turn: t.turnKey,
				})
			}
		}
		// Subagent transcripts (<dir>/<sid>/subagents/agent-*.jsonl) are
		// separate files: their usage bills to the parent session, one row
		// per token-bearing turn, keyed so live SubagentStop captures and
		// this backfill dedup against each other.
		agents, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(seenSid[sid], ".jsonl"), "subagents", "agent-*.jsonl"))
		for _, ap := range agents {
			for _, t := range parseAgentTranscript(ap) {
				if t.input == 0 && t.output == 0 && t.cacheCreate == 0 && t.cacheRd == 0 {
					continue
				}
				key, _ := route(t.cwd, aliases, known)
				auto := nsPathRe.MatchString(t.cwd) || workerRe.MatchString(t.cwd)
				addToken(key, tokenRow{
					ts: t.respDT.Format("2006-01-02T15:04:05Z"), session: sid,
					model: t.model, in: t.input, out: t.output,
					cacheCreate: t.cacheCreate, cacheRead: t.cacheRd, auto: auto,
					turn: transcript.SubagentTurnKey(ap, t.turnKey),
				})
			}
		}
	}

	// Codex stores token usage per model request; the sidecar's public shape
	// stays one row per user turn (transcript.Turns owns that aggregation).
	codexReplace := map[string]bool{}
	codexFiles, _ := filepath.Glob(filepath.Join(*codex, "sessions", "*", "*", "*", "*.jsonl"))
	for _, path := range codexFiles {
		if !fresh(path) {
			continue
		}
		sid := transcript.CodexSessionID(path)
		turns := parseTranscript(path)
		// Codex sessions were never captured live; the prompt/response/tools
		// live only in the transcript, so the log harvest mirrors Claude's.
		if len(turns) > 0 {
			doneSessions[sid] = true
		}
		for _, t := range turns {
			if !liveDays[[2]string{sid, t.dt.Format("2006-01-02")}] {
				addEntry(t.cwd, sid, t.dt, t.prompt, t.respDT, t.summary, t.meta)
			}
			if t.input == 0 && t.output == 0 && t.cacheCreate == 0 && t.cacheRd == 0 {
				continue
			}
			model := t.model
			if !(strings.HasPrefix(model, "gpt-") || strings.Contains(model, "codex")) {
				continue
			}
			key, _ := route(t.cwd, aliases, known)
			auto := nsPathRe.MatchString(t.cwd) || workerRe.MatchString(t.cwd)
			addToken(key, tokenRow{
				ts: t.respDT.Format("2006-01-02T15:04:05Z"), session: sid,
				model: model, in: t.input, out: t.output,
				cacheCreate: t.cacheCreate, cacheRead: t.cacheRd, auto: auto,
				turn: t.turnKey,
			})
			if !excluded[key] {
				codexReplace[sid] = true
			}
		}
	}

	// history.jsonl fallback: typed prompts for sessions whose transcript is
	// gone. Full runs only — on an incremental sweep (--newer-mtime) the file
	// is ALWAYS fresh (it grows with every prompt) while doneSessions only
	// names freshly re-parsed sessions, so the fallback would re-import old
	// sessions as prompt-only entries and clobber their richer
	// transcript-derived logs. New activity always has a transcript on disk.
	if raw, err := os.ReadFile(filepath.Join(*claude, "history.jsonl")); err == nil && *newerMtime == 0 {
		for _, line := range strings.Split(string(raw), "\n") {
			var r map[string]any
			dec := json.NewDecoder(strings.NewReader(line))
			dec.UseNumber()
			if dec.Decode(&r) != nil {
				continue
			}
			p, _ := r["display"].(string)
			p = strings.TrimSpace(p)
			sid, _ := r["sessionId"].(string)
			if sid == "" {
				sid = "nosession"
			}
			if p == "" || doneSessions[sid] || live[sid] || redact.IsSynthetic(p) {
				continue
			}
			tsNum, ok := r["timestamp"].(json.Number)
			if !ok {
				continue
			}
			ms, err := tsNum.Float64()
			if err != nil {
				continue
			}
			dt := time.UnixMilli(int64(ms)).UTC()
			proj, _ := r["project"].(string)
			addEntry(proj, sid, dt, redact.Redact(p), time.Time{}, "", "")
		}
	}

	// ---- harvest memory stores ----
	memory := map[string]map[string]string{} // key -> {filename: redacted}
	var memoryOrder []string
	if !*noMemory {
		memDirs, _ := filepath.Glob(filepath.Join(*claude, "projects", "*", "memory"))
		for _, md := range memDirs {
			mds, _ := filepath.Glob(filepath.Join(md, "*.md"))
			anyFresh := false
			for _, f := range mds {
				if fresh(f) {
					anyFresh = true
					break
				}
			}
			if !anyFresh {
				continue // nothing new — skip the cwd-resolution transcript reads
			}
			// the project dir's transcript names the cwd; slug guess fallback
			cwd := ""
			trs, _ := filepath.Glob(filepath.Join(filepath.Dir(md), "*.jsonl"))
			for _, tf := range trs {
				raw, err := os.ReadFile(tf)
				if err != nil {
					continue
				}
				for _, ln := range strings.Split(string(raw), "\n") {
					var e map[string]any
					if json.Unmarshal([]byte(ln), &e) != nil {
						continue
					}
					if c, _ := e["cwd"].(string); c != "" {
						cwd = c
						break
					}
				}
				if cwd != "" {
					break
				}
			}
			if cwd == "" { // no transcript left: reconstruct from the slug
				cwd = "/" + strings.ReplaceAll(strings.TrimLeft(filepath.Base(filepath.Dir(md)), "-"), "-", "/")
			}
			key, kconf := route(cwd, aliases, known)
			if confOrder[kconf] > confOrder[conf(key)] {
				confOf[key] = kconf
			}
			for _, f := range mds {
				if !fresh(f) {
					continue
				}
				raw, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				if _, ok := memory[key]; !ok {
					memory[key] = map[string]string{}
					memoryOrder = append(memoryOrder, key)
				}
				memory[key][filepath.Base(f)] = redact.Redact(string(raw))
				nMem[key]++
			}
		}
	}

	// ---- manifest ----
	keySet := map[string]bool{}
	for k := range nPrompts {
		keySet[k] = true
	}
	for k := range memory {
		keySet[k] = true
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		si := nPrompts[keys[i]] + nMem[keys[i]]
		sj := nPrompts[keys[j]] + nMem[keys[j]]
		if si != sj {
			return si > sj
		}
		return keys[i] < keys[j]
	})
	fmt.Fprintf(stdout, "%7s %5s %4s  CONF    KEY\n", "PROMPTS", "RESP", "MEM")
	fmt.Fprintln(stdout, strings.Repeat("-", 64))
	for _, k := range keys {
		if excluded[k] {
			// Python right-justifies the em dash by CHARACTERS, not bytes.
			fmt.Fprintf(stdout, "      —     —    —  skip    %s  (excluded)\n", k)
			continue
		}
		tag := ""
		if !existing[k] {
			tag = "  (NEW)"
		}
		fmt.Fprintf(stdout, "%7d %5d %4d  %-6s  %s%s\n", nPrompts[k], nResp[k], nMem[k], conf(k), k, tag)
	}

	// ---- write ----
	if !*apply {
		fmt.Fprintf(stdout, "\nDRY-RUN. %d projects. Re-run with --apply to write into %s.\n", len(keys), *data)
		fmt.Fprintln(stdout, "Opt out of a project:  --exclude <key>[,<key>...]   ·   fix routing:  --alias OLD=key")
		// History with no live remote pools in `miscellaneous`; deciding which
		// dead dir was which repo is a judgment call, so PROMPT the
		// setting-up agent to alias it rather than guess here.
		misc := map[string]string{}
		var miscOrder []string
		for _, gk := range groupOrder {
			if gk.key == "miscellaneous" {
				seg := filepath.Base(strings.TrimRight(gk.cwd, "/"))
				if _, ok := misc[seg]; !ok {
					misc[seg] = gk.cwd
					miscOrder = append(miscOrder, seg)
				}
			}
		}
		if len(misc) > 0 {
			fmt.Fprintln(stdout, "\nAGENT: history below is in `miscellaneous` — deleted worktrees with no")
			fmt.Fprintln(stdout, "live git remote to identify them. For each one you recognize as a real")
			fmt.Fprintln(stdout, "project, find its <owner>__<repo> (run `git -C <a live clone of it> remote")
			fmt.Fprintln(stdout, "get-url origin`, or `gh repo list`) and re-run with --alias <dir>=<owner>__<repo>")
			fmt.Fprintln(stdout, "to file it there. Leaving the rest in miscellaneous is fine — data is kept.")
			sort.Strings(miscOrder)
			for _, seg := range miscOrder {
				fmt.Fprintf(stdout, "  - %s\t(e.g. %s)\n", seg, misc[seg])
			}
		}
		return 0
	}

	// --tokens-only skips the prompt-log + memory writes.
	if !*tokensOnly {
		for _, gk := range groupOrder {
			if excluded[gk.key] {
				continue
			}
			entries := groups[gk]
			sort.SliceStable(entries, func(i, j int) bool { return entries[i].dt.Before(entries[j].dt) })
			d := filepath.Join(*data, "projects", gk.key, "log", gk.day)
			if err := os.MkdirAll(d, 0o755); err != nil {
				fmt.Fprintf(stderr, "devbrain import: %v\n", err)
				return 1
			}
			var b strings.Builder
			b.WriteString("# " + gk.key + " — " + gk.day + " — session " + gk.sid + "\n\n")
			b.WriteString("> devbrain Stage A raw prompt log. Append-only, source of truth.\n")
			b.WriteString("> worktree: " + gk.wt + " · cwd: " + gk.cwd + " · times in UTC\n>\n" + Banner + "\n")
			for _, e := range entries {
				b.WriteString("## " + e.dt.Format("15:04:05") + "\n\n" + e.prompt + "\n\n")
				if e.summary != "" {
					b.WriteString("↳ " + e.respDT.Format("15:04:05") + " — " + e.summary + "\n")
					if e.meta != "" {
						b.WriteString("   " + e.meta + "\n")
					}
					b.WriteString("\n")
				}
			}
			if err := os.WriteFile(filepath.Join(d, gk.wt+"."+gk.sid+".md"), []byte(b.String()), 0o644); err != nil {
				fmt.Fprintf(stderr, "devbrain import: %v\n", err)
				return 1
			}
		}
		for _, key := range memoryOrder {
			if excluded[key] {
				continue
			}
			d := filepath.Join(*data, "projects", key, "memory")
			if err := os.MkdirAll(d, 0o755); err != nil {
				fmt.Fprintf(stderr, "devbrain import: %v\n", err)
				return 1
			}
			names := make([]string, 0, len(memory[key]))
			for name := range memory[key] {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if err := os.WriteFile(filepath.Join(d, name), []byte(memory[key][name]), 0o644); err != nil {
					fmt.Fprintf(stderr, "devbrain import: %v\n", err)
					return 1
				}
			}
		}
	}

	// ---- token sidecars (append-only, idempotent) ----
	// Rows are re-derived per turn for every session whose transcript is
	// still on disk: strip that session's older rows (partial Stop-hook
	// captures, rows predating the turn key, stale routes) before the global
	// dedup pass, so the re-derived complete rows replace them. Sessions
	// whose transcripts were pruned keep their rows untouched. Codex rows are
	// matched by model since a codex sid's sidecar rows are codex-modeled.
	if len(codexReplace)+len(claudeReplace) > 0 {
		sidecars, _ := filepath.Glob(filepath.Join(*data, "projects", "*", "tokens.jsonl"))
		for _, sc := range sidecars {
			raw, err := os.ReadFile(sc)
			if err != nil {
				continue
			}
			var kept []string
			changed := false
			for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
				e := map[string]any{}
				if json.Unmarshal([]byte(line), &e) != nil {
					kept = append(kept, line)
					continue
				}
				model, _ := e["model"].(string)
				sess, _ := e["session"].(string)
				isCodexModel := strings.HasPrefix(model, "gpt-") || strings.Contains(model, "codex")
				if codexReplace[sess] && isCodexModel {
					changed = true
					continue
				}
				if claudeReplace[sess] && !isCodexModel {
					changed = true
					continue
				}
				kept = append(kept, line)
			}
			if changed {
				out := ""
				if len(kept) > 0 {
					out = strings.Join(kept, "\n") + "\n"
				}
				_ = os.WriteFile(sc, []byte(out), 0o644)
			}
		}
	}

	// Global (session, ts) dedup across EVERY project's sidecar: a turn
	// already recorded under project A must not be re-added under project B
	// when its routing changed between live capture and this backfill.
	seen := map[[2]string]bool{}
	sidecars, _ := filepath.Glob(filepath.Join(*data, "projects", "*", "tokens.jsonl"))
	for _, sc := range sidecars {
		raw, err := os.ReadFile(sc)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			e := map[string]any{}
			if json.Unmarshal([]byte(line), &e) != nil {
				continue
			}
			sess, _ := e["session"].(string)
			ts, _ := e["ts"].(string)
			seen[[2]string{sess, ts}] = true
		}
	}
	for _, key := range tokenOrder {
		if excluded[key] {
			continue
		}
		var fresh []tokenRow
		for _, r := range tokenRecs[key] {
			if !seen[[2]string{r.session, r.ts}] {
				fresh = append(fresh, r)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		d := filepath.Join(*data, "projects", key)
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintf(stderr, "devbrain import: %v\n", err)
			return 1
		}
		sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].ts < fresh[j].ts })
		f, err := os.OpenFile(filepath.Join(d, "tokens.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(stderr, "devbrain import: %v\n", err)
			return 1
		}
		for _, r := range fresh {
			fmt.Fprintln(f, r.json())
			seen[[2]string{r.session, r.ts}] = true // guard intra-run cross-route dups
		}
		f.Close()
	}
	var tokKeys []string
	for _, k := range tokenOrder {
		if !excluded[k] {
			tokKeys = append(tokKeys, k)
		}
	}
	sort.Strings(tokKeys)
	if *tokensOnly {
		fmt.Fprintf(stdout, "\nApplied (tokens-only). Wrote token sidecars for %d projects: %s\n",
			len(tokKeys), strings.Join(tokKeys, ", "))
	} else {
		n := 0
		for _, k := range keys {
			if !excluded[k] {
				n++
			}
		}
		fmt.Fprintf(stdout, "\nApplied. Wrote logs for %d projects + memory stores into %s.\n", n, *data)
		fmt.Fprintln(stdout, "Next: run /distill (or /continue) per project to fold this into searchable brain pages.")
	}
	return 0
}

// anyFreshGlob reports whether any file matching pattern passes the fresh
// gate (the sweep's subagent-transcript check).
func anyFreshGlob(fresh func(string) bool, pattern string) bool {
	files, _ := filepath.Glob(pattern)
	for _, f := range files {
		if fresh(f) {
			return true
		}
	}
	return false
}
