package main

// Port of scripts/test-git-gate.sh: drives scripts/git-hooks/pre-push inside a
// throwaway repo with crafted pre-push stdin and a STUB gate command, checking
// which pushes trigger the suite, that a red gate blocks, and env scrubbing.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func ggGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestGitPushGate(t *testing.T) {
	hook := filepath.Join(clitest.Root(t), "scripts", "git-hooks", "pre-push")
	if _, err := os.Stat(hook); err != nil {
		t.Skipf("pre-push hook not found: %v", err)
	}

	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	clitest.Git(t, "", "init", "-q", repo)
	clitest.WriteFile(t, filepath.Join(repo, "f"), "a\n")
	clitest.Git(t, repo, "add", "f")
	clitest.Git(t, repo, "commit", "-qm", "c1")
	oid := ggGitOut(t, repo, "rev-parse", "HEAD")
	const zero = "0000000000000000000000000000000000000000"
	const gone = "ffffffffffffffffffffffffffffffffffffffff" // valid shape, not a real object
	ran := filepath.Join(tmp, "ran")                        // the stub touches this iff the gate ran

	// fire runs the hook in the repo with crafted stdin and env; returns the hook
	// exit code and whether the gate fired (touched `ran`).
	fire := func(stdin string, env ...string) (int, bool) {
		os.Remove(ran)
		cmd := exec.Command("bash", hook, "origin", "git@x:r")
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(), env...)
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("hook: %v", err)
			}
			code = ee.ExitCode()
		}
		_, statErr := os.Stat(ran)
		return code, statErr == nil
	}
	green := "DEVBRAIN_GATE_CMD=touch " + ran
	red := "DEVBRAIN_GATE_CMD=touch " + ran + "; false"

	// protected branches gate the pushed OID; everything else passes through.
	if code, gated := fire("refs/heads/main "+oid+" refs/heads/main "+zero+"\n", green, "DEVBRAIN_GATE_SKIP=0"); code != 0 || !gated {
		t.Errorf("push to main (green): code=%d gated=%v, want 0/true", code, gated)
	}
	if code, gated := fire("refs/heads/x "+oid+" refs/heads/nightshift "+zero+"\n", green); code != 0 || !gated {
		t.Errorf("push to nightshift: code=%d gated=%v, want 0/true", code, gated)
	}
	if code, gated := fire("refs/heads/x "+oid+" refs/heads/feature-x "+zero+"\n", green); code != 0 || gated {
		t.Errorf("feature branch ungated: code=%d gated=%v, want 0/false", code, gated)
	}
	if code, gated := fire("refs/heads/x "+oid+" refs/heads/maintenance "+zero+"\n", green); code != 0 || gated {
		t.Errorf("'main' substring in 'maintenance' must not gate: code=%d gated=%v", code, gated)
	}

	// red gate blocks; an unstageable OID is a failure, not a skip.
	if code, _ := fire("refs/heads/main "+oid+" refs/heads/main "+zero+"\n", red); code != 1 {
		t.Errorf("red gate blocks push to main: code=%d, want 1", code)
	}
	if code, _ := fire("refs/heads/main "+gone+" refs/heads/main "+zero+"\n", green); code != 1 {
		t.Errorf("unstageable OID blocks push: code=%d, want 1", code)
	}
	if code, gated := fire("(delete) "+zero+" refs/heads/main "+oid+"\n", green); code != 0 || gated {
		t.Errorf("deleting main needs no gate: code=%d gated=%v", code, gated)
	}

	// mixed batch: any protected ref triggers the gate.
	if code, gated := fire("refs/heads/a "+oid+" refs/heads/feature-a "+zero+"\nrefs/heads/b "+oid+" refs/heads/main "+zero+"\n", green); code != 0 || !gated {
		t.Errorf("mixed batch with main gates: code=%d gated=%v", code, gated)
	}

	// same commit to both protected refs gates ONCE (dedup).
	runs := filepath.Join(tmp, "runs")
	clitest.WriteFile(t, runs, "")
	fire("refs/heads/main "+oid+" refs/heads/main "+zero+"\nrefs/heads/main "+oid+" refs/heads/nightshift "+zero+"\n",
		"DEVBRAIN_GATE_CMD=echo x >> "+runs)
	if got := nonEmptyLines(clitest.Read(t, runs)); got != 1 {
		t.Errorf("same OID to main+nightshift gated %d times, want 1", got)
	}

	// explicit bypass.
	if code, gated := fire("refs/heads/main "+oid+" refs/heads/main "+zero+"\n", "DEVBRAIN_GATE_SKIP=1", green); code != 0 || gated {
		t.Errorf("DEVBRAIN_GATE_SKIP=1 must bypass the gate: code=%d gated=%v", code, gated)
	}

	// the gate scrubs git's hook env (GIT_DIR etc.) before running the suite;
	// the stub touches `ran` only if GIT_DIR is gone.
	if code, gated := fire("refs/heads/main "+oid+" refs/heads/main "+zero+"\n",
		"GIT_DIR="+filepath.Join(repo, ".git"),
		`DEVBRAIN_GATE_CMD=[ -z "${GIT_DIR:-}" ] && touch `+ran); code != 0 || !gated {
		t.Errorf("gate must scrub GIT_DIR before the suite: code=%d gated=%v", code, gated)
	}
}

func nonEmptyLines(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			n++
		}
	}
	return n
}
