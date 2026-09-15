package flush

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/config"
)

func twoBrains(t *testing.T) (string, string, string, string) {
	t.Helper()
	one, remoteOne := setup(t)
	two, remoteTwo := setup(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DEVBRAIN_DATA", "")
	t.Setenv("DEVBRAIN_BRAIN", "")
	if err := config.Write(one); err != nil {
		t.Fatal(err)
	}
	if err := config.RegisterBrain("work", two); err != nil {
		t.Fatal(err)
	}
	return one, two, remoteOne, remoteTwo
}

func TestBrainsHaveIndependentFlushWindows(t *testing.T) {
	one, two, remoteOne, remoteTwo := twoBrains(t)
	os.WriteFile(filepath.Join(one, "personal"), []byte("personal"), 0o644)
	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("first flush: %d", rc)
	}
	os.WriteFile(filepath.Join(two, "work"), []byte("work"), 0o644)
	if rc := Run([]string{"--scheduled"}, io.Discard, io.Discard); rc != 0 {
		t.Fatalf("second flush: %d", rc)
	}
	if !strings.Contains(mustGit(t, remoteOne, "ls-tree", "--name-only", "main"), "personal") {
		t.Fatal("personal commit did not reach its remote")
	}
	if !strings.Contains(mustGit(t, remoteTwo, "ls-tree", "--name-only", "main"), "work") {
		t.Fatal("personal throttle blocked work commit")
	}
	if strings.Contains(mustGit(t, remoteOne, "ls-tree", "--name-only", "main"), "work") {
		t.Fatal("work reached personal remote")
	}
}

func TestUnavailableBrainDoesNotBlockOtherPushes(t *testing.T) {
	one, two, remoteOne, _ := twoBrains(t)
	if err := os.Rename(two, two+"-offline"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(one, "personal"), []byte("personal"), 0o644)
	if rc := Run(nil, io.Discard, io.Discard); rc == 0 {
		t.Fatal("missing brain was not reported")
	}
	if !strings.Contains(mustGit(t, remoteOne, "ls-tree", "--name-only", "main"), "personal") {
		t.Fatal("missing work brain blocked personal sync")
	}
}
