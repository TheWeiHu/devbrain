// Package sweep is devbrain's capture path: harvest new agent transcripts
// (Claude Code at ~/.claude, Codex at ~/.codex) into the data repo via the
// importer. Capture is file-based by design — no harness hooks, so it needs
// no hook trust and survives any hook wiring loss (the June Codex outage).
// The flusher runs it every minute; a machine-local mtime cursor makes idle
// ticks cost milliseconds.
package sweep

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/importer"
)

// Now is the injectable clock for the cursor stamp.
var Now = func() time.Time { return time.Now() }

// cursorPath is the machine-local sweep cursor (unix seconds of the last
// sweep's start). It lives OUTSIDE the data repo so the flusher never
// churn-commits it.
func cursorPath() string {
	if d := os.Getenv("DEVBRAIN_SWEEP_CURSOR_DIR"); d != "" {
		return filepath.Join(d, "sweep-cursor")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "devbrain", "sweep-cursor")
}

func readCursor() int64 {
	b, err := os.ReadFile(cursorPath())
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func writeCursor(ts int64) error {
	p := cursorPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strconv.FormatInt(ts, 10)+"\n"), 0o644)
}

// sourceRoots returns the transcript stores to watch.
func sourceRoots() (claude, codex string) {
	home, _ := os.UserHomeDir()
	claude = filepath.Join(home, ".claude")
	codex = os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	return claude, codex
}

// anyNewer reports whether any source file was modified after cursor.
func anyNewer(claude, codex string, cursor int64) bool {
	globs := []string{
		filepath.Join(claude, "projects", "*", "*.jsonl"),
		filepath.Join(claude, "projects", "*", "*", "subagents", "agent-*.jsonl"),
		filepath.Join(claude, "projects", "*", "memory", "*.md"),
		filepath.Join(codex, "sessions", "*", "*", "*", "*.jsonl"),
	}
	for _, g := range globs {
		files, _ := filepath.Glob(g)
		for _, f := range files {
			if st, err := os.Stat(f); err == nil && st.ModTime().Unix() > cursor {
				return true
			}
		}
	}
	return false
}

// Status reports the sweep cursor's last-run time (zero if never) and whether
// transcripts newer than the cursor are waiting on disk — the doctor's
// "is capture actually flowing" signal.
func Status() (last time.Time, pending bool) {
	cursor := readCursor()
	if cursor > 0 {
		last = time.Unix(cursor, 0)
	}
	claude, codex := sourceRoots()
	return last, anyNewer(claude, codex, cursor)
}

// Run executes one sweep. --force ignores the cursor (full re-harvest).
// Fail-open by design: capture must never break its caller (the flusher).
func Run(args []string, stdout, stderr io.Writer) int {
	force := false
	for _, a := range args {
		switch a {
		case "--force", "-f":
			force = true
		case "--help", "-h":
			fmt.Fprint(stdout, "devbrain sweep [--force]\n  harvest new Claude/Codex transcripts into the data repo\n  --force  ignore the incremental cursor and re-harvest everything\n")
			return 0
		default:
			fmt.Fprintf(stderr, "sweep: unknown arg: %s\n", a)
			return 2
		}
	}
	claude, codex := sourceRoots()
	cursor := readCursor()
	if force {
		cursor = 0
	}
	if !anyNewer(claude, codex, cursor) {
		return 0 // idle tick — nothing new on disk
	}
	// Stamp the cursor at sweep START minus one second (not end): a
	// transcript written while the import runs — or within the start's own
	// clock second — is re-swept next tick instead of skipped. Overlap is
	// harmless; the importer is idempotent.
	start := Now().Unix() - 1
	importArgs := []string{"--apply", "--claude", claude, "--codex", codex,
		"--newer-mtime", strconv.FormatInt(cursor, 10)}
	if rc := importer.Run(importArgs, io.Discard, stderr); rc != 0 {
		fmt.Fprintf(stderr, "sweep: import failed (rc=%d) — will retry next sweep\n", rc)
		return 0
	}
	if err := writeCursor(start); err != nil {
		fmt.Fprintf(stderr, "sweep: cursor write: %v\n", err)
	}
	fmt.Fprintln(stdout, "sweep: harvested new transcripts")
	return 0
}
