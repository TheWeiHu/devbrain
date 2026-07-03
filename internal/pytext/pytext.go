// Package pytext holds the Python-compatible string primitives shared by the
// packages that reproduce the retired python port's text handling. The legacy
// devbrain did all text work with Python str semantics — unicode whitespace in
// strip()/\s, code-point slicing, str.splitlines() line boundaries — and the
// goldens under testdata/golden pin that behavior as an immutable spec. These
// helpers keep the Go output byte-identical. Previously each consumer carried
// its own copy (deliberately, so parallel rewrite phases didn't contend); the
// logic now lives here once and the consumers alias to it.
package pytext

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

// Space matches Python str.isspace() / re \s (str mode): unicode whitespace
// plus the ASCII separators 0x1c-0x1f that Go's unicode.IsSpace omits.
func Space(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}

// Strip is Python str.strip() over Space.
func Strip(s string) string { return strings.TrimFunc(s, Space) }

// LStrip is Python str.lstrip() over Space.
func LStrip(s string) string { return strings.TrimLeftFunc(s, Space) }

// IsLineBreak reports the line boundaries Python str.splitlines() honors: \n,
// \r, \v, \f, 0x1c-0x1e, 0x85, U+2028, U+2029.
func IsLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// Int is Python int(str): trimmed integer string, else error.
func Int(s string) (int, error) {
	s = Strip(s)
	if s == "" {
		return 0, errors.New("invalid literal")
	}
	return strconv.Atoi(s)
}

// SplitLines mirrors Python str.splitlines(): splits on IsLineBreak (\r\n as
// one boundary) with no trailing empty line.
func SplitLines(s string) []string {
	var out []string
	var cur []rune
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if IsLineBreak(r) {
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
