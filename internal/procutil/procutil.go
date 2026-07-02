// Package procutil holds the process primitives nightshift needs: an
// O_EXCL pidfile with stale-owner recovery, kill-0 liveness, and a
// process-group kill (the daemonizer itself comes in a later phase).
package procutil

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Alive is `kill -0 pid`: true when the process exists (EPERM counts —
// the process is there, we just can't signal it).
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// ReadPidfile returns the pid stored at path (0, false when absent/garbage).
func ReadPidfile(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// CreatePidfile claims path for pid with O_CREATE|O_EXCL. If the file exists
// but its owner is dead (stale), it is removed and the claim retried once.
// A live owner returns an error carrying that pid.
func CreatePidfile(path string, pid int) error {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d\n", pid)
			cerr := f.Close()
			if werr != nil {
				return werr
			}
			return cerr
		}
		if !os.IsExist(err) {
			return err
		}
		if owner, ok := ReadPidfile(path); ok && Alive(owner) {
			return fmt.Errorf("pidfile %s held by live pid %d", path, owner)
		}
		os.Remove(path) // stale (dead or garbage owner) — reclaim
	}
	return fmt.Errorf("pidfile %s: could not claim", path)
}

// RemovePidfile drops the pidfile (best-effort).
func RemovePidfile(path string) { os.Remove(path) }

// KillGroup signals the whole process group of pid (negative-pid kill),
// falling back to the single process when it leads no group of its own —
// the `pkill -P $p; kill $p` sweep from the orchestrator's cleanup.
func KillGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("bad pid %d", pid)
	}
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
		return syscall.Kill(-pgid, sig)
	}
	return syscall.Kill(pid, sig)
}
