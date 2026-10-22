package brain_test

// gbrain 0.51 (2026-09-17) made `put` refuse an existing page unless the caller
// sends the revision it read (exit 1, "revision_required"). devbrain's Markdown
// pages on disk are the source of truth and this wrapper is the index's only
// writer, so a page update must land anyway: the wrapper repeats the write with
// --force instead of surfacing a conflict that is never real for it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// revisionStub stands in for gbrain. Every call appends its argv to
// $STUB_LOG/argv; a put also saves its stdin as $STUB_LOG/stdin.<call#>. A put
// without --force answers the way gbrain 0.60 does for an existing page (JSON
// receipt on stdout, Error line on stderr, exit 1); with --force it succeeds.
// STUB_MODE=other-error fails the put differently so a test can prove that
// only revision_required earns a retry. Everything else (tag, delete, embed)
// succeeds silently.
const revisionStub = `#!/bin/sh
printf '%s\n' "$*" >> "$STUB_LOG/argv"
n=$(wc -l < "$STUB_LOG/argv" | tr -d ' ')
case "$1" in
  put)
    cat > "$STUB_LOG/stdin.$n"
    case "$*" in
      *--force*) echo "updated $2"; exit 0 ;;
    esac
    if [ "$STUB_MODE" = other-error ]; then
      echo 'Error [invalid_params]: slug has no project prefix' >&2
      exit 2
    fi
    printf '{"error":"revision_required","code":"revision_required","message":"The page already exists; an expected revision is required."}\n'
    echo 'Error [revision_required]: The page already exists; an expected revision is required.' >&2
    exit 1
    ;;
esac
exit 0
`

func newRevisionHarness(t *testing.T) (*clitest.Harness, string) {
	t.Helper()
	h := clitest.New(t)
	h.Project = "owner__alpha"
	stub := filepath.Join(t.TempDir(), "gbrain-revision-stub")
	clitest.WriteExec(t, stub, revisionStub)
	logDir := t.TempDir()
	h.Env["DEVBRAIN_GBRAIN"] = stub
	h.Env["STUB_LOG"] = logDir
	return h, logDir
}

func stubCalls(t *testing.T, logDir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, "argv"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

func stubStdin(t *testing.T, logDir string, call int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, fmt.Sprintf("stdin.%d", call)))
	if err != nil {
		t.Fatalf("stdin of gbrain call %d was not recorded: %v", call, err)
	}
	return string(data)
}

func TestBrainPutRetriesWithForceOnRevisionRequired(t *testing.T) {
	h, logDir := newRevisionHarness(t)
	page := "# Install\nUpdated on disk; the index must follow.\n"

	res := h.RunWith(clitest.RunOpts{Stdin: page}, "brain", "put", "owner__alpha/install")

	if res.Code != 0 {
		t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", res.Code, res.Stdout, res.Stderr)
	}
	calls := stubCalls(t, logDir)
	want := []string{"put owner__alpha/install", "put owner__alpha/install --force"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("gbrain calls = %q, want %q", calls, want)
	}
	if got := stubStdin(t, logDir, 2); got != page {
		t.Errorf("forced retry received %q as the page, want the same stdin as the first attempt", got)
	}
	if strings.Contains(res.Stdout+res.Stderr, "revision_required") {
		t.Errorf("the superseded conflict receipt leaked into the output\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "updated owner__alpha/install") {
		t.Errorf("stdout = %q, want the successful retry's output", res.Stdout)
	}
}

func TestBrainPutLeavesOtherFailuresAlone(t *testing.T) {
	h, logDir := newRevisionHarness(t)

	res := h.RunWith(clitest.RunOpts{Stdin: "# x\n", Env: map[string]string{"STUB_MODE": "other-error"}},
		"brain", "put", "install")

	if res.Code != 2 {
		t.Fatalf("exit %d, want gbrain's own exit code 2\nstdout: %s\nstderr: %s", res.Code, res.Stdout, res.Stderr)
	}
	if calls := stubCalls(t, logDir); len(calls) != 1 {
		t.Errorf("gbrain called %d times (%q), want exactly once: only revision_required earns a retry", len(calls), calls)
	}
	if !strings.Contains(res.Stderr, "invalid_params") {
		t.Errorf("stderr = %q, want gbrain's error passed through unchanged", res.Stderr)
	}
}

func TestRebuildForcesUpdatesOfExistingPages(t *testing.T) {
	h, logDir := newRevisionHarness(t)
	dir := filepath.Join(h.Data, "projects", "owner__alpha", "brain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	page := "# Install\nSecond edition.\n"
	clitest.WriteFile(t, filepath.Join(dir, "install.md"), page)

	res := h.Run("rebuild")

	if res.Code != 0 {
		t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", res.Code, res.Stdout, res.Stderr)
	}
	calls := stubCalls(t, logDir)
	var puts []string
	for _, c := range calls {
		if strings.HasPrefix(c, "put ") {
			puts = append(puts, c)
		}
	}
	want := []string{"put owner__alpha/install", "put owner__alpha/install --force"}
	if strings.Join(puts, "|") != strings.Join(want, "|") {
		t.Fatalf("put calls = %q, want %q (all gbrain calls: %q)", puts, want, calls)
	}
	if got := stubStdin(t, logDir, 2); got != page {
		t.Errorf("forced retry received %q, want the on-disk page", got)
	}
	if !strings.Contains(res.Stdout, "put owner__alpha/install") {
		t.Errorf("stdout = %q, want the rebuild's per-page progress line", res.Stdout)
	}
}
