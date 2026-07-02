package nightshift

import (
	"os"
	"regexp"
	"strings"
)

// ciscope.go — port of the orchestrator's ci_scope_unsafe awk state machine.
// CI must fire only on `main`, never on the per-task PRs the fleet opens into
// `nightshift`. A workflow is unsafe if its `pull_request` trigger isn't
// scoped away from nightshift: bare `pull_request:`, inline `on: pull_request`,
// flow-list `[push, pull_request]`, block-list `- pull_request`, or a
// `branches:` filter that includes `nightshift`. Warn-only — never rewrites.

var (
	ciTopOnRe     = regexp.MustCompile(`^on[ \t]*:`)
	ciBlockPRRe   = regexp.MustCompile(`^-[ \t]*pull_request([ \t]|$)`)
	ciPRKeyRe     = regexp.MustCompile(`^pull_request[ \t]*:`)
	ciBranchKeyRe = regexp.MustCompile(`^branches[ \t]*:`)
	ciListItemRe  = regexp.MustCompile(`^-[ \t]`)
)

// CIScopeUnsafe reports whether the workflow file WOULD run CI on the fleet's
// per-task PRs into nightshift. A missing file is safe (bash `return 1`).
func CIScopeUnsafe(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return ciScopeUnsafeYAML(string(b))
}

// ciScopeUnsafeYAML is the pure state machine over the workflow text.
func ciScopeUnsafeYAML(text string) bool {
	inOn, inPR := false, false
	prIndent := -1
	haveBranches := false
	branches := ""
	unsafe := false

	finalize := func() {
		if unsafe {
			return
		}
		if !haveBranches { // bare pull_request: → all PRs
			unsafe = true
			return
		}
		if strings.Contains(branches, "nightshift") { // explicitly includes our base
			unsafe = true
		}
	}

	for _, raw := range strings.Split(text, "\n") {
		if i := strings.IndexByte(raw, '#'); i >= 0 { // strip comments
			raw = raw[:i]
		}
		if strings.TrimSpace(raw) == "" { // skip blank
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		content := strings.TrimLeft(raw, " \t")
		if indent == 0 { // top-level key
			inOn = ciTopOnRe.MatchString(content)
			if inOn {
				rest := ciTopOnRe.ReplaceAllString(content, "")
				rest = strings.TrimLeft(rest, " \t")
				if strings.Contains(rest, "pull_request") { // inline string / flow-list
					unsafe = true
				}
			} else {
				if inPR {
					finalize()
				}
				inPR, prIndent = false, -1
			}
			continue
		}
		if !inOn {
			continue
		}
		if !inPR && ciBlockPRRe.MatchString(content) { // on: block-list item
			unsafe = true
			continue
		}
		if inPR && indent <= prIndent {
			finalize()
			inPR, prIndent = false, -1
		}
		if ciPRKeyRe.MatchString(content) {
			inPR, prIndent = true, indent
			haveBranches, branches = false, ""
			continue
		}
		if inPR {
			if ciBranchKeyRe.MatchString(content) {
				haveBranches = true
				rest := ciBranchKeyRe.ReplaceAllString(content, "")
				rest = strings.TrimLeft(rest, " \t")
				branches += " " + rest
			} else if ciListItemRe.MatchString(content) {
				branches += " " + ciListItemRe.ReplaceAllString(content, "")
			}
		}
	}
	if inPR {
		finalize()
	}
	return unsafe
}
