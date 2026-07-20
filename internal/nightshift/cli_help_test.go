package nightshift

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartHelpIsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stdout, stderr bytes.Buffer
	if rc := RunCLI([]string{"start", "--help"}, &stdout, &stderr); rc != 0 {
		t.Fatalf("start --help rc=%d stderr=%q", rc, stderr.String())
	}
	for _, want := range []string{"usage: devbrain nightshift start", "--codex-reasoning", "--max-subagents", "--max-capacity-failures"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help missing %q:\n%s", want, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "nightshift", "repo")); !os.IsNotExist(err) {
		t.Fatalf("start --help mutated remembered repo state: %v", err)
	}
}
