// The read-side scanners behind /api/prompts, /api/gbrain and /api/tokens —
// ports of scan_prompts/classify/gbrain_queries/token_usage/project_repo in
// the retired queue.py.
package queue

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
		if date < cutoff || (project != "" && proj != project) {
			continue
		}
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
	sort.SliceStable(out, func(a, b int) bool { return out[a].DT < out[b].DT })
	return out
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
			out = append(out, &GBQuery{
				TS: ts, Date: truncStr(ts, 10), P: proj, Read: read,
				Modes: modes, Hits: hits, Slugs: slugs, Q: topic, Target: target,
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

// TokenUsage reads every project's tokens.jsonl, deduped on (session, ts) so
// a re-run or a sync can't double-count (token_usage). Pricing-agnostic —
// the model id flows through untouched.
func (q *Queue) TokenUsage(days int, project string) []*TokenRec {
	cutoff := q.cutoffDate(days)
	out := []*TokenRec{}
	seen := map[string]bool{}
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
			key := dedupKey(e["session"], ts)
			if seen[key] {
				continue
			}
			seen[key] = true
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
			out = append(out, &TokenRec{
				TS: ts, Date: truncStr(ts, 10), P: proj,
				Model: orEmpty("model"), Session: orEmpty("session"),
				In: orZero("in"), Out: orZero("out"),
				CC: orZero("cache_create"), CR: orZero("cache_read"),
				Auto: pyTruthy(e["auto"]),
			})
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
