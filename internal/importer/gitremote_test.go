package importer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestGitRemoteTimesOut proves gitRemote never hangs on a blocking `git`: a
// fake git that sleeps far past the timeout must be abandoned (returns "")
// within the bound, not waited out. Guards the regression where the Go port
// dropped import.py's timeout=5 and a single stalled git froze the whole run.
func TestGitRemoteTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-git shim is a POSIX shell script")
	}
	dir := t.TempDir()
	shim := filepath.Join(dir, "git")
	// exec (not fork) so the process holding the stdout pipe is the one the
	// timeout kills — no orphaned sleep left behind.
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := gitRemoteTimeout
	gitRemoteTimeout = 200 * time.Millisecond
	defer func() { gitRemoteTimeout = old }()

	start := time.Now()
	got := gitRemote(t.TempDir())
	elapsed := time.Since(start)

	if got != "" {
		t.Errorf("gitRemote returned %q, want empty on timeout", got)
	}
	// Budget: the (shortened) timeout + WaitDelay + scheduler slack. Loose enough
	// not to flake, tight enough to catch a regression that waits out the git.
	if budget := gitRemoteTimeout + time.Second + 800*time.Millisecond; elapsed > budget {
		t.Errorf("gitRemote took %s (budget %s) — did not honor the %s timeout", elapsed, budget, gitRemoteTimeout)
	}
}
