package nightshift

// Workers run without a login shell, so gbrain's install dir (e.g. ~/.bun/bin)
// is often missing from their inherited PATH and brain search silently drops to
// the `devbrain brain` fallback. These helpers put gbrain back on the worker PATH.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
)

// gbrainDir returns gbrain's directory: the install-recorded config hint first
// (validated, so a moved/removed gbrain self-heals rather than pinning a dead
// path), else the dir of gbrain on PATH, else "".
func gbrainDir() string {
	if dir := config.GbrainBinDir(); dir != "" {
		if fi, err := os.Stat(filepath.Join(dir, "gbrain")); err == nil && !fi.IsDir() {
			return dir
		}
	}
	if lp, err := exec.LookPath("gbrain"); err == nil {
		return filepath.Dir(lp)
	}
	return ""
}

// workerGbrainDir returns the dir to add to a worker's PATH, or "". inheritsEnv
// is true for headless (workers inherit the orchestrator env, so a gbrain
// already on PATH needs nothing) and false for tmux (panes inherit the tmux
// server env, so the recorded dir must be injected regardless).
func workerGbrainDir(inheritsEnv bool) string {
	if inheritsEnv {
		if _, err := exec.LookPath("gbrain"); err == nil {
			return "" // workers inherit it from the orchestrator env
		}
	}
	return gbrainDir()
}

// prependPATH puts dir at the front of PATH in env (a KEY=VALUE slice like
// os.Environ()), creating PATH if absent. dir "" is a no-op.
func prependPATH(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	sep := string(os.PathListSeparator)
	for i, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			env[i] = "PATH=" + dir + sep + v
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// shSingleQuote quotes s for a POSIX shell (the tmux backend types exports into
// a live shell), escaping embedded single quotes the standard '\” way.
func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
