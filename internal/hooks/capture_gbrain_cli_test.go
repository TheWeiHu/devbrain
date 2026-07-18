package hooks_test

// Go-native port of scripts/test-capture-gbrain.sh: PostToolUse payload tests
// for `devbrain hook gbrain` — mode detection, routing, redaction, slug parsing.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// gbrainPayload builds a Bash PostToolUse JSON payload.
func gbrainPayload(cmd, stdout, toolName string) string {
	b, _ := json.Marshal(map[string]any{
		"tool_name":     toolName,
		"cwd":           ".",
		"tool_input":    map[string]any{"command": cmd},
		"tool_response": map[string]any{"stdout": stdout},
	})
	return string(b)
}

// gbrainPayloadCWD builds a Bash PostToolUse payload with a specific cwd.
func gbrainPayloadCWD(cmd, stdout, cwd string) string {
	b, _ := json.Marshal(map[string]any{
		"tool_name":     "Bash",
		"cwd":           cwd,
		"tool_input":    map[string]any{"command": cmd},
		"tool_response": map[string]any{"stdout": stdout},
	})
	return string(b)
}

// fireGbrain sends the payload to hook gbrain. The harness has DEVBRAIN_PROJECT set.
func fireGbrain(h *clitest.Harness, cmd, stdout string) clitest.Result {
	return h.RunWith(clitest.RunOpts{
		Stdin: gbrainPayload(cmd, stdout, "Bash"),
	}, "hook", "gbrain")
}

// fireGbrainTool sends the payload with a specified tool name.
func fireGbrainTool(h *clitest.Harness, cmd, stdout, tool string) clitest.Result {
	return h.RunWith(clitest.RunOpts{
		Stdin: gbrainPayload(cmd, stdout, tool),
	}, "hook", "gbrain")
}

// logLines reads the gbrain log and returns all lines.
func gbrainLogLines(t *testing.T, h *clitest.Harness) []string {
	t.Helper()
	p := filepath.Join(h.Data, "projects", h.Project, "gbrain-queries.log")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// lastLine returns the last non-empty line of the log.
func gbrainLastLine(t *testing.T, h *clitest.Harness) string {
	t.Helper()
	lines := gbrainLogLines(t, h)
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// jfield extracts a JSON field from a single JSON object string.
func jfield(t *testing.T, jsonLine, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonLine), &m); err != nil {
		t.Fatalf("jfield: invalid JSON %q: %v", jsonLine, err)
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch tv := v.(type) {
	case string:
		return tv
	case float64:
		return fmt.Sprintf("%v", tv)
	case []any:
		strs := make([]string, len(tv))
		for i, item := range tv {
			strs[i] = fmt.Sprintf("%v", item)
		}
		return strings.Join(strs, ",")
	case bool:
		if tv {
			return "true"
		}
		return "false"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// jmodesCompact returns the modes field as compact JSON (e.g. `["query"]`).
func jmodesCompact(t *testing.T, jsonLine string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonLine), &m); err != nil {
		t.Fatalf("jmodesCompact: invalid JSON %q: %v", jsonLine, err)
	}
	b, _ := json.Marshal(m["modes"])
	return string(b)
}

// jslugJoin returns the slugs field joined by comma.
func jslugJoin(t *testing.T, jsonLine string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonLine), &m); err != nil {
		t.Fatalf("jslugJoin: invalid JSON %q: %v", jsonLine, err)
	}
	v, ok := m["slugs"]
	if !ok {
		return ""
	}
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return ""
	}
	strs := make([]string, len(arr))
	for i, item := range arr {
		strs[i] = fmt.Sprintf("%v", item)
	}
	return strings.Join(strs, ",")
}

func TestCaptureGbrain(t *testing.T) {
	h := clitest.New(t)

	hits2 := "[0.86] testproj/alpha -- first hit\n[0.40] testproj/beta -- second hit"

	// 1. A single query logs one line: modes, hits, slugs, project, cmd snippet.
	fireGbrain(h, `gbrain query "todo lifecycle"`, hits2)
	lines := gbrainLogLines(t, h)
	if len(lines) != 1 {
		t.Fatalf("one line written: got %d lines", len(lines))
	}
	line := lines[0]
	if got := jmodesCompact(t, line); got != `["query"]` {
		t.Errorf(`modes=[query]: got %q`, got)
	}
	if got := jfield(t, line, "hits"); got != "2" {
		t.Errorf("hits=2: got %q", got)
	}
	if got := jslugJoin(t, line); got != "testproj/alpha,testproj/beta" {
		t.Errorf("slugs parsed: got %q", got)
	}
	if got := jfield(t, line, "project"); got != "testproj" {
		t.Errorf("project key: got %q", got)
	}
	if cmd := jfield(t, line, "cmd"); !strings.Contains(cmd, "todo lifecycle") {
		t.Errorf("cmd snippet kept: got %q", cmd)
	}

	// 2. A non-Bash tool is ignored.
	fireGbrainTool(h, `gbrain query "ignored"`, hits2, "Read")
	if got := len(gbrainLogLines(t, h)); got != 1 {
		t.Errorf("non-Bash ignored: expected 1 line, got %d", got)
	}

	// 3. A Bash command with no gbrain is ignored.
	fireGbrain(h, "ls -la && echo hi", "stuff")
	if got := len(gbrainLogLines(t, h)); got != 1 {
		t.Errorf("no-gbrain ignored: expected 1 line, got %d", got)
	}

	// 4. A loop logs ONE line; the cmd snippet carries both topics; modes deduped.
	fireGbrain(h, `for q in "todo queue" "concurrency"; do gbrain search "$q"; done`, hits2)
	if got := len(gbrainLogLines(t, h)); got != 2 {
		t.Errorf("loop -> one line: expected 2 total, got %d", got)
	}
	loop := gbrainLastLine(t, h)
	if got := jmodesCompact(t, loop); got != `["search"]` {
		t.Errorf("loop modes=[search]: got %q", got)
	}
	cmd4 := jfield(t, loop, "cmd")
	if !strings.Contains(cmd4, "todo queue") || !strings.Contains(cmd4, "concurrency") {
		t.Errorf("loop cmd has topics: got %q", cmd4)
	}

	// 5. "gbrain <word>" inside a string is filtered by the whitelist.
	fireGbrain(h, `t="log gbrain queries"; gbrain search "$t"`, hits2)
	fp := gbrainLastLine(t, h)
	if got := jmodesCompact(t, fp); got != `["search"]` {
		t.Errorf("whitelist drops 'queries': got %q", got)
	}

	// 6. A filename containing gbrain (no real subcommand) logs NOTHING.
	fireGbrain(h, "cat README.md | head -1", "# devbrain")
	if got := len(gbrainLogLines(t, h)); got != 3 {
		t.Errorf("filename ref ignored: expected 3 lines, got %d", got)
	}

	// 7. Write subcommands are logged too; no result lines -> hits 0.
	fireGbrain(h, "printf hi | gbrain put theproj/page", "")
	w := gbrainLastLine(t, h)
	if got := jmodesCompact(t, w); got != `["put"]` {
		t.Errorf("put logged: got %q", got)
	}
	if got := jfield(t, w, "hits"); got != "0" {
		t.Errorf("put hits 0: got %q", got)
	}

	// 7b. A gbrain get that returns content is a HIT; slug recorded.
	fireGbrain(h, "gbrain get testproj/alpha --fuzzy", "# Alpha page\nsome real body content")
	g := gbrainLastLine(t, h)
	if got := jmodesCompact(t, g); got != `["get"]` {
		t.Errorf("get modes=[get]: got %q", got)
	}
	if got := jfield(t, g, "hits"); got != "1" {
		t.Errorf("get success hits=1: got %q", got)
	}
	if got := jslugJoin(t, g); got != "testproj/alpha" {
		t.Errorf("get slug recorded: got %q", got)
	}

	if got := jfield(t, g, "ok"); got != "true" {
		t.Errorf("get success ok=true: got %q", got)
	}

	// 7b-ii. ok is INDEPENDENT of hits: a get whose target can't be parsed off the
	// command still delivered a page, so it counts as useful context at hits 0.
	fireGbrain(h, `gbrain get "a;b<c"`, "# Alpha page\nsome real body content")
	gi := gbrainLastLine(t, h)
	if got := jfield(t, gi, "hits"); got != "0" {
		t.Errorf("unparseable get target hits=0: got %q", got)
	}
	if got := jfield(t, gi, "ok"); got != "true" {
		t.Errorf("unparseable get target still ok=true: got %q", got)
	}

	// 7c. A gbrain get that misses stays hits 0.
	fireGbrain(h, "gbrain get testproj/nope", "page_not_found: did you mean testproj/alpha?")
	gm := gbrainLastLine(t, h)
	if got := jfield(t, gm, "ok"); got != "false" {
		t.Errorf("get not-found ok=false: got %q", got)
	}
	if got := jfield(t, gm, "hits"); got != "0" {
		t.Errorf("get not-found hits 0: got %q", got)
	}
	if got := jslugJoin(t, gm); got != "" {
		t.Errorf("get not-found no slug: got %q", got)
	}

	// 7d. An option-only get (gbrain get --help) is not a page read: hits 0.
	fireGbrain(h, "gbrain get --help", "Usage: gbrain get <slug> [--fuzzy]")
	gh := gbrainLastLine(t, h)
	if got := jfield(t, gh, "hits"); got != "0" {
		t.Errorf("get --help hits 0: got %q", got)
	}
	if got := jslugJoin(t, gh); got != "" {
		t.Errorf("get --help no slug: got %q", got)
	}

	// 7d2. Option-only get with redirection: fd `2` must not be mistaken for a slug.
	fireGbrain(h, "gbrain get --help 2>&1", "Usage: gbrain get <slug>")
	ghr := gbrainLastLine(t, h)
	if got := jfield(t, ghr, "hits"); got != "0" {
		t.Errorf("get --help 2>&1 hits 0: got %q", got)
	}
	if got := jslugJoin(t, ghr); got != "" {
		t.Errorf("get --help 2>&1 no slug: got %q", got)
	}

	// 7d3. Option-only get chained before a real get: skip probe, credit real read.
	fireGbrain(h, "gbrain get --help; gbrain get testproj/alpha", "# Alpha\nbody")
	gd3 := gbrainLastLine(t, h)
	if got := jfield(t, gd3, "hits"); got != "1" {
		t.Errorf("probe-then-real get hits 1: got %q", got)
	}
	if got := jslugJoin(t, gd3); got != "testproj/alpha" {
		t.Errorf("probe-then-real get slug: got %q", got)
	}

	// 7d4. A get inside a QUOTED command substitution.
	fireGbrain(h, `echo "$(gbrain get testproj/alpha)"`, "# Alpha\nbody")
	gd4 := gbrainLastLine(t, h)
	if got := jfield(t, gd4, "hits"); got != "1" {
		t.Errorf("quoted cmd-subst get hits 1: got %q", got)
	}
	if got := jslugJoin(t, gd4); got != "testproj/alpha" {
		t.Errorf("quoted cmd-subst get slug: got %q", got)
	}

	// 7d5. A get chained or path-prefixed INSIDE a quoted substitution.
	fireGbrain(h, `echo "$(cd /tmp && gbrain get testproj/alpha)"`, "# Alpha\nbody")
	gd5 := gbrainLastLine(t, h)
	if got := jfield(t, gd5, "hits"); got != "1" {
		t.Errorf("chained-in-subst get hits 1: got %q", got)
	}
	if got := jslugJoin(t, gd5); got != "testproj/alpha" {
		t.Errorf("chained-in-subst get slug: got %q", got)
	}

	// 7e. A get whose slug is an unexpanded shell var ($page): credit hit, no slug.
	fireGbrain(h, `page=testproj/alpha; gbrain get "$page"`, "# Alpha page\nbody")
	gv := gbrainLastLine(t, h)
	if got := jfield(t, gv, "hits"); got != "1" {
		t.Errorf("get $var hits 1: got %q", got)
	}
	if got := jslugJoin(t, gv); got != "" {
		t.Errorf("get $var no slug: got %q", got)
	}

	// 7e2. A braced var (gbrain get "${page}"): credit hit, record no slug.
	fireGbrain(h, `page=testproj/alpha; gbrain get "${page}"`, "# Alpha page\nbody")
	gvb := gbrainLastLine(t, h)
	if got := jfield(t, gvb, "hits"); got != "1" {
		t.Errorf("get ${var} hits 1: got %q", got)
	}
	if got := jslugJoin(t, gvb); got != "" {
		t.Errorf("get ${var} no slug: got %q", got)
	}

	// 7f. "gbrain get X" INSIDE a search query string must not masquerade as a real get.
	fireGbrain(h, `gbrain search "why is gbrain get counted as a miss"`, "")
	gq := gbrainLastLine(t, h)
	if got := jslugJoin(t, gq); got != "" {
		t.Errorf("quoted 'gbrain get' no slug: got %q", got)
	}
	if got := jfield(t, gq, "hits"); got != "0" {
		t.Errorf("quoted 'gbrain get' hits 0: got %q", got)
	}

	// 7g. A real get chained after a search whose query mentions "gbrain get".
	fireGbrain(h, `gbrain search "gbrain get hits" && gbrain get testproj/alpha`, "# Alpha\nbody")
	gc := gbrainLastLine(t, h)
	if got := jfield(t, gc, "hits"); got != "1" {
		t.Errorf("chained real get hits 1: got %q", got)
	}
	if got := jslugJoin(t, gc); got != "testproj/alpha" {
		t.Errorf("chained real get slug: got %q", got)
	}

	// 7h. A get wrapped in a command substitution / subshell.
	fireGbrain(h, "body=$(gbrain get testproj/alpha)", "# Alpha\nbody")
	gs := gbrainLastLine(t, h)
	if got := jfield(t, gs, "hits"); got != "1" {
		t.Errorf("cmd-subst get hits 1: got %q", got)
	}
	if got := jslugJoin(t, gs); got != "testproj/alpha" {
		t.Errorf("cmd-subst get slug: got %q", got)
	}

	// 7i. A real get chained with a heredoc whose body has a stray apostrophe.
	fireGbrain(h, "gbrain get testproj/alpha\ncat <<EOF\ndon't break\nEOF", "# Alpha\nbody")
	ghd := gbrainLastLine(t, h)
	if got := jfield(t, ghd, "hits"); got != "1" {
		t.Errorf("heredoc-chained get hits 1: got %q", got)
	}
	if got := jslugJoin(t, ghd); got != "testproj/alpha" {
		t.Errorf("heredoc-chained get slug: got %q", got)
	}

	// 7j. An ANSI-C quoted apostrophe on the SAME line as the get.
	fireGbrain(h, "printf $'don\\'t'; gbrain get testproj/alpha", "# Alpha\nbody")
	gan := gbrainLastLine(t, h)
	if got := jfield(t, gan, "hits"); got != "1" {
		t.Errorf("ansi-c same-line get hits 1: got %q", got)
	}
	if got := jslugJoin(t, gan); got != "testproj/alpha" {
		t.Errorf("ansi-c same-line get slug: got %q", got)
	}

	// 8. Path-prefixed binary still matches.
	fireGbrain(h, `/home/u/.bun/bin/gbrain ask "deep question"`, hits2)
	if got := jmodesCompact(t, gbrainLastLine(t, h)); got != `["ask"]` {
		t.Errorf("path-prefixed matched: got %q", got)
	}

	// 9. A secret in the command is redacted out of the logged cmd snippet.
	fireGbrain(h, `gbrain search "key sk-abcdefghijklmnopqrstuvwxyz0123"`, "")
	c9 := jfield(t, gbrainLastLine(t, h), "cmd")
	if !strings.Contains(c9, "REDACTED") || strings.Contains(c9, "sk-abcdefghijklmnopqrstuvwxyz0123") {
		t.Errorf("cmd snippet redacted: got %q", c9)
	}

	// 10. Inline `cd <repo>` attributes the call to the repo it actually queried,
	//     not the (non-repo) payload cwd. Drop the project override.
	// Session working dirs live OUTSIDE the data store (a real user's cwd is a
	// project checkout, never inside ~/devbrain-data); nesting them under h.Data
	// would trip InDataRepo's refusal.
	work := t.TempDir()
	repo := filepath.Join(work, "acme-widget")
	parent := filepath.Join(work, "no-repo-parent")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	clitest.Git(t, "", "init", "-q", repo)
	clitest.Git(t, repo, "remote", "add", "origin", "https://github.com/Acme/Widget.git")

	miscLog := filepath.Join(h.Data, "projects", "miscellaneous", "gbrain-queries.log")
	acmeLog := filepath.Join(h.Data, "projects", "acme__widget", "gbrain-queries.log")

	// Remove DEVBRAIN_PROJECT so routing uses git/cwd.
	fireNoProj := func(cmd, stdout, cwd string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{
			"tool_name":     "Bash",
			"cwd":           cwd,
			"tool_input":    map[string]any{"command": cmd},
			"tool_response": map[string]any{"stdout": stdout},
		})
		h.RunWith(clitest.RunOpts{
			Stdin: string(b),
			Env:   map[string]string{"DEVBRAIN_PROJECT": ""},
		}, "hook", "gbrain")
	}

	fireNoProj("cd "+repo+` && gbrain search "x"`, hits2, parent)
	if _, err := os.Stat(acmeLog); err != nil {
		t.Errorf("inline cd -> hosted repo, not miscellaneous: acmeLog missing: %v", err)
	}
	acmeContent := clitest.Read(t, acmeLog)
	if got := jfield(t, lastJSONLine(acmeContent), "project"); got != "acme__widget" {
		t.Errorf("inline cd -> hosted repo: project %q, want acme__widget", got)
	}
	if _, err := os.Stat(miscLog); err == nil {
		t.Error("miscellaneous NOT written: miscLog exists")
	}

	// var+subshell cd -> hosted repo (the nightshift pattern).
	fireNoProj(`main="`+repo+`" (cd "$main" && gbrain get acme__widget/page)`, "", parent)
	acmeContent2 := clitest.Read(t, acmeLog)
	if got := jfield(t, lastJSONLine(acmeContent2), "project"); got != "acme__widget" {
		t.Errorf("var+subshell cd -> hosted repo: project %q", got)
	}

	// cd to a non-repo dir falls back to payload cwd identity (miscellaneous here).
	fireNoProj("cd "+parent+` && gbrain search "y"`, hits2, parent)
	if _, err := os.Stat(miscLog); err != nil {
		t.Errorf("cd non-repo -> falls back to cwd: miscLog missing: %v", err)
	}

	// 13b. A command that only MENTIONS gbrain but runs no real subcommand must
	//      touch nothing — no empty projects/<repo>/ folder.
	newRepo := filepath.Join(work, "zeta-repo")
	os.MkdirAll(newRepo, 0o755)
	clitest.Git(t, "", "init", "-q", newRepo)
	clitest.Git(t, newRepo, "remote", "add", "origin", "https://github.com/Zeta/Repo.git")
	fireNoProj("cd "+newRepo+" && cat gbrain-notes.md", "some notes", parent)
	if _, err := os.Stat(filepath.Join(h.Data, "projects", "zeta__repo")); err == nil {
		t.Error("mention-only cd creates no folder: zeta__repo folder was created")
	}

	// 11. Slug prefix wins outright: no cd, cwd is non-repo parent, output names owner__repo.
	ownedHits := "[0.91] beta__gizmo/page-one -- hit\n[0.40] beta__gizmo/page-two -- hit"
	fireNoProj(`gbrain search "z"`, ownedHits, parent)
	gizmoLog := filepath.Join(h.Data, "projects", "beta__gizmo", "gbrain-queries.log")
	if _, err := os.Stat(gizmoLog); err != nil {
		t.Errorf("slug prefix routes (no cd needed): gizmoLog missing: %v", err)
	}
	if got := jfield(t, lastJSONLine(clitest.Read(t, gizmoLog)), "project"); got != "beta__gizmo" {
		t.Errorf("slug prefix routes: project %q", got)
	}

	// 12. Slug beats an inline cd that points elsewhere.
	fireNoProj("cd "+repo+` && gbrain search "z"`, ownedHits, parent)
	if got := jfield(t, lastJSONLine(clitest.Read(t, gizmoLog)), "project"); got != "beta__gizmo" {
		t.Errorf("slug beats cd target: project %q", got)
	}

	// 13. A slug-less line does NOT hijack routing; cd/cwd still decide.
	noSlugHits := "[0.91] localpage -- no owner prefix"
	fireNoProj("cd "+repo+` && gbrain search "q"`, noSlugHits, parent)
	if got := jfield(t, lastJSONLine(clitest.Read(t, acmeLog)), "project"); got != "acme__widget" {
		t.Errorf("slug-less output ignored -> cd wins: project %q", got)
	}
}

// lastJSONLine returns the last non-empty line of a multi-line string.
func lastJSONLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] != "" {
			return lines[i]
		}
	}
	return ""
}
