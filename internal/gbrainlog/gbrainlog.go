// Package gbrainlog parses gbrain invocations out of captured shell commands
// and renders the per-query log record. It is the Go port of the gbrain
// section of the legacy hooks/devbrain_lib.py (gbrain_modes, gbrain_get_target,
// gbrain_record and their _gb_* helpers); the record contract is pinned
// byte-for-byte by testdata/golden/gbrain-record.jsonl.
package gbrainlog

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/TheWeiHu/devbrain/internal/pytext"
	"github.com/TheWeiHu/devbrain/internal/redact"
)

var gbWhitelist = map[string]bool{
	"query": true, "search": true, "ask": true, "get": true, "put": true,
	"delete": true, "list": true, "tag": true, "link": true, "embed": true,
	"sync": true, "import": true, "export": true,
}

const gbPunct = "();<>|&`"

var (
	modeRe = regexp.MustCompile(`gbrain\s+([a-z][a-z-]*)`)
	// \A..\z reproduces Python re.fullmatch (Go's bare $ also matches before a
	// trailing newline in some engines; \z is exact).
	gbSlugRe = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._/-]*\z`)
	hitRe    = regexp.MustCompile(`^\[[0-9.]+\]`)
	slugRe   = regexp.MustCompile(`^\[[0-9.]+\]\s+(\S+)\s+--`)
	// Python's \s on str is Unicode-aware; Go's is ASCII, so spell out the
	// extra Python whitespace (\v, file separators, NEL, category Z).
	wsRe = regexp.MustCompile(`[\s\x0B\x1C-\x1F\x85\p{Z}]+`)
)

// Modes returns the whitelisted gbrain subcommands actually invoked in cmd,
// deduped in first-seen order (gbrain_modes). A verb only counts when `gbrain`
// and the verb are adjacent shell tokens: a "gbrain query" buried in a commit
// message body (git commit -F - <<EOF … EOF) or inside a quoted argument
// (codex review "… gbrain query …") is prose, not an invocation, and must not
// be counted — otherwise those phantom records land in the hit-rate denominator
// with hits=0 and flatten the dashboard's Brain Hit line to 0% on quiet days.
// Heredoc bodies are masked first; gbTok already keeps quoted spans intact so a
// verb inside "…" never surfaces as a bare token.
func Modes(cmd string) []string {
	masked := maskHeredocs(cmd)
	toks, ok := gbTok(masked)
	if !ok {
		// Unbalanced quote/escape: fall back to the substring scan on the
		// masked text so a genuine invocation is not silently dropped.
		var modes []string
		for _, m := range modeRe.FindAllStringSubmatch(masked, -1) {
			if sub := m[1]; gbWhitelist[sub] && !contains(modes, sub) {
				modes = append(modes, sub)
			}
		}
		return modes
	}
	var modes []string
	scanModes(toks, &modes)
	// Recover verbs hidden in a $( … ) / ` … ` substitution, which gbTok keeps
	// inside one (often quoted) token — e.g. RESULT="$(gbrain get …)". Mirrors
	// GetTarget's substitution recovery.
	subst := "$("
	replacer := strings.NewReplacer(subst, " ", "(", " ", ")", " ", "`", " ")
	for _, t := range toks {
		if !strings.Contains(t, subst) && !strings.Contains(t, "`") {
			continue
		}
		if inner, iok := gbTok(replacer.Replace(t)); iok {
			scanModes(inner, &modes)
		}
	}
	return modes
}

// scanModes appends whitelisted verbs that follow a `gbrain` token to modes.
func scanModes(toks []string, modes *[]string) {
	for i, t := range toks {
		if i+1 >= len(toks) || lastSegment(t) != "gbrain" {
			continue
		}
		if sub := toks[i+1]; gbWhitelist[sub] && !contains(*modes, sub) {
			*modes = append(*modes, sub)
		}
	}
}

// maskHeredocs blanks the body of every here-document (<<[-] DELIM … DELIM) in
// cmd, replacing body characters with spaces while preserving newlines so any
// later heredoc still lines up. The body of `git commit -F - <<'EOF' …` is a
// commit message, not shell — masking it stops a "gbrain query" mentioned there
// from registering as a real query. Here-strings (<<<) and bit-shifts carry no
// delimiter word and are left untouched.
func maskHeredocs(cmd string) string {
	if !strings.Contains(cmd, "<<") {
		return cmd
	}
	r := []rune(cmd)
	n := len(r)
	out := make([]rune, n)
	copy(out, r)
	for i := 0; i+1 < n; {
		if r[i] != '<' || r[i+1] != '<' {
			i++
			continue
		}
		j := i + 2
		dash := false
		if j < n && r[j] == '-' {
			dash, j = true, j+1
		}
		for j < n && (r[j] == ' ' || r[j] == '\t') {
			j++
		}
		var quote rune
		if j < n && (r[j] == '\'' || r[j] == '"') {
			quote, j = r[j], j+1
		}
		ds := j
		for j < n && isDelimRune(r[j]) {
			j++
		}
		delim := string(r[ds:j])
		if quote != 0 && j < n && r[j] == quote {
			j++
		}
		if delim == "" { // <<<, << as bit-shift, or a var delimiter — not a heredoc
			i += 2
			continue
		}
		for j < n && r[j] != '\n' { // rest of the opener line
			j++
		}
		bodyStart, end := j, n
		for k := j; k < n; {
			ls := k + 1
			le := ls
			for le < n && r[le] != '\n' {
				le++
			}
			line := string(r[ls:le])
			if dash {
				line = strings.TrimLeft(line, "\t")
			}
			if line == delim {
				end = le
				break
			}
			k = le
		}
		for x := bodyStart; x < end; x++ {
			if out[x] != '\n' {
				out[x] = ' '
			}
		}
		i = end
	}
	return string(out)
}

// isDelimRune reports whether r may appear in a bare heredoc delimiter word.
func isDelimRune(r rune) bool {
	return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// gbPageArg finds the first plausible page argument in a token sequence
// (_gb_page_arg): flags and bare numbers are skipped, a $VAR is returned
// as-is, and any shell-meta character aborts with "".
func gbPageArg(seq []string) string {
	for _, t := range seq {
		if t == "" || strings.HasPrefix(t, "-") || isDigits(t) {
			continue
		}
		if strings.HasPrefix(t, "$") {
			return t
		}
		if strings.ContainsAny(t, "<>&|;(){}") {
			return ""
		}
		return t
	}
	return ""
}

// isDigits mirrors Python str.isdigit for the shapes that occur in shell
// commands (Nd digits; Python additionally accepts super/subscripts).
func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// gbTok ports _gb_tok: Python shlex.shlex(s, posix=True,
// punctuation_chars="();<>|&`") with whitespace_split=True and commenters="".
// Returns (nil, false) on an unterminated quote or trailing escape, where
// Python raises ValueError.
//
// Behavior pinned against CPython shlex (see the unit tests):
//   - whitespace splits tokens; adjacent quoted/unquoted segments merge;
//   - '...' is literal; inside "..." a backslash escapes only \ and ",
//     otherwise the backslash is kept; outside quotes it escapes any char;
//   - a run of punctuation chars is its own token ("&&", ");(", ...);
//   - a quoted empty string yields an empty token.
func gbTok(s string) ([]string, bool) {
	const whitespace = " \t\r\n"
	var (
		toks    []string
		tok     []rune
		quoted  bool
		state   rune = ' ' // ' ' space, 'a' word, 'c' punct run, '\''/'"' in-quote, 'e' escape
		escFrom rune       // state to return to after an escape ('a' or '"')
	)
	emit := func() {
		if len(tok) > 0 || quoted {
			toks = append(toks, string(tok))
		}
		tok = tok[:0]
		quoted = false
	}
	runes := []rune(s)
	for i := 0; i <= len(runes); i++ {
		if i == len(runes) { // end of input
			switch state {
			case '\'', '"', 'e':
				return nil, false // "No closing quotation" / "No escaped character"
			}
			emit()
			return toks, true
		}
		c := runes[i]
		switch state {
		case ' ':
			switch {
			case strings.ContainsRune(whitespace, c):
			case c == '\\':
				escFrom, state = 'a', 'e'
			case strings.ContainsRune(gbPunct, c):
				tok = append(tok, c)
				state = 'c'
			case c == '\'' || c == '"':
				state = c
			default: // whitespace_split: anything else starts a word
				tok = append(tok, c)
				state = 'a'
			}
		case '\'', '"':
			quoted = true
			switch {
			case c == state:
				state = 'a'
			case state == '"' && c == '\\': // escapedquotes: only inside "..."
				escFrom, state = '"', 'e'
			default:
				tok = append(tok, c)
			}
		case 'e':
			// Inside "..." only the quote or the escape char may be escaped;
			// any other char keeps its backslash.
			if escFrom == '"' && c != '\\' && c != '"' {
				tok = append(tok, '\\')
			}
			tok = append(tok, c)
			state = escFrom
		case 'a':
			switch {
			case strings.ContainsRune(whitespace, c):
				emit()
				state = ' '
			case c == '\'' || c == '"':
				state = c
			case c == '\\':
				escFrom, state = 'a', 'e'
			case strings.ContainsRune(gbPunct, c):
				emit()
				state = ' '
				i-- // reprocess (shlex pushback)
			default:
				tok = append(tok, c)
			}
		case 'c':
			switch {
			case strings.ContainsRune(whitespace, c):
				emit()
				state = ' '
			case strings.ContainsRune(gbPunct, c):
				tok = append(tok, c)
			default:
				emit()
				state = ' '
				i-- // reprocess
			}
		}
	}
	return toks, true // unreachable; loop always returns at end of input
}

// gbScan finds the page argument of the first successful `gbrain get` in a
// token stream (_gb_scan). The command word may be path-prefixed.
func gbScan(toks []string) string {
	for i, t := range toks {
		if i+1 < len(toks) && lastSegment(t) == "gbrain" && toks[i+1] == "get" {
			if target := gbPageArg(toks[i+2:]); target != "" {
				return target
			}
		}
	}
	return ""
}

func lastSegment(t string) string {
	if i := strings.LastIndex(t, "/"); i >= 0 {
		return t[i+1:]
	}
	return t
}

// GetTarget returns the best-effort page argument for a real `gbrain get`
// invocation in cmd (gbrain_get_target). With fallback, an unparseable line
// is retried with a crude split. Note: the dashboard side (scripts/queue.py
// gb_get_target) additionally requires a slash-containing slug shape; that
// filter belongs to the queue port, not here.
func GetTarget(cmd string, fallback bool) string {
	if cmd == "" || !strings.Contains(cmd, "gbrain get ") {
		return ""
	}
	subst := "$("
	replacer := strings.NewReplacer(subst, " ", "(", " ", ")", " ", "`", " ")
	for _, line := range splitLines(cmd) {
		target := ""
		if toks, ok := gbTok(line); ok {
			target = gbScan(toks)
			if target == "" {
				// Recover a get hidden inside $( ... ) or ` ... `.
				for _, t := range toks {
					if !strings.Contains(t, subst) && !strings.Contains(t, "`") {
						continue
					}
					if inner, iok := gbTok(replacer.Replace(t)); iok && len(inner) > 0 {
						if target = gbScan(inner); target != "" {
							break
						}
					}
				}
			}
		} else if fallback && strings.Contains(line, "gbrain get ") {
			rest := strings.Fields(strings.SplitN(line, "gbrain get ", 2)[1])
			stripped := make([]string, len(rest))
			for i, t := range rest {
				stripped[i] = strings.Trim(t, `"'();`)
			}
			target = gbPageArg(stripped)
		}
		if target != "" {
			return target
		}
	}
	return ""
}

// Record renders one gbrain query-log line for a captured command + output
// (gbrain_record), or "" when the command ran no whitelisted gbrain verb.
// auto marks a nightshift/autonomous session (its keyboard-vs-bot origin) so
// the dashboard can split typed from bot hit-/useful-rate. Emitted as a
// trailing "auto" key; readers default a missing key to false (typed).
// The output is byte-identical to Python json.dumps(..., ensure_ascii=False)
// with key order ts, project, cmd, modes, hits, slugs, auto.
func Record(cmd, out, project, ts string, auto bool) string {
	modes := Modes(cmd)
	if len(modes) == 0 {
		return ""
	}
	snippet := redact.Redact(strings.TrimSpace(wsRe.ReplaceAllString(cmd, " ")))
	if utf8.RuneCountInString(snippet) > 300 {
		snippet = string([]rune(snippet)[:300]) + "…"
	}
	var slugs []string
	hits := 0
	for _, ln := range splitLines(out) {
		if !hitRe.MatchString(ln) {
			continue
		}
		if m := slugRe.FindStringSubmatch(ln); m != nil {
			cs, ok := canonSlug(m[1])
			if !ok {
				continue // a /log/ transcript match — not a page, don't count it as a hit
			}
			hits++
			if !contains(slugs, cs) {
				slugs = append(slugs, cs)
			}
		} else {
			hits++ // a result line with no parseable slug still counts as a returned result
		}
	}
	if contains(modes, "get") && hits == 0 {
		low := strings.ToLower(out)
		missed := strings.TrimSpace(out) == "" || strings.Contains(low, "page_not_found") ||
			strings.Contains(low, "did you mean") || strings.Contains(low, "not found")
		if !missed {
			// Silent success (page body with no score lines): credit the get
			// and, when the target looks like a real slug, surface it.
			if target := GetTarget(cmd, true); target != "" {
				hits = 1
				if cs, ok := canonSlug(target); ok && gbSlugRe.MatchString(target) && !contains(slugs, cs) {
					slugs = append(slugs, cs)
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString(`{"ts": `)
	writePyString(&b, ts)
	b.WriteString(`, "project": `)
	writePyString(&b, project)
	b.WriteString(`, "cmd": `)
	writePyString(&b, snippet)
	b.WriteString(`, "modes": `)
	writePyStrings(&b, modes)
	b.WriteString(`, "hits": `)
	b.WriteString(strconv.Itoa(hits))
	b.WriteString(`, "slugs": `)
	writePyStrings(&b, slugs)
	b.WriteString(`, "auto": `)
	if auto {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	b.WriteByte('}')
	return b.String()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// canonSlug folds a gbrain result location to its canonical <project>/<page>
// slug and reports whether to keep it. devbrain's canonical slug is
// <project>/<page>, but a raw `gbrain import` of the data dir slugs pages by
// FILE PATH — projects/<project>/brain/<page> — and also indexes raw prompt
// logs as projects/<project>/log/<...>. So the same page shows up under two
// strings (splitting its count) and logs masquerade as pages. Strip the
// projects/ + /brain/ path noise so a page counts once, and drop /log/ matches
// — they're transcripts, not pages.
func canonSlug(s string) (string, bool) {
	if strings.Contains(s, "/log/") {
		return "", false
	}
	s = strings.TrimPrefix(s, "projects/")
	s = strings.Replace(s, "/brain/", "/", 1)
	// Drop a redundant <project>- prefix on the page, matching how rebuild and
	// /distill slug a file named <project>-<page>.md -> <project>/<page>.
	if i := strings.IndexByte(s, '/'); i >= 0 {
		proj, page := s[:i], s[i+1:]
		s = proj + "/" + strings.TrimPrefix(page, proj+"-")
	}
	return s, true
}

// splitLines mirrors Python str.splitlines(): splits on the full line-break
// set (\n, \r, \r\n, \v, \f, FS/GS/RS, NEL, LS, PS), no trailing empty line.
func splitLines(s string) []string { return pytext.SplitLines(s) }

// writePyString escapes exactly like Python json with ensure_ascii=False:
// short escapes for \" \\ \n \r \t \b \f, \u00XX for other control chars,
// everything else (including non-ASCII) raw.
func writePyString(b *strings.Builder, s string) {
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
				const hex = "0123456789abcdef"
				b.WriteString(`\u00`)
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// writePyStrings renders a string list like Python json.dumps: [] or
// ["a", "b"].
func writePyStrings(b *strings.Builder, xs []string) {
	b.WriteByte('[')
	for i, x := range xs {
		if i > 0 {
			b.WriteString(", ")
		}
		writePyString(b, x)
	}
	b.WriteByte(']')
}
