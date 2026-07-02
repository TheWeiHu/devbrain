// Python string-semantics helpers for the queue port. Kept local (duplicated
// from internal/transcript's unexported twins) so parallel rewrite phases
// don't contend on shared files.
package queue

import (
	"strings"
	"unicode"
)

// pySpace matches Python str.isspace(): unicode whitespace plus the
// \x1c-\x1f file separators.
func pySpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}

func pyStrip(s string) string  { return strings.TrimFunc(s, pySpace) }
func pyLStrip(s string) string { return strings.TrimLeftFunc(s, pySpace) }

// splitPyLines mirrors Python str.splitlines(): the full line-break set,
// no trailing empty line.
func splitPyLines(s string) []string {
	var out []string
	var cur []rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if isLineBreak(r) {
			out = append(out, string(cur))
			cur = cur[:0]
			if r == '\r' && i+1 < len(runes) && runes[i+1] == '\n' {
				i++
			}
			continue
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}
