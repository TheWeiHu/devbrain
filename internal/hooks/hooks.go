// Package hooks implements the harness hook handlers behind
// `devbrain hook <event>`: the gbrain query trace, the session-start context
// nudge, and nightshift's turn marker. Prompt/response/token capture is NOT
// hook-based — the sweep (internal/sweep, run by every flush) harvests it
// from the harness's own transcripts, so capture needs no hook trust and
// survives any hook wiring loss.
//
// The contract every handler inherits from the scripts: model-free, never
// blocks the agent's turn, never fails the session — errors are swallowed and
// the process exits 0 (see Run in runner.go).
package hooks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/gbrainlog"
	"github.com/TheWeiHu/devbrain/internal/hookev"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
	"github.com/TheWeiHu/devbrain/internal/version"
)

// Now is the injectable clock (UTC). Tests override it; the hooks' file
// naming and timestamps all derive from it.
var Now = func() time.Time { return time.Now().UTC() }

// Event gives handlers normalized access to the hook payload.
type Event struct {
	Payload []byte
}

// Field reads one normalized field via the per-harness shim.
func (e *Event) Field(name string) string {
	return hookev.ReadEvent(string(e.Payload), name, "")
}

// cwdOrDefault falls back to the process cwd, like the scripts' `$PWD`.
func (e *Event) cwd() string {
	if c := e.Field("cwd"); c != "" {
		return c
	}
	wd, _ := os.Getwd()
	return wd
}

// projectOf resolves the project folder for a capture. It returns "" for the
// devbrain data repo itself (ProjectKey's data-repo refusal) — every capture
// caller treats "" as "skip", so a session run from inside the data repo never
// mints a projects/<data-repo>/ folder.
func projectOf(cwd string) string {
	return projectkey.ProjectKey(cwd)
}

// autoSession mirrors the capture-response.sh / queue.py rule: a session is
// autonomous when its cwd is under a nightshift/drain dir or its worktree
// carries a -w<N> suffix.
var wtAuto = regexp.MustCompile(`-w[0-9]+$`)

func autoSession(cwd, worktree string) bool {
	if strings.Contains(cwd, "/nightshift/") || strings.Contains(cwd, "/drain/") {
		return true
	}
	return wtAuto.MatchString(worktree)
}

// slugPrefixRe extracts the owner__repo prefix from a gbrain result line —
// authoritative routing when the call returned hits (the sed in
// capture-gbrain.sh).
var slugPrefixRe = regexp.MustCompile(`^\[[0-9.]+\][ \t\v\f\r]+([A-Za-z0-9._-]*__[A-Za-z0-9._-]*)/`)

// Gbrain ports capture-gbrain.sh (PostToolUse on Bash): trace gbrain calls to
// projects/<project>/gbrain-queries.log, routing the record to the repo the
// call actually queried.
func Gbrain(e *Event) error {
	// raw fast-bail before any parsing: this fires on EVERY Bash call
	if !bytes.Contains(e.Payload, []byte("gbrain")) {
		return nil
	}
	data := config.DataDir()
	if e.Field("tool") != "Bash" {
		return nil
	}
	cmd := e.Field("command")
	if cmd == "" || !strings.Contains(cmd, "gbrain") {
		return nil
	}
	cwd := e.cwd()
	out := e.Field("tool-response")
	project := projectOf(cwd)

	if os.Getenv("DEVBRAIN_PROJECT") == "" {
		if slug := slugFromOutput(out); slug != "" {
			project = slug // 1. gbrain's own output names the brain that answered
		} else if target := cdTarget(cmd, cwd); target != "" {
			if st, err := os.Stat(target); err == nil && st.IsDir() {
				cdProject := projectkey.ProjectKey(target)
				switch cdProject {
				case "", "miscellaneous", "unknown":
				default:
					project = cdProject // 2. inline `cd` target -> attribute there
				}
			}
		}
	}

	auto := autoSession(cwd, projectkey.WorktreeSlug(cwd))
	record := gbrainlog.Record(cmd, out, project, Now().Format("2006-01-02T15:04:05Z"), auto)
	if record == "" {
		return nil // no real gbrain subcommand -> touch nothing
	}
	if project == "" {
		return nil // data repo, and no cd/slug routed it elsewhere -> don't log
	}
	dir := filepath.Join(data, "projects", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return appendFile(filepath.Join(dir, "gbrain-queries.log"), record+"\n")
}

func slugFromOutput(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if m := slugPrefixRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// cdTarget recovers the first `cd <target>` from an inline compound command
// (port of the embedded python in capture-gbrain.sh). Returns an absolute
// path or "" when none found.
var (
	cdVarRe    = regexp.MustCompile(`(?:^|[\s;&|(])([A-Za-z_]\w*)=("(?:[^"\\]|\\.)*"|'[^']*'|[^\s;&|()]*)`)
	cdRe       = regexp.MustCompile(`(?:^|[\s;&|(])cd\s+("(?:[^"\\]|\\.)*"|'[^']*'|[^\s;&|()]+)`)
	cdVarRefRe = regexp.MustCompile(`^\$\{?(\w+)\}?$`)
)

func cdTarget(cmd, cwd string) string {
	vars := map[string]string{}
	for _, m := range cdVarRe.FindAllStringSubmatch(cmd, -1) {
		v := m[2]
		if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, "'") {
			v = v[1 : len(v)-1]
		}
		vars[m[1]] = v
	}
	m := cdRe.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	t := m[1]
	if strings.HasPrefix(t, `"`) || strings.HasPrefix(t, "'") {
		t = t[1 : len(t)-1]
	}
	if mv := cdVarRefRe.FindStringSubmatch(t); mv != nil {
		t = vars[mv[1]]
	}
	if strings.HasPrefix(t, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if t == "~" {
				t = home
			} else if strings.HasPrefix(t, "~/") {
				t = filepath.Join(home, t[2:])
			}
		}
	}
	if t == "" {
		return ""
	}
	if !strings.HasPrefix(t, "/") {
		t = filepath.Join(cwd, t)
	}
	return t
}

var openStatusRe = regexp.MustCompile(`(?m)^status:[ \t\v\f\r]*open[ \t\v\f\r]*$`)

// SessionStart ports session-start-nudge.sh: when the cwd's project has brain
// pages or open tasks, print the additionalContext JSON nudge to stdout.
func SessionStart(e *Event) error {
	data := config.DataDir()
	project := projectkey.ProjectKey(e.cwd())
	if project == "" || project == "miscellaneous" {
		return nil
	}
	pdir := filepath.Join(data, "projects", project)
	if st, err := os.Stat(pdir); err != nil || !st.IsDir() {
		return nil
	}
	pages := 0
	if ents, err := os.ReadDir(filepath.Join(pdir, "brain")); err == nil {
		for _, en := range ents {
			if strings.HasSuffix(en.Name(), ".md") {
				pages++
			}
		}
	}
	tasks := 0
	if ents, err := os.ReadDir(filepath.Join(pdir, "todo")); err == nil {
		for _, en := range ents {
			if !strings.HasSuffix(en.Name(), ".md") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(pdir, "todo", en.Name()))
			if err == nil && openStatusRe.Match(b) {
				tasks++
			}
		}
	}
	if pages == 0 && tasks == 0 {
		return nil
	}
	plural := func(n int, s string) string {
		if n == 1 {
			return s
		}
		return s + "s"
	}
	parts := ""
	if pages > 0 {
		parts = fmt.Sprintf("%d %s", pages, plural(pages, "brain page"))
	}
	if tasks > 0 {
		if parts != "" {
			parts += " and "
		}
		parts += fmt.Sprintf("%d open %s", tasks, plural(tasks, "task"))
	}
	msg := "devbrain: this repo maps to project `" + project + "` with " + parts + ". Before you answer a " +
		"non-trivial question, ask the user something the brain may already record, or start work, " +
		"query the brain FIRST: `gbrain search \"<terms>\"` (or `gbrain query \"<question>\"` with an " +
		"OpenAI key). No gbrain installed? `devbrain brain search \"<terms>\"` is a drop-in that greps " +
		"the pages offline. The brain is usually faster and more current than re-deriving from the code. " +
		"To READ a page a search surfaces, pass its FULL `<project>/<page>` slug from the output to " +
		"`gbrain get \"<project>/<page>\" --fuzzy` (or `devbrain brain get …`) — not the bare page name " +
		"(the brain is one namespace, so a bare slug is page_not_found). To resume this project in full " +
		"— brief + work the top task — run /continue."
	if up := version.Notice(); up != "" {
		msg += " " + up
	}
	fmt.Print(hookev.SessionStartContext(msg))
	return nil
}

// TurnMarker ports turn-marker.sh (Stop, nightshift only): append one TSV
// line to $NIGHTSHIFT_MARKER. No-ops without the env var, so it is safe to
// register globally.
func TurnMarker(e *Event) error {
	marker := os.Getenv("NIGHTSHIFT_MARKER")
	if marker == "" {
		return nil
	}
	session := e.Field("session")
	if session == "" {
		session = "nosession"
	}
	stopActive := e.Field("stop-active")
	if stopActive == "" {
		stopActive = "false"
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return err
	}
	line := fmt.Sprintf("%s\t%s\tstop_active=%s\n", Now().Format("2006-01-02T15:04:05Z"), session, stopActive)
	return appendFile(marker, line)
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
