package brain_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

// --local keeps retrieval to the current project: no cross-project padding,
// and the flag is consumed here rather than forwarded to the engine.
func TestBrainSearchLocalFlagEngine(t *testing.T) {
	h := clitest.New(t)
	h.Project = "owner__alpha"
	stub := filepath.Join(t.TempDir(), "gbrain-stub")
	clitest.WriteExec(t, stub, `#!/bin/sh
case "$1" in
  search|query|ask)
    case "$*" in *--local*) echo 'engine rejected --local'; exit 2;; esac
    printf '%s\n' \
      '[0.9900] projects/owner__beta/brain/guide -- beta curated' \
      '[0.9800] projects/owner__alpha/brain/guide -- alpha curated' \
      '[0.9700] projects/owner__alpha/brain/setup -- alpha setup' \
      '[0.9600] projects/owner__gamma/brain/guide -- gamma curated'
    ;;
  *) printf 'stub:%s\n' "$*" ;;
esac
`)
	h.Env["DEVBRAIN_GBRAIN"] = stub

	res := h.Run("brain", "search", "guide", "--local")
	lines := resultLines(res.Stdout)
	if res.Code != 0 || len(lines) != 2 {
		t.Fatalf("--local: code=%d results=%d, want 0 and the 2 current-project pages:\n%s", res.Code, len(lines), res.Stdout)
	}
	for _, l := range lines {
		if !strings.Contains(l, "owner__alpha/") {
			t.Errorf("--local leaked a cross-project result: %s", l)
		}
	}
	// the default is unchanged: local first, then up to two cross-project hits
	if def := h.Run("brain", "search", "guide").Stdout; len(resultLines(def)) != 4 {
		t.Errorf("default search changed shape:\n%s", def)
	}
}

// The offline fallback honors --local the same way, and the two scope flags
// cannot be combined.
func TestBrainSearchLocalFlagOffline(t *testing.T) {
	h := clitest.New(t)
	h.Project = "owner__alpha"
	for _, p := range []struct{ project, body string }{
		{"owner__alpha", "# Install\nHow to install the widget daemon.\n"},
		{"owner__beta", "# Install\nBeta notes on the widget daemon.\n"},
	} {
		dir := filepath.Join(h.Data, "projects", p.project, "brain")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		clitest.WriteFile(t, filepath.Join(dir, "install.md"), p.body)
	}
	h.Env["DEVBRAIN_GBRAIN"] = "gbrain-offline-stub-not-a-real-binary"
	if _, err := exec.LookPath("gbrain-offline-stub-not-a-real-binary"); err == nil {
		t.Skip("skip: could not remove gbrain from PATH for offline test")
	}
	out := h.Run("brain", "search", "widget", "daemon", "--local").Stdout
	if lines := resultLines(out); len(lines) != 1 || !strings.Contains(lines[0], "owner__alpha/install") {
		t.Errorf("offline --local: want only owner__alpha/install, got:\n%s", out)
	}
	if def := h.Run("brain", "search", "widget", "daemon").Stdout; !strings.Contains(def, "owner__beta/install") {
		t.Errorf("default offline search must still include the cross-project page:\n%s", def)
	}
	if both := h.Run("brain", "search", "widget", "--local", "--global"); both.Code == 0 {
		t.Errorf("--local with --global must be rejected, got code 0:\n%s", both.Stdout)
	}
}
