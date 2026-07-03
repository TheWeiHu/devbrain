package plan

import (
	"regexp"
	"strings"
)

// fence.go — the pure half of fixed-set (--only) machinery: `todo list`
// output parsing and selection normalization/resolution/membership. The
// stateful fence itself (park/release, landed.tsv, verify) stays on Orch in
// the root package's fence.go.

var (
	// ids come from the FIRST column of `list` (the id field), not the title,
	// so a task whose title happens to contain an NNNN-word pattern can't be
	// mistaken for a task id.
	listIDRe       = regexp.MustCompile(`^[ \t]*\[[^\]]*\][ \t]+([0-9]{4}-[a-z0-9-]+)`)
	listStatusIDRe = regexp.MustCompile(`^[ \t]*\[[^\]]*\][ \t]+([a-z]+)[ \t]+([0-9]{4}-[a-z0-9-]+)`)
	wsRe           = regexp.MustCompile(`[ \t\r\n\v\f]`)
)

// ListIDs extracts task ids from `todo list` (open) output.
func ListIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if m := listIDRe.FindStringSubmatch(line); m != nil {
			ids = append(ids, m[1])
		}
	}
	return ids
}

// ListStatusIDs extracts (status, id) pairs from `todo list <status>|all`.
func ListStatusIDs(out string) [][2]string {
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

// MatchRow finds the first (status, id) row whose id equals the token or
// shares its leading 4-digit number (the awk `$2==t || substr($2,1,4)==num`).
func MatchRow(rows [][2]string, tok string) (status, id string) {
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
