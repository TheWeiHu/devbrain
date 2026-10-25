package todo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeOpenTask(t *testing.T, dir, id string) {
	t.Helper()
	body := "---\nid: " + id + "\nstatus: open\n---\n\n# " + id + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The body may be passed as trailing arguments instead of on stdin — the way an
// agent invokes it inline — and must be stored, not silently dropped.
func TestContextBodyFromArgs(t *testing.T) {
	dir := t.TempDir()
	writeOpenTask(t, dir, "0001-task")
	var out bytes.Buffer
	c := &cli{dir: dir, project: "p", stdout: &out, stderr: &out, stdin: strings.NewReader("")}
	if code := c.context([]string{"0001-task", "first finding;", "second finding"}); code != 0 {
		t.Fatalf("context with body args = %d, want 0: %s", code, out.String())
	}
	b, _ := os.ReadFile(filepath.Join(dir, "0001-task.md"))
	if !strings.Contains(string(b), "## Context (synthesized ") || !strings.Contains(string(b), "first finding; second finding") {
		t.Errorf("body args were not stored:\n%s", b)
	}
}

// A socket on stdin — a tool harness that never closes it — must be refused the
// way a tty is, not read until the end of time.
func TestContextRefusesSocketStdin(t *testing.T) {
	dir := t.TempDir()
	writeOpenTask(t, dir, "0002-task")
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Skip("socketpair unavailable:", err)
	}
	rd := os.NewFile(uintptr(fds[0]), "stdin-socket")
	peer := os.NewFile(uintptr(fds[1]), "stdin-socket-peer")
	defer rd.Close()
	defer peer.Close() // stays open for the whole test: nothing ever ends stdin
	var out bytes.Buffer
	c := &cli{dir: dir, project: "p", stdout: &out, stderr: &out, stdin: rd}
	done := make(chan int, 1)
	go func() { done <- c.context([]string{"0002-task"}) }()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(out.String(), "stdin") {
			t.Errorf("socket stdin: code=%d stderr=%q, want 1 and a stdin hint", code, out.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context blocked on an unclosed socket stdin instead of refusing it")
	}
}
