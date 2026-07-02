package procutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Error("own pid must be alive")
	}
	if Alive(0) || Alive(-1) {
		t.Error("non-positive pids are never alive")
	}
	// a reaped child is dead
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	cmd.Wait()
	if Alive(pid) {
		t.Error("reaped child should be dead")
	}
}

func TestPidfile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.pid")
	if err := CreatePidfile(p, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if got, ok := ReadPidfile(p); !ok || got != os.Getpid() {
		t.Errorf("ReadPidfile = %d,%v", got, ok)
	}
	// live owner → second claim refused, file intact
	if err := CreatePidfile(p, 12345); err == nil || !strings.Contains(err.Error(), "live pid") {
		t.Errorf("claim over a live owner must fail, got %v", err)
	}
	if got, _ := ReadPidfile(p); got != os.Getpid() {
		t.Error("failed claim must not clobber the live pidfile")
	}
	// stale owner (dead pid) → reclaimed
	cmd := exec.Command("true")
	cmd.Start()
	dead := cmd.Process.Pid
	cmd.Wait()
	os.WriteFile(p, []byte("garbage\n"), 0o644) // garbage counts as stale too
	if err := CreatePidfile(p, dead+0); err != nil {
		t.Fatalf("stale (garbage) pidfile not reclaimed: %v", err)
	}
	os.Remove(p)
	os.WriteFile(p, []byte("999999\n"), 0o644) // near-certainly dead pid
	if Alive(999999) {
		t.Skip("pid 999999 exists on this machine")
	}
	if err := CreatePidfile(p, os.Getpid()); err != nil {
		t.Fatalf("stale (dead-owner) pidfile not reclaimed: %v", err)
	}
	if got, _ := ReadPidfile(p); got != os.Getpid() {
		t.Error("reclaim did not write our pid")
	}
	RemovePidfile(p)
	if _, ok := ReadPidfile(p); ok {
		t.Error("RemovePidfile left the file")
	}
}

func TestKillGroup(t *testing.T) {
	// a sleeper in its own process group
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := KillGroup(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("KillGroup: %v", err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("group kill did not stop the sleeper")
	}
	if err := KillGroup(0, syscall.SIGTERM); err == nil {
		t.Error("pid 0 must be rejected")
	}
}
