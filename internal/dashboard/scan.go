// The read-side scanners behind /api/prompts, /api/gbrain and /api/tokens —
// ports of scan_prompts/classify/gbrain_queries/token_usage/project_repo in
// the retired queue.py.
package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TheWeiHu/devbrain/internal/gbrainlog"
)

var (
	promptRe = regexp.MustCompile(`^## (\d{2}:\d{2}:\d{2})\s*$`)
	headerRe = regexp.MustCompile(`worktree:\s*(\S+).*?cwd:\s*(\S+)`)
	// Skill invocations recorded in a turn's `tools:` meta line:
	// `Skill:distill×2` (named) or a bare `Skill×2` (older logs).
	skillMetaRe = regexp.MustCompile(`Skill(?::([^×,]+))?×(\d+)`)
	nsCwdRe     = regexp.MustCompile(`/(?:nightshift|drain)/`)
	nsWtRe      = regexp.MustCompile(`-w\d+$`)
	// payloadVoiceRe matches a prompt that OPENS in agent-instruction voice — a
	// review/judge payload pasted by a skill or `claude -p`/codex, not you steering.
	// An optional leading markdown header (`# Autonomous loop check\n…`) is skipped
	// so a titled payload still matches on its second line.
	payloadVoiceRe = regexp.MustCompile(`(?i)^\s*(?:#[^\n]*\n\s*)?(?:you are|you're|review this|review the|you are reviewing|you are doing a code review)\b`)
)

// typedKinds are the "you, at the keyboard" prompt kinds.
var typedKinds = map[string]bool{"human": true, "command": true}

// SessionIsAutonomous is true for a nightshift worker session — by its
// worktree path / name.
func SessionIsAutonomous(cwd, worktree string) bool {
	return nsCwdRe.MatchString(cwd) || nsWtRe.MatchString(worktree)
}

// Classify returns the kind for a prompt, or "" to skip (empty prompt).
// autonomous forces a keyboard turn (human/command) to "nightshift".
func Classify(s string, autonomous bool) string {
	s = pyStrip(s)
	if s == "" {
		return ""
	}
	for _, p := range []string{"<system_instruction>", "<local-command-caveat>", "<command-", "<task-notification>"} {
		if strings.HasPrefix(s, p) {
			return "system"
		}
	}
	if strings.HasPrefix(s, "You are generating a short conversation title") {
		return "title-gen"
	}
	head := s
	if r := []rune(s); len(r) > 200 {
		head = string(r[:200])
	}
	if strings.Contains(head, "Caveat: The messages below were generated") {
		return "system"
	}
	if strings.HasPrefix(s, "PLANNING TURN:") ||
		strings.HasPrefix(s, "Check in on the nightshift") || strings.HasPrefix(s, "Check on the nightshift") {
		return "nightshift"
	}
	kind := "human"
	if strings.HasPrefix(s, "/") {
		kind = "command"
	}
	if autonomous {
		return "nightshift"
	}
	return kind
}

// Prompt is one classified prompt record (field names pinned by the
// dashboard + testdata/golden/api/prompts-*.json).
type Prompt struct {
	P      string   `json:"p"`
	S      string   `json:"s"`
	Date   string   `json:"date"`
	Time   string   `json:"time"`
	DT     string   `json:"dt"`
	Hour   int      `json:"h"`
	WD     string   `json:"wd"`
	Chars  int      `json:"c"`
	Words  int      `json:"w"`
	X      string   `json:"x"`
	Kind   string   `json:"kind"`
	Skills []string `json:"sk"`
	Recap  string   `json:"r"`
}

// cutoffDate mirrors queue.py's window: (today - days) local, or the
// always-passing sentinel for days=0.
func (q *Queue) cutoffDate(days int) string {
	if days == 0 {
		return "0000-00-00"
	}
	return q.Now().AddDate(0, 0, -days).Format("2006-01-02")
}

// ScanPrompts returns every prompt in the window, each tagged with its
// Classify() kind (scan_prompts).
func (q *Queue) ScanPrompts(days int, project string) []*Prompt {
	cutoff := q.cutoffDate(days)
	out := []*Prompt{}
	files, _ := filepath.Glob(filepath.Join(q.projectsDir(), "*", "log", "*", "*.md"))
	for _, md := range files {
		parts := strings.Split(md, string(os.PathSeparator))
		date, proj := parts[len(parts)-2], parts[len(parts)-4]
		sess := strings.TrimSuffix(parts[len(parts)-1], ".md")
		// Classify over the FULL corpus — every project, every date — so a prompt's kind can't
		// flip with the query params: repeat detection groups over full per-project history, and
		// the cross-project payload signal needs all projects. The project + date filters are
		// applied AFTER classification, below.
		raw, err := os.ReadFile(md)
		if err != nil {
			continue
		}
		lines := splitPyLines(string(raw))
		auton := false
		for i, l := range lines {
			if i >= 6 {
				break
			}
			if h := headerRe.FindStringSubmatch(l); h != nil {
				auton = SessionIsAutonomous(h[2], h[1])
				break
			}
		}
		i := 0
		for i < len(lines) {
			m := promptRe.FindStringSubmatch(lines[i])
			if m == nil {
				i++
				continue
			}
			ts := m[1]
			var body []string
			j := i + 1
			for j < len(lines) && !promptRe.MatchString(lines[j]) &&
				!strings.HasPrefix(pyLStrip(lines[j]), "↳") {
				body = append(body, lines[j])
				j++
			}
			text := pyStrip(strings.Join(body, "\n"))
			// Scan the response block for the `tools:` META LINE — only it
			// counts; a response sample can quote "Skill×1" as prose.
			var skills []string
			for k := j; k < len(lines) && !promptRe.MatchString(lines[k]); k++ {
				s := pyLStrip(lines[k])
				if (strings.HasPrefix(s, "touched:") || strings.HasPrefix(s, "tools:")) &&
					strings.Contains(s, "tools:") {
					for _, sm := range skillMetaRe.FindAllStringSubmatch(lines[k], -1) {
						name := pyStrip(sm[1])
						if name == "" {
							name = "?"
						}
						n, _ := pyIntStr(sm[2])
						for x := 0; x < n; x++ {
							skills = append(skills, name)
						}
					}
				}
			}
			// The turn's ↳ recap line, so a drill-in shows "what happened".
			recap := ""
			if j < len(lines) && strings.HasPrefix(pyLStrip(lines[j]), "↳") {
				rl := pyStrip(strings.TrimPrefix(pyLStrip(lines[j]), "↳"))
				if _, after, found := strings.Cut(rl, "—"); found {
					recap = pyStrip(after)
				} else {
					recap = rl
				}
			}
			if kind := Classify(text, auton); kind != "" {
				if dt, err := time.Parse("2006-01-02 15:04:05", date+" "+ts); err == nil {
					if skills == nil {
						skills = []string{}
					}
					out = append(out, &Prompt{
						P: proj, S: sess, Date: date, Time: ts[:5],
						DT: dt.Format("2006-01-02T15:04:05"), Hour: dt.Hour(),
						WD: dt.Format("Mon"), Chars: utf8.RuneCountInString(text),
						Words: len(strings.Fields(text)), X: text, Kind: kind,
						Skills: skills, Recap: recap,
					})
				}
			}
			i = j
		}
	}
	reclassifyRepeats(out)   // over the full per-project corpus, before the project/date filters
	reclassifyPayloads(out)  // single-instance agent payloads, same corpus/pre-filter pass
	windowed := out[:0]      // now drop out-of-window / other-project records (cutoff is the always-pass sentinel for days=0)
	for _, r := range out {
		if r.Date >= cutoff && (project == "" || r.P == project) {
			windowed = append(windowed, r)
		}
	}
	out = windowed
	sort.SliceStable(out, func(a, b int) bool { return out[a].DT < out[b].DT })
	return out
}

// repeatSigLen is the normalized-text prefix used as the dedup key. A prefix (not the
// whole text) makes it a near-dup signature: a rubric whose only varying part is the
// item appended at the end still collapses into one group.
const repeatSigLen = 200

// repeatLongWords is the word count at/above which a prompt is a "payload" — a pasted
// rubric/spec, not something you'd hand-type. Two copies of that is already mechanical.
const repeatLongWords = 200

// repeatThreshold is how many copies a group needs before it's a payload, as a function
// of length. A short prompt needs 3+ (you might fire a one-liner twice); a long one is a
// payload at just 2. Returns the count a group must EXCEED to flip.
func repeatThreshold(words int) int {
	if words >= repeatLongWords {
		return 1 // long: flip at 2+
	}
	return 2 // short: flip at 3+
}

// reclassifyRepeats moves pasted-payload prompts off the typed side. When the same (or
// near-identical) typed prompt appears enough times in a project — an LLM rubric or system
// prompt pasted once per batch item — it's a payload, not you steering, and it swamps the
// typed word cloud. Group typed records per project by a normalized text prefix (catches
// exact repeats and shared-preamble near-dups); any group past its length-aware threshold
// flips to "repeat", which FilterKind/typedKinds route to the bot side. Called on the full
// per-project corpus (before the date window), so a prompt's kind doesn't flip with the query window.
func reclassifyRepeats(recs []*Prompt) {
	type key struct{ proj, sig string }
	groups := map[key][]*Prompt{}
	for _, r := range recs {
		if !typedKinds[r.Kind] {
			continue
		}
		k := key{r.P, repeatSig(r.X)}
		groups[k] = append(groups[k], r)
	}
	for _, g := range groups {
		maxWords := 0
		for _, r := range g {
			if r.Words > maxWords {
				maxWords = r.Words
			}
		}
		if len(g) > repeatThreshold(maxWords) {
			for _, r := range g {
				r.Kind = "repeat"
			}
		}
	}
}

// payloadMinWords is the length floor for the single-instance payload signals. Below it a
// prompt is short enough to be you at the keyboard even in an imperative voice.
const payloadMinWords = 150

// reclassifyPayloads moves single-instance agent payloads — a review/judge prompt pasted once
// by a skill or `claude -p`/codex, logged as a keyboard turn because it doesn't start with "/"
// — off the typed side. Repeat detection (above) only catches things pasted 2-3+ times; this
// catches the one-off. Two deterministic signals, both gated on payloadMinWords:
//   (1) the prompt OPENS in agent-instruction voice (payloadVoiceRe);
//   (2) its opener signature appears in ≥2 DIFFERENT projects — no human hand-types an
//       identical long prompt across unrelated repos, so it's a tool payload.
// Signal (2) only fires when the scan loaded the global corpus (the profile view, project="");
// a single-project query can't see cross-project repeats. Flipped records become "payload",
// which FilterKind/typedKinds route to the bot side. Called before the date window so kind is
// stable across query windows, matching reclassifyRepeats.
func reclassifyPayloads(recs []*Prompt) {
	// Cross-project evidence covers every originally-typed record — including ones
	// reclassifyRepeats already flipped to "repeat" — so a singleton in project B still
	// counts a copy already marked "repeat" in project A.
	wasTyped := func(kind string) bool { return typedKinds[kind] || kind == "repeat" }
	projSeen := map[string]map[string]bool{}
	for _, r := range recs {
		if !wasTyped(r.Kind) || r.Words < payloadMinWords {
			continue
		}
		sig := repeatSig(r.X)
		if projSeen[sig] == nil {
			projSeen[sig] = map[string]bool{}
		}
		projSeen[sig][r.P] = true
	}
	for _, r := range recs {
		if !typedKinds[r.Kind] || r.Words < payloadMinWords { // only flip records still on the typed side
			continue
		}
		if payloadVoiceRe.MatchString(r.X) || len(projSeen[repeatSig(r.X)]) >= 2 {
			r.Kind = "payload"
		}
	}
}

// repeatSig is the dedup signature: lowercased, whitespace-collapsed, first repeatSigLen runes.
func repeatSig(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	if r := []rune(s); len(r) > repeatSigLen {
		return string(r[:repeatSigLen])
	}
	return s
}

// FilterKind applies the typed/bot/all toggle.
func FilterKind(recs []*Prompt, kind string) []*Prompt {
	if kind == "all" {
		return recs
	}
	out := []*Prompt{}
	for _, r := range recs {
		if (kind == "bot") != typedKinds[r.Kind] {
			out = append(out, r)
		}
	}
	return out
}

// ProjectRepo is the best-effort local checkout path for a project, read
// from the `cwd:` header of its most recent INTERACTIVE session log
// (nightshift worker cwds are throwaway worktrees and skipped). "" if none.
func (q *Queue) ProjectRepo(project string) string {
	files, _ := filepath.Glob(filepath.Join(q.projectsDir(), project, "log", "*", "*.md"))
	sort.SliceStable(files, func(a, b int) bool {
		fa, _ := os.Stat(files[a])
		fb, _ := os.Stat(files[b])
		var ta, tb time.Time
		if fa != nil {
			ta = fa.ModTime()
		}
		if fb != nil {
			tb = fb.ModTime()
		}
		return tb.Before(ta) // newest first
	})
	for _, md := range files {
		head, err := readHead(md, 2000)
		if err != nil {
			continue
		}
		h := headerRe.FindStringSubmatch(head)
		if h == nil {
			continue
		}
		wt, cwd := h[1], h[2]
		if SessionIsAutonomous(cwd, wt) {
			continue
		}
		// .git is a file in a linked worktree, a dir in a clone
		if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
			return cwd
		}
	}
	return ""
}

// readHead reads up to n runes from the start of a file (Python text-mode
// read(n) counts characters).
func readHead(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 4*n)
	got, err := f.Read(buf)
	if got == 0 && err != nil {
		return "", err
	}
	s := string(buf[:got])
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s, nil
}

// --- gbrain read/value log ----------------------------------------------------

var (
	gbRead    = map[string]bool{"search": true, "query": true, "get": true}
	gbTopicRe = regexp.MustCompile(`gbrain\s+(?:search|query)\s+"([^"]{2,140})"`)
	// A real gbrain page is always <project>/<page>; requiring the slash keeps
	// prose mentions from surfacing junk targets.
	gbSlugRe = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._/-]+\z`)
)

// GBGetTarget names the page a `gbrain get` tried to read (display-only):
// the shared lib's parse, then the queue-side slug-shape fullmatch.
func GBGetTarget(cmd string) string {
	target := gbrainlog.GetTarget(cmd, false)
	if target != "" && gbSlugRe.MatchString(target) {
		return target
	}
	return ""
}

// GBQuery is one gbrain query-log record for the Brain Value card.
type GBQuery struct {
	TS     string `json:"ts"`
	Date   string `json:"date"`
	P      string `json:"p"`
	Read   bool   `json:"read"`
	Modes  []any  `json:"modes"`
	Hits   any    `json:"hits"`
	Slugs  any    `json:"slugs"`
	Q      string `json:"q"`
	Target string `json:"target"`
	Auto   bool   `json:"auto"` // nightshift/bot session vs typed; missing key -> false
}

// GBrainQueries reads every project's gbrain-queries.log (gbrain_queries).
func (q *Queue) GBrainQueries(days int, project string) []*GBQuery {
	cutoff := q.cutoffDate(days)
	out := []*GBQuery{}
	files, _ := filepath.Glob(filepath.Join(q.projectsDir(), "*", "gbrain-queries.log"))
	for _, f := range files {
		parts := strings.Split(f, string(os.PathSeparator))
		proj := parts[len(parts)-2]
		if project != "" && proj != project {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range splitPyLines(string(raw)) {
			line = pyStrip(line)
			if line == "" {
				continue
			}
			e, err := decodeJSONMap(line)
			if err != nil {
				continue
			}
			ts, _ := e["ts"].(string)
			if truncStr(ts, 10) < cutoff {
				continue
			}
			modes, _ := e["modes"].([]any)
			if modes == nil {
				modes = []any{}
			}
			cmd, _ := e["cmd"].(string)
			read := false
			for _, m := range modes {
				if ms, ok := m.(string); ok && gbRead[ms] {
					read = true
					break
				}
			}
			target := ""
			if containsStr(modes, "get") {
				target = GBGetTarget(cmd)
			}
			topic := ""
			if m := gbTopicRe.FindStringSubmatch(cmd); m != nil {
				topic = m[1]
			}
			hits := e["hits"]
			if !pyTruthy(hits) {
				hits = 0
			}
			slugs := e["slugs"]
			if !pyTruthy(slugs) {
				slugs = []any{}
			}
			auto, _ := e["auto"].(bool)
			out = append(out, &GBQuery{
				TS: ts, Date: truncStr(ts, 10), P: proj, Read: read,
				Modes: modes, Hits: hits, Slugs: slugs, Q: topic, Target: target, Auto: auto,
			})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].TS < out[b].TS })
	return out
}

func containsStr(xs []any, s string) bool {
	for _, x := range xs {
		if xs, ok := x.(string); ok && xs == s {
			return true
		}
	}
	return false
}

// truncStr is Python s[:n] by code points.
func truncStr(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// --- token-usage reader ---------------------------------------------------------

// TokenRec is one per-turn token record for the Token Cost card.
type TokenRec struct {
	TS      string `json:"ts"`
	Date    string `json:"date"`
	P       string `json:"p"`
	Model   any    `json:"model"`
	Session any    `json:"session"`
	In      any    `json:"in"`
	Out     any    `json:"out"`
	CC      any    `json:"cc"`
	CR      any    `json:"cr"`
	Auto    bool   `json:"auto"`
}

// TokenUsage reads every project's tokens.jsonl, deduped so a re-run, a
// sync, or a Stop-hook re-capture can't double-count (token_usage).
// Pricing-agnostic — the model id flows through untouched.
//
// Records carrying a "turn" key (the turn's stable user-prompt timestamp)
// dedup on (session, turn), keeping the record with the LATEST ts: the Stop
// hook can capture the same turn repeatedly as it grows, each capture
// re-summing its cumulative usage under a new last-assistant ts, so only
// the final capture is complete. Legacy records without "turn" keep the
// historical (session, ts) first-wins behavior.
func (q *Queue) TokenUsage(days int, project string) []*TokenRec {
	cutoff := q.cutoffDate(days)
	out := []*TokenRec{}
	seen := map[string]bool{}
	byTurn := map[string]int{} // (session, turn) key -> index in out
	files, _ := filepath.Glob(filepath.Join(q.projectsDir(), "*", "tokens.jsonl"))
	for _, f := range files {
		parts := strings.Split(f, string(os.PathSeparator))
		proj := parts[len(parts)-2]
		if project != "" && proj != project {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range splitPyLines(string(raw)) {
			line = pyStrip(line)
			if line == "" {
				continue
			}
			e, err := decodeJSONMap(line)
			if err != nil {
				continue
			}
			ts, _ := e["ts"].(string)
			if truncStr(ts, 10) < cutoff {
				continue
			}
			turn, _ := e["turn"].(string)
			if turn == "" {
				key := dedupKey(e["session"], ts)
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			orZero := func(k string) any {
				v, ok := e[k]
				if !ok || !pyTruthy(v) {
					return 0
				}
				return v
			}
			orEmpty := func(k string) any {
				v := e[k]
				if !pyTruthy(v) {
					return ""
				}
				return v
			}
			rec := &TokenRec{
				TS: ts, Date: truncStr(ts, 10), P: proj,
				Model: orEmpty("model"), Session: orEmpty("session"),
				In: orZero("in"), Out: orZero("out"),
				CC: orZero("cache_create"), CR: orZero("cache_read"),
				Auto: pyTruthy(e["auto"]),
			}
			if turn != "" {
				key := dedupKey(e["session"], "\x01turn\x00"+turn)
				if i, ok := byTurn[key]; ok {
					if ts >= out[i].TS { // keep the latest (most complete) capture
						out[i] = rec
					}
					continue
				}
				byTurn[key] = len(out)
			}
			out = append(out, rec)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].TS < out[b].TS })
	return out
}

// dedupKey distinguishes a missing session (Python None) from "" and keeps
// distinct JSON types distinct.
func dedupKey(session any, ts string) string {
	tag := "?"
	switch x := session.(type) {
	case nil:
		tag = "n"
	case string:
		tag = "s:" + x
	default:
		b, _ := json.Marshal(x) // json.Number round-trips verbatim
		tag = "j:" + string(b)
	}
	return tag + "\x00" + ts
}
