package transcript

// Python-compatible string primitives. The legacy devbrain_lib.py did all its
// text handling with Python str semantics — unicode whitespace in strip()/\s,
// slicing by code point, json.dumps ensure_ascii escaping. These helpers
// reproduce those semantics so outputs stay byte-identical to the Python port
// source.

import (
	"fmt"
	"strings"
	"unicode"
)

// pySpace matches Python str.isspace() / re \s (str mode): unicode whitespace
// plus the ASCII separators 0x1c-0x1f that Go's unicode.IsSpace omits.
func pySpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}

func pyStrip(s string) string  { return strings.TrimFunc(s, pySpace) }
func pyLStrip(s string) string { return strings.TrimLeftFunc(s, pySpace) }

// splitLines mirrors Python str.splitlines(): \n, \r, \r\n, \v, \f, 0x1c-0x1e,
// 0x85, U+2028, U+2029 are line boundaries; no trailing empty line.
func splitLines(s string) []string {
	isBreak := func(r rune) bool {
		switch r {
		case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			return true
		}
		return false
	}
	var out []string
	rs := []rune(s)
	start := 0
	for i := 0; i < len(rs); i++ {
		if !isBreak(rs[i]) {
			continue
		}
		out = append(out, string(rs[start:i]))
		if rs[i] == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

// trimLeadingClass is re.sub(r"^[<extra>\s]+", "", s): strip the leading run
// of whitespace and the given marker runes.
func trimLeadingClass(s, extra string) string {
	return strings.TrimLeftFunc(s, func(r rune) bool {
		return pySpace(r) || strings.ContainsRune(extra, r)
	})
}

// collapseWS is re.sub(r"\s+", " ", s): every maximal whitespace run becomes
// one space (leading/trailing runs included — callers strip separately).
func collapseWS(s string) string {
	var b strings.Builder
	inWS := false
	for _, r := range s {
		if pySpace(r) {
			if !inWS {
				b.WriteByte(' ')
			}
			inWS = true
			continue
		}
		inWS = false
		b.WriteRune(r)
	}
	return b.String()
}

// truncRunes is Python s[:n] — slice by code point, not byte.
func truncRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

// pyJSONString renders s exactly like Python json.dumps (default
// ensure_ascii=True): short escapes for the usual controls, \u00xx for other
// controls, \uXXXX (surrogate pairs above the BMP) for every non-ASCII rune.
func pyJSONString(s string) string {
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
			switch {
			case r >= 0x20 && r <= 0x7e:
				b.WriteRune(r)
			case r > 0xffff:
				v := r - 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(v>>10), 0xdc00+(v&0x3ff))
			default:
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
