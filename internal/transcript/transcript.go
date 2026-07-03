// Package transcript parses agent transcript JSONL (Claude Code events and
// Codex rollout files) into turns and produces the Stop-capture
// summary/meta/body triple. It is the Go port of recap()/sample()/
// transcript_turns()/response_capture() in the legacy hooks/devbrain_lib.py;
// string handling mirrors Python semantics (see pytext.go) so outputs stay
// byte-identical.
package transcript

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TheWeiHu/devbrain/internal/redact"
)

// Counter is an insertion-ordered string->int map (OrderedDict tools port).
type Counter struct {
	keys []string
	m    map[string]int
}

func (c *Counter) Inc(key string, n int) {
	if c.m == nil {
		c.m = make(map[string]int)
	}
	if _, ok := c.m[key]; !ok {
		c.keys = append(c.keys, key)
	}
	c.m[key] += n
}

func (c *Counter) Keys() []string     { return c.keys }
func (c *Counter) Get(key string) int { return c.m[key] }
func (c *Counter) Len() int           { return len(c.keys) }

// Set is an insertion-ordered string set (OrderedDict files port).
type Set struct {
	keys []string
	m    map[string]bool
}

func (s *Set) Add(key string) {
	if s.m == nil {
		s.m = make(map[string]bool)
	}
	if !s.m[key] {
		s.m[key] = true
		s.keys = append(s.keys, key)
	}
}

func (s *Set) Keys() []string { return s.keys }

// Turn is one prompt + its assistant details (the turn dict of the Python lib).
type Turn struct {
	DT, CWD, Prompt                       string
	Texts                                 []string
	Tools                                 *Counter
	Files                                 *Set
	TurnTS                                string
	Input, Output, CacheCreate, CacheRead int
	Model                                 string
}

// --- recap / sample --------------------------------------------------------

// sentence splitter for the already-whitespace-collapsed recap line;
// Python: re.findall(r".+?[.!?](?:\s|$)", chosen)
var sentenceRe = regexp.MustCompile(`.+?[.!?](?: |$)`)

// Recap returns the turn's CLOSING sentence — the recap lives at the end of
// the final assistant message. Port of recap().
func Recap(texts []string) string {
	lastText := ""
	for _, t := range texts {
		if pyStrip(t) != "" {
			lastText = t
		}
	}
	chosen := ""
	for _, line := range splitLines(lastText) {
		s := trimLeadingClass(pyStrip(line), ">-*")
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		chosen = s // keep going -> ends on the LAST substantive line
	}
	if chosen == "" {
		chosen = trimLeadingClass(pyStrip(lastText), "#>-*")
	}
	chosen = pyStrip(collapseWS(chosen))
	parts := sentenceRe.FindAllString(chosen, -1)
	var summary string
	if len(parts) > 0 {
		summary = pyStrip(parts[len(parts)-1])
		if utf8.RuneCountInString(summary) < 60 && len(parts) > 1 { // extend a too-short tail backwards
			summary = pyStrip(pyStrip(parts[len(parts)-2]) + " " + summary)
		}
	} else {
		summary = chosen
	}
	return pyStrip(truncRunes(summary, 500))
}

// Sample returns a bounded head+middle sample of the whole turn's prose (the
// recap is the tail). Port of sample(); all offsets are code points.
func Sample(texts []string) string {
	var kept []string
	for _, t := range texts {
		if s := pyStrip(t); s != "" {
			kept = append(kept, s)
		}
	}
	full := regexp.MustCompile(`\n{3,}`).ReplaceAllString(pyStrip(strings.Join(kept, "\n\n")), "\n\n")
	const maxChars, head, mid = 4000, 2200, 1400 // maxChars ~700 words; whole if under it
	rs := []rune(full)
	if len(rs) <= maxChars {
		return full
	}
	h := string(rs[:head])
	if i := strings.LastIndex(h, " "); i >= 0 { // snap off a partial word
		h = h[:i]
	}
	h = pyStrip(h)
	c := len(rs) / 2
	m := string(rs[c-mid/2 : c+mid/2])
	if i := strings.Index(m, " "); i >= 0 { // trim partial words both ends
		m = m[i+1:]
	}
	if i := strings.LastIndex(m, " "); i >= 0 {
		m = m[:i]
	}
	m = pyStrip(m)
	return h + "\n\n[…]\n\n" + m + "\n\n[…]"
}

// --- JSON event helpers ----------------------------------------------------

func getStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func getMap(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	return v
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// num reads a JSON number field as float64; anything else is 0 (Python's
// `usage.get(...) or 0` with number fields).
func num(v any) float64 {
	if n, ok := v.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return 0
}

// numOK reports a parseable JSON number and its value.
func numOK(v any) (float64, bool) {
	if n, ok := v.(json.Number); ok {
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// pyTruthy is Python bool(v) over decoded JSON values.
func pyTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// pyScalarStr is Python str(v) for the scalar JSON types we can meet where the
// legacy code stringified a value (tool names, skill names).
func pyScalarStr(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case json.Number:
		return string(x), true
	case bool:
		if x {
			return "True", true
		}
		return "False", true
	}
	return "", false
}

// basename is Python fp.rsplit("/", 1)[-1].
func basename(p string) string {
	return p[strings.LastIndex(p, "/")+1:]
}

// --- Claude transcript parsing ----------------------------------------------

// contentText is _content_text: a message content that is either a plain
// string or a list of {"type":"text"} blocks.
func contentText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, x := range c {
			if bm, ok := x.(map[string]any); ok && getStr(bm, "type") == "text" {
				b.WriteString(getStr(bm, "text"))
			}
		}
		return b.String()
	}
	return ""
}

// assistantDetails is _assistant_details: texts/tools/files/tokens/model for
// one turn's assistant events. Token usage is counted once per message id —
// a missing id dedups as one shared key, matching Python's None-in-set.
// Within one id the transcript repeats the usage snapshot per content-block
// line and the snapshot GROWS as output streams (input/cache fields are set
// at request start; output_tokens climbs), so each field takes the per-id
// MAX rather than the first snapshot, which under-counted output ~35%.
func assistantDetails(events []map[string]any) Turn {
	t := Turn{Tools: &Counter{}, Files: &Set{}}
	usageByID := map[string]*[4]float64{}
	var idOrder []string
	for _, e := range events {
		if getStr(e, "type") != "assistant" {
			continue
		}
		msg := getMap(e, "message")
		key := idKey(msg["id"])
		u := usageByID[key]
		if u == nil {
			u = &[4]float64{}
			usageByID[key] = u
			idOrder = append(idOrder, key)
		}
		usage := getMap(msg, "usage")
		for i, f := range []string{"input_tokens", "output_tokens",
			"cache_creation_input_tokens", "cache_read_input_tokens"} {
			if v := num(usage[f]); v > u[i] {
				u[i] = v
			}
		}
		if m := getStr(msg, "model"); m != "" {
			t.Model = m
		}
		if ts := getStr(e, "timestamp"); ts != "" {
			t.TurnTS = ts
		}
		for _, b := range asList(msg["content"]) {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch getStr(bm, "type") {
			case "text":
				t.Texts = append(t.Texts, getStr(bm, "text"))
			case "tool_use":
				name := "?"
				if s, ok := pyScalarStr(bm["name"]); ok {
					name = s
				}
				inp := getMap(bm, "input")
				if name == "Skill" {
					sv := inp["skill"]
					if !pyTruthy(sv) {
						sv = inp["name"]
					}
					if pyTruthy(sv) {
						if s, ok := pyScalarStr(sv); ok {
							name = "Skill:" + s
						}
					}
				}
				t.Tools.Inc(name, 1)
				fp := getStr(inp, "file_path")
				if fp == "" {
					fp = getStr(inp, "path")
				}
				if fp != "" {
					t.Files.Add(basename(fp))
				}
			}
		}
	}
	var tin, tout, tcc, tcr float64
	for _, key := range idOrder {
		u := usageByID[key]
		tin += u[0]
		tout += u[1]
		tcc += u[2]
		tcr += u[3]
	}
	t.Input, t.Output, t.CacheCreate, t.CacheRead = int(tin), int(tout), int(tcc), int(tcr)
	return t
}

// idKey builds the dedup key for a message id, keeping distinct JSON types
// distinct and treating a missing id like Python's None.
func idKey(v any) string {
	switch x := v.(type) {
	case nil:
		return "\x00none"
	case string:
		return "s:" + x
	case json.Number:
		return "n:" + string(x)
	case bool:
		if x {
			return "b:1"
		}
		return "b:0"
	default:
		b, _ := json.Marshal(x)
		return "j:" + string(b)
	}
}

// claudeTurns is the Claude branch of transcript_turns(). includeSidechain
// is false for main transcripts (sidechain user events are injected noise
// there) and true for subagent transcripts, where EVERY event is sidechain.
func claudeTurns(events []map[string]any, filterSynthetic, includeSidechain bool) []Turn {
	var turns []Turn
	var cur *Turn
	var curEvents []map[string]any
	finish := func() {
		if cur == nil {
			return
		}
		d := assistantDetails(curEvents)
		d.DT, d.CWD, d.Prompt = cur.DT, cur.CWD, cur.Prompt
		turns = append(turns, d)
		cur, curEvents = nil, nil
	}
	for _, e := range events {
		switch getStr(e, "type") {
		case "user":
			if !includeSidechain && pyTruthy(e["isSidechain"]) {
				continue
			}
			prompt := pyStrip(contentText(getMap(e, "message")["content"]))
			if prompt == "" || (filterSynthetic && redact.IsSynthetic(prompt)) {
				continue
			}
			finish()
			cur = &Turn{DT: getStr(e, "timestamp"), CWD: getStr(e, "cwd"), Prompt: prompt}
		case "assistant":
			if cur != nil {
				curEvents = append(curEvents, e)
			}
		}
	}
	finish()
	return turns
}

// --- JSONL reading -----------------------------------------------------------

// parseEvent is one json.loads: strict (trailing garbage rejected), numbers
// preserved as json.Number, and only objects kept as events.
func parseEvent(line string) (map[string]any, bool) {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// readJSONL is _read_jsonl: parse each line, skipping blanks and invalid
// JSON; tailLines>0 keeps only the LAST tailLines physical lines (deque).
func readJSONL(path string, tailLines int) ([]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" { // file iteration yields no empty final line
		lines = lines[:n-1]
	}
	if tailLines > 0 && len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	var events []map[string]any
	for _, ln := range lines {
		ln = pyStrip(ln)
		if ln == "" {
			continue
		}
		if e, ok := parseEvent(ln); ok {
			events = append(events, e)
		}
	}
	return events, nil
}

// Turns is transcript_turns(): parse transcript turns into prompt + assistant
// details. A missing or unreadable file yields nil (fail open).
func Turns(path string, tailLines int, filterSynthetic bool) []Turn {
	events, err := readJSONL(path, tailLines)
	if err != nil {
		return nil
	}
	for _, e := range events {
		if isCodexEvent(e) {
			return codexTurns(events, filterSynthetic)
		}
	}
	return claudeTurns(events, filterSynthetic, false)
}

// --- meta line / timestamps ---------------------------------------------------

// MetaLine is _meta_line: "touched: …  ·  tools: …  ·  tokens: i/o/cc/cr · model: m".
func MetaLine(t Turn, includeTokens bool) string {
	var meta []string
	if t.Files != nil && len(t.Files.Keys()) > 0 {
		meta = append(meta, "touched: "+strings.Join(t.Files.Keys(), ", "))
	}
	if t.Tools != nil && t.Tools.Len() > 0 {
		parts := make([]string, 0, t.Tools.Len())
		for _, k := range t.Tools.Keys() {
			parts = append(parts, k+"×"+strconv.Itoa(t.Tools.Get(k)))
		}
		meta = append(meta, "tools: "+strings.Join(parts, ", "))
	}
	if includeTokens && (t.Input != 0 || t.Output != 0 || t.CacheCreate != 0 || t.CacheRead != 0) {
		tok := "tokens: " + strconv.Itoa(t.Input) + "/" + strconv.Itoa(t.Output) +
			"/" + strconv.Itoa(t.CacheCreate) + "/" + strconv.Itoa(t.CacheRead)
		if t.Model != "" {
			tok += " · model: " + t.Model
		}
		meta = append(meta, tok)
	}
	return strings.Join(meta, "  ·  ")
}

// isoSeconds is _iso_seconds: normalize an ISO-ish timestamp to
// %Y-%m-%dT%H:%M:%SZ, or fallback when unparseable. Like Python's
// fromisoformat+strftime it formats the WALL-CLOCK fields of the parsed
// offset (no UTC conversion) — identical for the Z/+00:00 inputs transcripts
// carry.
func isoSeconds(ts, fallback string) string {
	if ts == "" {
		return fallback
	}
	s := strings.ReplaceAll(ts, "Z", "+00:00")
	layouts := []string{
		"2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02T15:04:05.999999999-07",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999-0700",
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02T15:04:05") + "Z"
		}
	}
	return fallback
}

// --- response capture -----------------------------------------------------------

// ToolResultsMarker delimits the optional tool-results digest that
// ResponseCapture appends after the response sample; hooks.go splits on it to
// render the digest under its own heading. A record-separator prefix keeps it
// out of any prose the sample or a result could carry.
const ToolResultsMarker = "\x1etool results:"

// resultTools are the high-signal tools whose RETURN value carries the turn's
// derivation (what git/gbrain/searches actually showed) at a bounded size —
// file dumps (Read) and the rest are excluded to keep the digest small.
var resultTools = map[string]bool{"Bash": true, "Grep": true, "Glob": true}

// flatWS collapses all whitespace runs (incl. newlines) to single spaces and
// strips the ends, so a multi-line tool result renders as one bounded log line.
func flatWS(s string) string { return pyStrip(collapseWS(s)) }

// clip trims s to at most n runes, appending "…" when it had to cut.
func clip(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return strings.TrimRight(string(rs[:n]), " ") + "…"
}

// ToolResultsSummary is a bounded, model-free digest of what the LAST turn's
// high-signal tools RETURNED — each result paired with the call that produced
// it — so a log-only distill can see WHY a turn moved, not just which tools
// ran. Claude events only (Codex tool shape differs); "" when nothing matches.
// Bounded per-result and in total; the caller redacts the output.
func ToolResultsSummary(events []map[string]any) string {
	// last real (non-sidechain, non-empty) user prompt starts the turn.
	start := 0
	for i, e := range events {
		if getStr(e, "type") != "user" || pyTruthy(e["isSidechain"]) {
			continue
		}
		if pyStrip(contentText(getMap(e, "message")["content"])) != "" {
			start = i
		}
	}
	turn := events[start:]

	type call struct{ id, name, label string }
	var calls []call
	results := map[string]string{}
	errs := map[string]bool{}
	for _, e := range turn {
		msg := getMap(e, "message")
		for _, b := range asList(msg["content"]) {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch getStr(bm, "type") {
			case "tool_use":
				name, _ := pyScalarStr(bm["name"])
				if !resultTools[name] {
					continue
				}
				inp := getMap(bm, "input")
				label := getStr(inp, "command")
				if label == "" {
					label = getStr(inp, "pattern")
				}
				calls = append(calls, call{getStr(bm, "id"), name, flatWS(label)})
			case "tool_result":
				id := getStr(bm, "tool_use_id")
				if id == "" {
					continue
				}
				results[id] = flatWS(contentText(bm["content"]))
				errs[id] = pyTruthy(bm["is_error"])
			}
		}
	}

	const maxLines, perResult, totalCap = 10, 200, 1400
	var out []string
	total := 0
	for _, c := range calls {
		res, ok := results[c.id]
		if !ok || res == "" {
			continue
		}
		line := "- " + c.name
		if c.label != "" {
			line += "(" + clip(c.label, 60) + ")"
		}
		line += ": "
		if errs[c.id] {
			line += "ERR "
		}
		line += clip(res, perResult)
		out = append(out, line)
		total += utf8.RuneCountInString(line)
		if len(out) >= maxLines || total >= totalCap {
			break
		}
	}
	return strings.Join(out, "\n")
}

// pyDirname is os.path.dirname for slash paths.
func pyDirname(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	head := p[:i+1]
	if t := strings.TrimRight(head, "/"); t != "" {
		return t
	}
	return head // all slashes (e.g. "/x" -> "/")
}

// TurnKey canonicalizes a turn's user-prompt timestamp into the stable
// identity stamped on sidecar records ("" when the turn has none). Every
// sidecar writer (live capture, importer) must use this same form so a turn
// re-captured by different writers dedups to one row at read time.
func TurnKey(dt string) string {
	return isoSeconds(dt, "")
}

// appendSidecar writes the token-usage JSONL record, serialized like
// Python's json.dumps({"ts": …, "session": …, …}). All failures are
// swallowed (fail open), including a sidecar path with no directory —
// Python's makedirs("") raises and skips the write.
//
// "ts" is the turn's LAST-assistant timestamp, which ADVANCES when the Stop
// hook re-captures a still-growing turn — so it cannot identify the turn.
// "turn" is the stable identity (the user-prompt timestamp) that lets the
// read side collapse those cumulative re-captures to the final one.
func appendSidecar(sidecar string, t Turn, session, fallbackTS string, auto bool) {
	appendSidecarKey(sidecar, t, session, fallbackTS, auto, TurnKey(t.DT))
}

// appendSidecarKey is appendSidecar with an explicit turn key (subagent rows
// prefix theirs with the agent id so parallel agents can't collide).
func appendSidecarKey(sidecar string, t Turn, session, fallbackTS string, auto bool, turnKey string) {
	dir := pyDirname(sidecar)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return
	}
	autoStr := "false"
	if auto {
		autoStr = "true"
	}
	rec := `{"ts": ` + pyJSONString(isoSeconds(t.TurnTS, fallbackTS)) +
		`, "session": ` + pyJSONString(session) +
		`, "model": ` + pyJSONString(t.Model) +
		`, "in": ` + strconv.Itoa(t.Input) +
		`, "out": ` + strconv.Itoa(t.Output) +
		`, "cache_create": ` + strconv.Itoa(t.CacheCreate) +
		`, "cache_read": ` + strconv.Itoa(t.CacheRead) +
		`, "auto": ` + autoStr +
		`, "turn": ` + pyJSONString(turnKey) + "}"
	f, err := os.OpenFile(sidecar, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(rec + "\n")
}

// AgentTurns parses a SUBAGENT transcript. Same shape as Turns, but every
// event in an agent file is sidechain (isSidechain: true), so the main
// transcript's sidechain skip must not apply.
func AgentTurns(path string, tailLines int) []Turn {
	events, err := readJSONL(path, tailLines)
	if err != nil {
		return nil
	}
	return claudeTurns(events, false, true)
}

// SubagentTurnKey is the stable identity for a subagent's turn: the agent id
// (its transcript basename, "agent-<id>") plus the turn's canonical TurnKey.
// The prefix keeps parallel agents that started the same second distinct, and
// live capture and the importer must build it identically. "" when the turn
// has no timestamp (falls back to legacy (session, ts) dedup at read time).
func SubagentTurnKey(agentPath, turnKey string) string {
	if turnKey == "" {
		return ""
	}
	agent := strings.TrimSuffix(basename(agentPath), ".jsonl")
	return agent + ":" + turnKey
}

// SubagentCapture appends a sidecar row for the LAST turn of a subagent
// transcript — the turn the SubagentStop event just finished, mirroring the
// Stop hook's last-turn contract (earlier turns of a resumed agent were
// captured by earlier fires; the importer backfills anything missed). Tokens
// only — subagent turns never touch the prompt log. session is the PARENT
// session id: subagent usage bills to the session that spawned it, like
// ccusage.
func SubagentCapture(agentPath, sidecar, session, fallbackTS string, auto bool) {
	if sidecar == "" {
		return
	}
	turns := AgentTurns(agentPath, 1500)
	if len(turns) == 0 {
		return
	}
	turn := turns[len(turns)-1]
	if turn.Input == 0 && turn.Output == 0 && turn.CacheCreate == 0 && turn.CacheRead == 0 {
		return
	}
	appendSidecarKey(sidecar, turn, session, fallbackTS, auto, SubagentTurnKey(agentPath, TurnKey(turn.DT)))
}

// ResponseCapture is response_capture(): summarize the LAST turn of the
// transcript into "summary\nmeta\nbody" (redacted), appending token usage to
// the sidecar JSONL. Unreadable transcripts yield "" (the CLI's fail-open).
func ResponseCapture(transcriptPath, sidecar, session, fallbackTS string, auto bool, fallbackText string) string {
	events, err := readJSONL(transcriptPath, 1500)
	if err != nil {
		return ""
	}
	codex := false
	for _, e := range events {
		if isCodexEvent(e) {
			codex = true
			break
		}
	}
	var turn Turn
	if codex {
		turns := codexTurns(events, false)
		if len(turns) > 0 {
			turn = turns[len(turns)-1]
		} else {
			lastUser := -1
			for i, e := range events {
				if isCodexUserPrompt(e) {
					lastUser = i
				}
			}
			var prior []map[string]any
			if lastUser >= 0 {
				prior = events[:lastUser+1]
			}
			turn = codexDetails(events[lastUser+1:], prior)
		}
	} else {
		turns := claudeTurns(events, false, false)
		if len(turns) > 0 {
			turn = turns[len(turns)-1]
		} else {
			turn = assistantDetails(events)
		}
	}
	if fallbackText != "" && len(turn.Texts) == 0 {
		turn.Texts = append(turn.Texts, fallbackText)
	}
	if sidecar != "" && (turn.Input != 0 || turn.Output != 0 || turn.CacheCreate != 0 || turn.CacheRead != 0) {
		appendSidecar(sidecar, turn, session, fallbackTS, auto)
	}
	summary := redact.Redact(Recap(turn.Texts))
	meta := redact.Redact(MetaLine(turn, true))
	body := redact.Redact(Sample(turn.Texts))
	if !codex { // append a bounded digest of what this turn's tools returned
		if d := redact.Redact(ToolResultsSummary(events)); d != "" {
			if body != "" {
				body += "\n\n"
			}
			body += ToolResultsMarker + "\n" + d
		}
	}
	if summary == "" && meta == "" && body == "" {
		return ""
	}
	return summary + "\n" + meta + "\n" + body
}
