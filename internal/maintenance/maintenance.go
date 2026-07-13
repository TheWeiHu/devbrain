// Package maintenance holds distill's daily-maintenance gating: which of the
// once-a-day upkeep passes (reconcile / audit / preferences / archive) are due,
// and stamping a pass done. It replaces the fragile date-math shell block in the
// distill skill's Step 8 — the one that shelled out to `date -j`/`date -d` with
// non-portable fallbacks — with one tested implementation.
package maintenance

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
)

// A pass is one daily-maintenance gate. Its cursor file records the date it last
// ran; it's due again once `everyDays` have elapsed (or it never ran).
type pass struct {
	name      string
	everyDays int
}

// passes in the fixed order Due prints them. preferences has no stamp file — its
// cursor is the newest `· distill` entry in preferences/edits.md (the diff the
// refresh already writes), so it is due-only, never stamped here.
var passes = []pass{
	{"sweep", 1}, // backstop transcript harvest for machines without the flusher
	{"reconcile", 1},
	{"audit", 1},
	{"preferences", 1},
	{"archive", 30},
}

func passByName(name string) (pass, bool) {
	for _, p := range passes {
		if p.name == name {
			return p, true
		}
	}
	return pass{}, false
}

// lastRun returns the date a pass last ran, or the zero time if it never has.
func lastRun(dataDir, project, name string) time.Time {
	switch name {
	case "preferences":
		// newest `## <date> … · distill` line in the shared edit history.
		b, err := os.ReadFile(filepath.Join(dataDir, "preferences", "edits.md"))
		if err != nil {
			return time.Time{}
		}
		re := regexp.MustCompile(`(?m)^## (\d{4}-\d{2}-\d{2}).*· distill`)
		var newest time.Time
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if t, err := time.ParseInLocation("2006-01-02", m[1], time.Local); err == nil && t.After(newest) {
				newest = t
			}
		}
		return newest
	default:
		// `last <pass>: <date>` in the per-project cursor file.
		b, err := os.ReadFile(cursorPath(dataDir, project, name))
		if err != nil {
			return time.Time{}
		}
		prefix := "last " + name + ": "
		for _, line := range strings.Split(string(b), "\n") {
			if s, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
				if t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local); err == nil {
					return t
				}
			}
		}
		return time.Time{}
	}
}

// cursorPath is the per-project stamp file for a pass. Names mirror the distill
// skill's historical files so an existing repo keeps its cursors.
func cursorPath(dataDir, project, name string) string {
	file := map[string]string{
		"sweep":     "swept.md",
		"reconcile": "reconciled.md",
		"audit":     "audited.md",
		"archive":   "archived.md",
	}[name]
	return filepath.Join(dataDir, "projects", project, file)
}

// due reports whether a pass is due at now: never-run, or its interval elapsed.
// Dates are read and written in local time to match the shell this replaces
// (`date +%F` / `date -j`), so the once-a-(local-)day gates don't reopen twice
// when two runs straddle UTC midnight but not local midnight.
func due(last time.Time, everyDays int, now time.Time) bool {
	if last.IsZero() {
		return true
	}
	return int(now.Sub(last).Hours()/24) >= everyDays
}

// Due returns the names of the passes due for a project, in fixed order.
func Due(dataDir, project string, now time.Time) []string {
	var out []string
	for _, p := range passes {
		if due(lastRun(dataDir, project, p.name), p.everyDays, now) {
			out = append(out, p.name)
		}
	}
	return out
}

// Stamp records a pass as run today. preferences is not stampable (its cursor is
// the edits.md diff the refresh writes).
func Stamp(dataDir, project, name string, now time.Time) error {
	p, ok := passByName(name)
	if !ok {
		return fmt.Errorf("unknown pass %q (want: sweep reconcile audit preferences archive)", name)
	}
	if p.name == "preferences" {
		return fmt.Errorf("preferences is not stampable — its cursor is the `· distill` entry in edits.md")
	}
	header := map[string]string{
		"sweep":     "# swept — transcript-sweep cursor for %s\n\nlast sweep: %s\n",
		"reconcile": "# reconciled — /reconcile cursor for %s\n\nlast reconcile: %s\n",
		"audit":     "# audited — /audit cursor for %s\n\nlast audit: %s\n",
		"archive":   "# archived — todo archive cursor for %s\n\nlast archive: %s\n",
	}[name]
	path := cursorPath(dataDir, project, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf(header, project, now.Format("2006-01-02"))), 0o644)
}

// Run is `devbrain maintenance <due|stamp> <project> [pass]`.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: devbrain maintenance <due|stamp> <project> [pass]")
		return 2
	}
	sub, project := args[0], args[1]
	dataDir := config.DataDir()
	now := time.Now()
	switch sub {
	case "due":
		if d := Due(dataDir, project, now); len(d) > 0 {
			fmt.Fprintln(stdout, strings.Join(d, " "))
		}
		return 0
	case "stamp":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "usage: devbrain maintenance stamp <project> <pass>")
			return 2
		}
		if err := Stamp(dataDir, project, args[2], now); err != nil {
			fmt.Fprintf(stderr, "maintenance: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stderr, "maintenance: unknown subcommand %q\n", sub)
	return 2
}
