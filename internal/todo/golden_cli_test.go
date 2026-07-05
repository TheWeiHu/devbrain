package todo_test

// TestTodoGolden is the Go port of scripts/test-todo-golden.sh.
// It replays a fixed verb sequence against the built binary and diffs the
// normalised CLI output + on-disk task tree against testdata/golden/ byte-for-byte.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// gldTSRE matches ISO-8601 UTC timestamps produced by the binary.
var gldTSRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)

// gldNormTS replaces all timestamps with <TS>.
func gldNormTS(s string) string { return gldTSRE.ReplaceAllString(s, "<TS>") }

// gldNormWho additionally replaces "claimed_by: <anything>" with the placeholder
// used in the tree golden files (tree normalization from the bash script).
var gldWhoRE = regexp.MustCompile(`(?m)^claimed_by: .+`)

func gldNormTree(s string) string {
	s = gldNormTS(s)
	s = gldWhoRE.ReplaceAllString(s, "claimed_by: <WHO>")
	return s
}

func TestTodoGolden(t *testing.T) {
	root := clitest.Root(t)
	goldDir := filepath.Join(root, "testdata", "golden")
	goldCLI := filepath.Join(goldDir, "todo-cli-output.txt")
	goldTree := filepath.Join(goldDir, "todo-tree")

	// Mirror the script's skip guard.
	if _, err := os.Stat(goldCLI); err != nil {
		t.Skip("todo golden missing: testdata/golden/todo-cli-output.txt")
	}
	if _, err := os.Stat(goldTree); err != nil {
		t.Skip("todo golden missing: testdata/golden/todo-tree/")
	}

	h := clitest.New(t)
	h.Project = "fix__demo"

	// Build the PR-state stub: args containing "81" → MERGED, else OPEN.
	prstub := filepath.Join(h.Data, "prstub")
	clitest.WriteExec(t, prstub, "#!/bin/sh\ncase \"$1\" in *81*) echo MERGED;; *) echo OPEN;; esac\n")
	h.Env["DEVBRAIN_PR_STATE_CMD"] = prstub

	// gldTodo runs `devbrain todo <args>` capturing combined stdout+stderr,
	// appends a normalised "--- <label> (rc=N)\n<output>\n" block to buf, and
	// returns the raw result for ad-hoc assertions.
	var buf strings.Builder
	gldStep := func(label string, args ...string) clitest.Result {
		r := h.Run(append([]string{"todo"}, args...)...)
		combined := r.Stdout + r.Stderr
		// The bash script captures output via command substitution, which strips
		// trailing newlines. Mirror that: trim trailing newlines before formatting.
		// Then printf adds exactly one trailing newline.
		norm := gldNormTS(strings.TrimRight(combined, "\n"))
		fmt.Fprintf(&buf, "--- %s (rc=%d)\n%s\n", label, r.Code, norm)
		return r
	}

	gldStep("add1", "add", "Add retry queue", "-p", "80", "-b", "Failed jobs should retry with backoff.")
	gldStep("add2", "add", "Fix Mobile FOOTER overlap!!", "-p", "5")
	gldStep("add3", "add", "Third task", "-b", "body only")
	gldStep("list-open", "list")
	gldStep("next", "next")
	gldStep("claim2", "claim", "0002-fix-mobile-footer-overlap")
	gldStep("claim2-again", "claim", "0002-fix-mobile-footer-overlap")
	gldStep("review1", "review", "0001-add-retry-queue", "https://github.com/fix/demo/pull/81")
	gldStep("hold3", "hold", "0003-third-task", "waiting on design")
	gldStep("note3", "note", "0003-third-task", "gate failed twice")
	gldStep("list-all", "list", "all")
	gldStep("prio3", "prio", "0003-third-task", "99")
	gldStep("edit3", "edit", "0003-third-task", "-t", "Third task (renamed)", "-b", "new body line")
	gldStep("approve3", "approve", "0003-third-task")
	gldStep("done2-guard", "done", "0002-fix-mobile-footer-overlap")
	gldStep("done2", "done", "0002-fix-mobile-footer-overlap", "--force")
	gldStep("release2-done", "release", "0002-fix-mobile-footer-overlap")
	gldStep("selfheal", "self-heal", "open", "taken", "review")
	gldStep("reopen2", "reopen", "0002-fix-mobile-footer-overlap", "verified absent")
	gldStep("list-final", "list", "all")
	gldStep("show1", "show", "0001-add-retry-queue")

	// The context step is written manually in the bash script (no tstep wrapper):
	//   printf '--- context3 (rc=0)\n' >> "$TOUT"
	//   printf '...' | devbrain todo context 0003-third-task >> "$TOUT"
	// So the header is always rc=0 and output is not wrapped — just appended raw.
	buf.WriteString("--- context3 (rc=0)\n")
	ctxR := h.RunWith(
		clitest.RunOpts{Stdin: "Synthesized context from the brain.\nSecond line.\n"},
		"todo", "context", "0003-third-task",
	)
	buf.WriteString(gldNormTS(ctxR.Stdout))

	gotCLI := buf.String()
	wantCLI := clitest.Read(t, goldCLI)
	if gotCLI != wantCLI {
		t.Errorf("CLI output mismatch:\n--- want ---\n%s\n--- got ---\n%s\n--- diff (want vs got) ---\n%s",
			wantCLI, gotCLI, gldUnifiedDiff(wantCLI, gotCLI))
	}

	// Task-tree comparison: normalise each .md file in the live todo dir and
	// diff byte-for-byte against the corresponding file in testdata/golden/todo-tree/.
	todoDir := filepath.Join(h.Data, "projects", h.Project, "todo")
	liveFiles := clitest.Find(t, todoDir, "*.md")
	goldenFiles := clitest.Find(t, goldTree, "*.md")

	// Build maps keyed by basename.
	liveMap := make(map[string]string, len(liveFiles))
	for _, p := range liveFiles {
		liveMap[filepath.Base(p)] = gldNormTree(clitest.Read(t, p))
	}
	goldenMap := make(map[string]string, len(goldenFiles))
	for _, p := range goldenFiles {
		goldenMap[filepath.Base(p)] = clitest.Read(t, p)
	}

	// Check that every golden file is present and matches.
	for name, want := range goldenMap {
		got, ok := liveMap[name]
		if !ok {
			t.Errorf("task tree: missing file %s", name)
			continue
		}
		if got != want {
			t.Errorf("task tree: %s mismatch:\n--- want ---\n%s\n--- got ---\n%s\n--- diff ---\n%s",
				name, want, got, gldUnifiedDiff(want, got))
		}
	}
	// Check for unexpected extra files.
	for name := range liveMap {
		if _, ok := goldenMap[name]; !ok {
			t.Errorf("task tree: unexpected extra file %s", name)
		}
	}
}

// gldUnifiedDiff produces a simple line-by-line diff (want vs got) for test output.
func gldUnifiedDiff(want, got string) string {
	wlines := strings.Split(want, "\n")
	glines := strings.Split(got, "\n")
	var sb strings.Builder
	max := len(wlines)
	if len(glines) > max {
		max = len(glines)
	}
	for i := 0; i < max; i++ {
		var w, g string
		if i < len(wlines) {
			w = wlines[i]
		}
		if i < len(glines) {
			g = glines[i]
		}
		if w != g {
			fmt.Fprintf(&sb, "line %d:\n  want: %q\n   got: %q\n", i+1, w, g)
		}
	}
	return sb.String()
}
