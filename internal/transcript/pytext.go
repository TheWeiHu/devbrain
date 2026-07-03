package transcript

// Python-compatible string primitives. The legacy devbrain_lib.py did all its
// text handling with Python str semantics — unicode whitespace in strip()/\s,
// slicing by code point, json.dumps ensure_ascii escaping. These helpers
// reproduce those semantics so outputs stay byte-identical to the Python port
// source.

import (
	"fmt"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/pytext"
)

// The core str primitives live in internal/pytext; these aliases keep the
// call sites terse. The helpers below are transcript-specific.
func pySpace(r rune) bool          { return pytext.Space(r) }
func pyStrip(s string) string      { return pytext.Strip(s) }
func pyLStrip(s string) string     { return pytext.LStrip(s) }
func splitLines(s string) []string { return pytext.SplitLines(s) }

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
