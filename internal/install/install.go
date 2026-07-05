// Package install is the machine wiring: `devbrain install` (setup +
// scripts/install.sh folded into one front door), `devbrain uninstall`, and
// `devbrain link-preferences`. It replaces the copy-install model — no script
// copies under ~/.claude/hooks; settings.json/hooks.json point straight at
// the devbrain binary, and the resolved data dir lives in
// ~/.config/devbrain/config.json (internal/config) instead of sed-pinned
// script copies. A legacy sweep (migrate.go) runs first on every install so
// upgrading from the bash installer is automatic.
package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/brain"
	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/jsonedit"
	"github.com/TheWeiHu/devbrain/internal/version"
)

// components, in display order. Defaults: everything on (nightshift included —
// it ships the toolset but never auto-runs the fleet).
var allComponents = []string{
	"capture", "response-trace", "nudge", "flusher", "skills",
	"claude-md", "codex", "nightshift", "git-gate",
}

// backCompatAliases are the ~/.local/bin shims install creates (argv[0] dispatch
// in the binary handles the devbrain-* names). Single source of truth so install's
// wire, install's preview, and uninstall stay mirror images — add one here and it
// is created and swept in lockstep.
var backCompatAliases = []string{"devbrain-todo", "devbrain-import"}

// legacyAliases are ~/.local/bin shims the old bash installer left that install no
// longer creates but uninstall still sweeps (devbrain/nightshift come from brew/PATH).
var legacyAliases = []string{"devbrain", "nightshift"}

// ctx carries the resolved environment for one install/uninstall run.
type ctx struct {
	home   string
	claude string // ~/.claude
	codex  string // ${CODEX_HOME:-~/.codex}
	bin    string // stable binary path recorded in settings/plists
	data   string // resolved data repo
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	goos   string // runtime.GOOS, overridable in tests

	codexHooksEnabled bool // set by wireCodex, read by summary
}

func newCtx(stdout, stderr io.Writer, stdin io.Reader) (*ctx, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot resolve HOME: %w", err)
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	return &ctx{
		home:   home,
		claude: filepath.Join(home, ".claude"),
		codex:  codex,
		bin:    BinaryPath(),
		stdout: stdout,
		stderr: stderr,
		stdin:  stdin,
		goos:   runtime.GOOS,
	}, nil
}

// BinaryPath resolves the path to record in settings.json / hooks.json / the
// flusher plist: `devbrain` from PATH when it is the same file as the running
// executable (so brew's /opt/homebrew/bin/devbrain symlink is recorded, not
// the keg path — symlinks are deliberately NOT resolved), else the running
// executable itself.
func BinaryPath() string {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	if lp, err := exec.LookPath("devbrain"); err == nil {
		if abs, err := filepath.Abs(lp); err == nil {
			ai, err1 := os.Stat(abs)
			ei, err2 := os.Stat(exe)
			if err1 == nil && err2 == nil && os.SameFile(ai, ei) {
				return abs
			}
		}
	}
	return exe
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// backup copies path to path.bak.<unix-ts> (legacy behavior before every
// settings edit). Missing file is a no-op.
func backup(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = os.WriteFile(fmt.Sprintf("%s.bak.%d", path, time.Now().Unix()), b, 0o644)
}

// display shortens a path under $HOME to ~/… for output.
func (c *ctx) display(p string) string {
	if strings.HasPrefix(p, c.home+"/") {
		return "~/" + strings.TrimPrefix(p, c.home+"/")
	}
	return p
}

// ── option parsing ────────────────────────────────────────────────────────────

type options struct {
	on          map[string]bool
	explicit    map[string]bool
	yes         bool
	gbrain      string // "" undecided · "1" install · "0" skip (flag or DEVBRAIN_GBRAIN)
	open        string // "" default · "1" open · "0" skip
	dryRun      bool   // --dry-run/--explain: print the plan, touch nothing
	explain     bool   // --explain: dry-run plus a one-line why per action
	installDeps bool   // --install-deps: consent to a global `bun add -g gbrain`
}

// defaultGbrainPackage pins the engine install for reproducible/auditable runs;
// override with DEVBRAIN_GBRAIN_PACKAGE (e.g. gbrain@latest or a fork).
const defaultGbrainPackage = "gbrain@0.18.2"

func gbrainPackage() string {
	if p := os.Getenv("DEVBRAIN_GBRAIN_PACKAGE"); p != "" {
		return p
	}
	return defaultGbrainPackage
}

func parseArgs(args []string, errw io.Writer) (*options, int) {
	o := &options{
		on:       map[string]bool{},
		explicit: map[string]bool{},
		gbrain:   os.Getenv("DEVBRAIN_GBRAIN"),
		open:     os.Getenv("DEVBRAIN_OPEN_DASHBOARD"),
	}
	for _, c := range allComponents {
		o.on[c] = true
	}
	if os.Getenv("DEVBRAIN_NIGHTSHIFT") == "0" {
		o.on["nightshift"] = false
	}
	known := map[string]bool{}
	for _, c := range allComponents {
		known[c] = true
	}
	setList := func(val bool, list string) bool {
		for _, item := range strings.Split(list, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if !known[item] {
				fmt.Fprintf(errw, "install: unknown component '%s' (have: %s)\n", item, strings.Join(allComponents, " "))
				return false
			}
			o.on[item] = val
			o.explicit[item] = true
		}
		return true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			fmt.Fprintf(errw, "install: %s needs a component list\n", a)
			return "", false
		}
		switch a {
		case "--version", "-V":
			fmt.Fprintf(errw, "devbrain %s\n", version.String())
			return nil, 0
		case "--with":
			v, ok := next()
			if !ok || !setList(true, v) {
				return nil, 1
			}
		case "--without":
			v, ok := next()
			if !ok || !setList(false, v) {
				return nil, 1
			}
		case "--only":
			v, ok := next()
			if !ok {
				return nil, 1
			}
			for _, c := range allComponents {
				o.on[c] = false
				o.explicit[c] = true
			}
			if !setList(true, v) {
				return nil, 1
			}
		case "--yes", "-y":
			o.yes = true
		case "--with-gbrain", "--gbrain":
			o.gbrain = "1"
		case "--without-gbrain", "--no-gbrain":
			o.gbrain = "0"
		case "--open", "--open-dashboard":
			o.open = "1"
		case "--no-open", "--no-open-dashboard":
			o.open = "0"
		case "--dry-run", "-n":
			o.dryRun = true
		case "--explain":
			o.dryRun, o.explain = true, true
		case "--install-deps":
			o.installDeps = true
		default:
			fmt.Fprintf(errw, "install: unknown arg: %s (use --with/--without/--only <components>, --yes, --dry-run, --install-deps)\n", a)
			return nil, 1
		}
	}
	return o, -1
}

// ── the front door ────────────────────────────────────────────────────────────

// Run is `devbrain install`: legacy sweep, data-repo front phase (the old
// ./setup), then per-component machine wiring.
func Run(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	o, code := parseArgs(args, stderr)
	if o == nil {
		return code
	}
	c, err := newCtx(stdout, stderr, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}

	// Preview mode: resolve the data home read-only and print the plan without
	// touching anything (no migrate, no writes, no schedulers, no gbrain).
	if o.dryRun {
		c.data = config.DataDir()
		return c.preview(o)
	}

	// 0. Legacy sweep FIRST — recover the sed-pinned data path into config.json
	//    before the old copies are deleted, drop old hook entries/copies/plists.
	migrate(c, true)

	// 1. Resolve the data home: $DEVBRAIN_DATA > config.json > prompt (TTY) > default.
	c.data = config.DataDir()
	if os.Getenv("DEVBRAIN_DATA") == "" && !exists(config.Path()) && isTTY(os.Stdin) && !o.yes {
		fmt.Fprintf(stdout, "Where should devbrain store prompts + brain? [%s]: ", c.data)
		if reply := readLine(stdin); reply != "" {
			if strings.HasPrefix(reply, "~/") {
				reply = filepath.Join(c.home, reply[2:])
			}
			c.data = reply
		}
	}

	// macOS TCC guard: LaunchAgents (the flusher) are denied access under
	// ~/Desktop, ~/Documents, ~/Downloads — which silently breaks auto-sync.
	if rc := c.tccGuard(o); rc != 0 {
		return rc
	}

	fmt.Fprintf(stdout, "devbrain install (%s)\n", version.String())
	fmt.Fprintf(stdout, "  binary      : %s\n", c.bin)
	fmt.Fprintf(stdout, "  data home   : %s\n", c.data)

	// 2. Interactive component picker (TTY only, --yes skips, flags already decided).
	if isTTY(os.Stdin) && !o.yes {
		fmt.Fprintln(stdout, "Choose components (Enter keeps the default):")
		for _, comp := range allComponents {
			if o.explicit[comp] {
				continue
			}
			p := "y/N"
			if o.on[comp] {
				p = "Y/n"
			}
			fmt.Fprintf(stdout, "  %-14s [%s] ", comp, p)
			switch ans := strings.ToLower(readLine(stdin)); {
			case strings.HasPrefix(ans, "y"):
				o.on[comp] = true
			case strings.HasPrefix(ans, "n"):
				o.on[comp] = false
			}
		}
	}
	picked := pickedComponents(o)
	fmt.Fprintf(stdout, "  components  : %s\n", strings.Join(picked, " "))

	// 3. Data repo (the source of truth) — create/clone when missing.
	if err := c.ensureDataRepo(); err != nil {
		fmt.Fprintf(stderr, "install: %v\n", err)
		return 1
	}

	// 4. Optional gbrain engine (interactive offer only; silent skip otherwise).
	c.offerGbrain(o)

	// 5. Persist the resolved data dir — the hooks read this instead of a
	//    sed-pinned copy (relocate by re-running install with $DEVBRAIN_DATA).
	if err := config.Write(c.data); err != nil {
		fmt.Fprintf(stderr, "install: cannot write %s: %v\n", config.Path(), err)
		return 1
	}
	fmt.Fprintf(stdout, "  wrote data home -> %s\n", config.Path())

	// 6. Machine wiring, per component.
	if rc := c.wire(o); rc != 0 {
		return rc
	}

	// 7. First-run import seed (consent-gated; DEVBRAIN_NO_IMPORT=1 skips).
	c.firstRunImport()

	// 8. Rebuild the search index when gbrain is wired in.
	if o.gbrain != "0" && haveCmd("gbrain") {
		_ = brain.Rebuild(io.Discard, io.Discard)
		fmt.Fprintln(stdout, "  rebuilt the brain index (gbrain)")
	}

	c.summary(o)
	c.openDashboard(o)
	return 0
}

func pickedComponents(o *options) []string {
	var picked []string
	for _, comp := range allComponents {
		if o.on[comp] {
			picked = append(picked, comp)
		}
	}
	return picked
}

// preview prints every path install would create/modify for the selected
// components and exits without writing (--dry-run / --explain). It mirrors the
// wire() decisions; --explain adds a one-line why under each action.
func (c *ctx) preview(o *options) int {
	w := c.stdout
	picked := pickedComponents(o)
	fmt.Fprintf(w, "devbrain install --dry-run (%s) — nothing below is written\n", version.String())
	fmt.Fprintf(w, "  binary      : %s\n", c.bin)
	fmt.Fprintf(w, "  data home   : %s\n", c.data)
	fmt.Fprintf(w, "  components  : %s\n", strings.Join(picked, " "))

	line := func(verb, path, why string) {
		fmt.Fprintf(w, "  %-7s %s\n", verb, c.display(path))
		if o.explain && why != "" {
			fmt.Fprintf(w, "            ↳ %s\n", why)
		}
	}
	on := func(n string) bool { return o.on[n] }

	if exists(filepath.Join(c.data, ".git")) {
		line("exists", c.data, "data repo already initialized — left as-is")
	} else {
		line("create", c.data, "init or clone the private prompt + brain repo")
	}
	line("write", config.Path(), "records the resolved data home the hooks read")

	switch {
	case o.gbrain == "0":
		line("skip", "gbrain", "opted out — offline 'devbrain brain' search still works")
	case haveCmd("gbrain"):
		line("run", "gbrain init --pglite", "gbrain already present — init the local brain")
	case o.gbrain == "1" || o.installDeps:
		line("install", gbrainPackage(), "global 'bun add -g' (consented via --with-gbrain/--install-deps)")
	default:
		line("skip", gbrainPackage(), "global install gated — pass --install-deps (or --with-gbrain) to allow")
	}

	if on("capture") || on("response-trace") || on("nudge") {
		line("modify", filepath.Join(c.claude, "settings.json"), "register Claude hooks (capture/response/nudge)")
	}
	if on("codex") {
		line("modify", filepath.Join(c.codex, "hooks.json"), "register Codex hooks")
		line("modify", filepath.Join(c.codex, "config.toml"), "enable Codex 'hooks' feature")
		line("modify", filepath.Join(c.codex, "AGENTS.md"), "devbrain instruction block")
	}
	if on("flusher") {
		if c.goos == "darwin" {
			line("create", c.plistPath(), "launchd 5-min auto-flush job")
		} else {
			line("create", filepath.Join(c.home, ".config", "systemd", "user", "devbrain-flush.timer"), "systemd (or cron) 5-min auto-flush")
		}
	}
	if on("skills") {
		for _, d := range c.skillsDirs() {
			line("write", d, "install the bundled skills")
		}
	}
	if on("claude-md") {
		line("modify", filepath.Join(c.claude, "CLAUDE.md"), "devbrain instruction block + global-prefs @import")
	}
	if on("git-gate") {
		line("note", "git-gate", "sets core.hooksPath only when run inside a devbrain checkout")
	}
	if on("nightshift") {
		line("note", "nightshift", "toolset ships in the binary — no files written")
	}
	lb := filepath.Join(c.home, ".local", "bin")
	for _, alias := range backCompatAliases {
		line("link", filepath.Join(lb, alias), "back-compat alias -> the binary")
	}
	line("note", "first-run import", "previews existing history; seeds only on consent (fresh brain)")

	fmt.Fprintln(w, "  (dry run — re-run without --dry-run to apply)")
	return 0
}

func readLine(r io.Reader) string {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(string(line))
}

func haveCmd(name string) bool { _, err := exec.LookPath(name); return err == nil }

// tccGuard steers the data home out of macOS-protected folders. Interactive:
// offer ~/devbrain-data instead. Non-interactive: refuse (exit 1) — a silent
// broken flusher is worse than a failed install.
func (c *ctx) tccGuard(o *options) int {
	for _, d := range []string{"Desktop", "Documents", "Downloads"} {
		pref := filepath.Join(c.home, d) + string(os.PathSeparator)
		if !strings.HasPrefix(c.data, pref) {
			continue
		}
		fmt.Fprintf(c.stderr, "  ! %s is under a macOS-protected folder (Desktop/Documents/Downloads) —\n", c.data)
		fmt.Fprintf(c.stderr, "  ! the launchd flusher is denied access there, silently breaking auto-sync.\n")
		if isTTY(os.Stdin) && !o.yes {
			fmt.Fprintf(c.stdout, "Use ~/devbrain-data instead? [Y/n]: ")
			if ans := strings.ToLower(readLine(c.stdin)); !strings.HasPrefix(ans, "n") {
				c.data = filepath.Join(c.home, "devbrain-data")
			}
			return 0
		}
		fmt.Fprintf(c.stderr, "install: refusing — set DEVBRAIN_DATA to a path outside those folders and re-run.\n")
		return 1
	}
	return 0
}

// ensureDataRepo creates/clones the data repo when missing (the ./setup phase).
func (c *ctx) ensureDataRepo() error {
	if exists(filepath.Join(c.data, ".git")) {
		fmt.Fprintf(c.stdout, "  data repo   : exists (%s)\n", c.data)
		return nil
	}
	if remote := os.Getenv("DEVBRAIN_DATA_REMOTE"); remote != "" {
		if err := run("git", "clone", remote, c.data); err != nil {
			return fmt.Errorf("clone %s failed: %v", remote, err)
		}
		fmt.Fprintf(c.stdout, "  data repo   : cloned %s -> %s\n", remote, c.data)
		return nil
	}
	if err := os.MkdirAll(filepath.Join(c.data, "projects"), 0o755); err != nil {
		return err
	}
	if err := run("git", "-C", c.data, "init", "-q"); err != nil {
		return fmt.Errorf("git init %s failed: %v", c.data, err)
	}
	_ = os.WriteFile(filepath.Join(c.data, ".gitignore"), []byte("*.pglite\n.DS_Store\n"), 0o644)
	_ = os.WriteFile(filepath.Join(c.data, "README.md"),
		[]byte("# devbrain-data\n\nPrivate prompt log + brain. Source of truth — never lose it.\n"), 0o644)
	_ = run("git", "-C", c.data, "add", "-A")
	name := os.Getenv("DEVBRAIN_GIT_NAME")
	if name == "" {
		name = "devbrain"
	}
	email := os.Getenv("DEVBRAIN_GIT_EMAIL")
	if email == "" {
		email = "devbrain@localhost"
	}
	_ = run("git", "-C", c.data, "-c", "user.name="+name, "-c", "user.email="+email,
		"commit", "-qm", "init devbrain-data")
	fmt.Fprintf(c.stdout, "  data repo   : initialized fresh at %s\n", c.data)
	return nil
}

// offerGbrain: the optional ranked/semantic search engine. Offered only in a
// real terminal; non-interactive runs and bun-less machines skip silently
// (offline `devbrain brain search/get` works with zero engine).
func (c *ctx) offerGbrain(o *options) {
	if o.gbrain == "0" {
		fmt.Fprintln(c.stdout, "  gbrain      : skipped (opted out) — offline 'devbrain brain search/get' still works")
		return
	}
	if haveCmd("gbrain") {
		if err := run("gbrain", "init", "--pglite"); err == nil {
			fmt.Fprintln(c.stdout, "  gbrain      : present — local brain ready (PGLite)")
		} else {
			fmt.Fprintln(c.stdout, "  gbrain      : present (init failed — non-fatal)")
		}
		return
	}
	pkg := gbrainPackage()
	// A global 'bun add -g' is a mutation outside devbrain's own footprint, so
	// it is opt-in: explicit consent (--with-gbrain/--install-deps) or a TTY yes.
	want := o.gbrain == "1" || o.installDeps
	if !want && isTTY(os.Stdin) && !o.yes {
		fmt.Fprintf(c.stdout, "  gbrain adds ranked + semantic search (global 'bun add -g %s'; decline and offline search still works).\n", pkg)
		fmt.Fprintf(c.stdout, "  Install gbrain now? [Y/n]: ")
		want = !strings.HasPrefix(strings.ToLower(readLine(c.stdin)), "n")
	}
	if !want || !haveCmd("bun") {
		return // silent skip: no bun, or consent not given
	}
	if run("bun", "add", "-g", pkg) == nil && haveCmd("gbrain") {
		_ = run("gbrain", "init", "--pglite")
		fmt.Fprintln(c.stdout, "  gbrain      : installed via bun — local brain ready")
	} else {
		fmt.Fprintln(c.stdout, "  gbrain      : install failed — fine, offline 'devbrain brain' search still works")
	}
}

// run executes a command with output discarded.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run()
}

// ── wiring ───────────────────────────────────────────────────────────────────

// hookSpec is one settings.json registration.
type hookSpec struct {
	event, matcher, hook string // hook = `devbrain hook <hook>`
	component            string
}

var hookSpecs = []hookSpec{
	{"UserPromptSubmit", "", "capture", "capture"},
	{"PostToolUse", "Bash", "gbrain", "capture"},
	{"Stop", "", "response", "response-trace"},
	{"SubagentStop", "", "subagent-response", "response-trace"},
	{"SessionEnd", "", "memory", "response-trace"},
	{"SessionStart", "startup|resume", "session-start", "nudge"},
}

// codex registers the same pipeline minus the SessionEnd memory mirror
// (Codex has no equivalent store) and the SubagentStop capture (Codex has no
// subagents), with the harness marker prefixed.
func codexSpec(s hookSpec) bool { return s.hook != "memory" && s.hook != "subagent-response" }

func (c *ctx) wire(o *options) int {
	on := func(name string) bool { return o.on[name] }

	// Claude hooks -> ~/.claude/settings.json (idempotent; backup first).
	if on("capture") || on("response-trace") || on("nudge") {
		settings := filepath.Join(c.claude, "settings.json")
		if err := os.MkdirAll(c.claude, 0o755); err != nil {
			fmt.Fprintf(c.stderr, "install: %v\n", err)
			return 1
		}
		backup(settings)
		for _, s := range hookSpecs {
			if !on(s.component) {
				continue
			}
			if err := jsonedit.RegisterHook(settings, s.event, s.matcher, c.bin+" hook "+s.hook); err != nil {
				fmt.Fprintf(c.stderr, "install: register %s hook: %v\n", s.event, err)
				return 1
			}
		}
		fmt.Fprintf(c.stdout, "  registered hooks -> %s (command: %s hook <event>)\n", settings, c.bin)
		if !pathResolvable(c.bin) {
			// A fixed path that PATH can't resolve breaks silently if the binary
			// later moves/is replaced (the harness never reports a hook failure).
			fmt.Fprintf(c.stdout, "  NOTE: %s is not on PATH — if you move or replace this binary, capture stops silently. Re-run 'devbrain install' or 'devbrain doctor --fix' after moving it (brew installs record a stable path).\n", c.bin)
		}
	}

	if on("codex") {
		if rc := c.wireCodex(o); rc != 0 {
			return rc
		}
	}

	if on("flusher") {
		c.wireFlusher()
	}

	if on("skills") {
		if err := c.installSkills(); err != nil {
			fmt.Fprintf(c.stderr, "install: skills: %v\n", err)
			return 1
		}
	}

	if on("claude-md") {
		if err := c.writeClaudeMd(); err != nil {
			fmt.Fprintf(c.stderr, "install: claude-md: %v\n", err)
			return 1
		}
		// Wire the @import for the global preferences page /distill maintains.
		if LinkPreferences(nil, io.Discard, io.Discard) == 0 {
			fmt.Fprintf(c.stdout, "  wired global-preferences @import -> %s\n", filepath.Join(c.claude, "CLAUDE.md"))
		}
	}

	if on("nightshift") {
		fmt.Fprintln(c.stdout, "  nightshift  : toolset ships inside the binary — run it via:  devbrain nightshift start <repo>")
	} else {
		fmt.Fprintln(c.stdout, "  nightshift  : off (--without nightshift / DEVBRAIN_NIGHTSHIFT=0)")
	}

	if on("git-gate") {
		c.wireGitGate()
	}

	// Back-compat aliases on ~/.local/bin (argv[0] aliasing in the binary
	// handles the devbrain-* names). No PATH mutation — brew owns PATH now.
	lb := filepath.Join(c.home, ".local", "bin")
	if err := os.MkdirAll(lb, 0o755); err == nil {
		for _, alias := range backCompatAliases {
			p := filepath.Join(lb, alias)
			_ = os.Remove(p)
			if os.Symlink(c.bin, p) == nil {
				fmt.Fprintf(c.stdout, "  linked %s -> %s (back-compat alias)\n", c.display(p), c.bin)
			}
		}
	}
	return 0
}

func (c *ctx) wireCodex(o *options) int {
	on := func(name string) bool { return o.on[name] }
	if on("capture") || on("response-trace") || on("nudge") {
		if err := os.MkdirAll(c.codex, 0o755); err != nil {
			fmt.Fprintf(c.stderr, "install: %v\n", err)
			return 1
		}
		hooksJSON := filepath.Join(c.codex, "hooks.json")
		if !exists(hooksJSON) {
			_ = os.WriteFile(hooksJSON, []byte("{}"), 0o644)
		}
		backup(hooksJSON)
		for _, s := range hookSpecs {
			if !on(s.component) || !codexSpec(s) {
				continue
			}
			cmd := "DEVBRAIN_HARNESS=codex " + c.bin + " hook " + s.hook
			if err := jsonedit.RegisterHook(hooksJSON, s.event, s.matcher, cmd); err != nil {
				fmt.Fprintf(c.stderr, "install: codex register %s hook: %v\n", s.event, err)
				return 1
			}
		}
		fmt.Fprintf(c.stdout, "  registered Codex hooks -> %s\n", hooksJSON)

		// Codex 0.138+ gates hook execution behind the `hooks` feature flag
		// (OFF by default) — enable it, TOML-safely, via codex itself.
		c.codexHooksEnabled = false
		if haveCmd("codex") {
			backup(filepath.Join(c.codex, "config.toml"))
			if run("codex", "features", "enable", "hooks") == nil {
				c.codexHooksEnabled = true
				fmt.Fprintf(c.stdout, "  enabled Codex 'hooks' feature -> %s (registered hooks now fire)\n", filepath.Join(c.codex, "config.toml"))
			} else {
				fmt.Fprintln(c.stdout, "  NOTE: enable Codex hooks yourself — run 'codex features enable hooks'")
			}
		} else {
			fmt.Fprintln(c.stdout, "  NOTE: codex not on PATH — run 'codex features enable hooks' so the registered hooks fire")
		}
		fmt.Fprintln(c.stdout, "  Codex may ask you to review/trust these hooks with /hooks on next startup")
	}
	if err := c.writeAgentsMd(); err != nil {
		fmt.Fprintf(c.stderr, "install: codex AGENTS.md: %v\n", err)
		return 1
	}
	return 0
}

// wireGitGate points a devbrain checkout's hooks at scripts/git-hooks so the
// pre-push gate runs the fast suite before pushes to main/nightshift. Only
// fires when the working directory IS a devbrain checkout; silent otherwise.
func (c *ctx) wireGitGate() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return
	}
	top := strings.TrimSpace(string(out))
	if top == "" || !exists(filepath.Join(top, "scripts", "git-hooks", "pre-push")) {
		return // not a devbrain checkout — skip silently
	}
	hp, _ := exec.Command("git", "-C", top, "config", "--local", "core.hooksPath").Output()
	if cur := strings.TrimSpace(string(hp)); cur != "" && cur != "scripts/git-hooks" {
		fmt.Fprintf(c.stdout, "  git-gate: skipped — %s already sets core.hooksPath=%s (left as-is)\n", top, cur)
		return
	}
	if run("git", "-C", top, "config", "core.hooksPath", "scripts/git-hooks") == nil {
		_ = os.MkdirAll(c.claude, 0o755)
		_ = os.WriteFile(filepath.Join(c.claude, ".git-gate-repo"), []byte(top+"\n"), 0o644)
		fmt.Fprintln(c.stdout, "  git-gate: pre-push runs the fast suite before pushing main/nightshift (bypass: git push --no-verify)")
	}
}

// firstRunImport offers to seed a FRESH brain from existing Claude Code
// history — preview always, apply only on interactive consent. A brain with
// content skips (reinstall-safe); DEVBRAIN_NO_IMPORT=1 skips entirely.
func (c *ctx) firstRunImport() {
	if os.Getenv("DEVBRAIN_NO_IMPORT") != "" {
		return
	}
	if brainHasContent(c.data) {
		fmt.Fprintln(c.stdout, "")
		fmt.Fprintln(c.stdout, "  brain already has content — skipping first-run import (this keeps reinstall safe).")
		fmt.Fprintln(c.stdout, "  to re-import deliberately:  devbrain import --apply")
		return
	}
	fmt.Fprintln(c.stdout, "")
	fmt.Fprintln(c.stdout, "  Fresh brain — devbrain import preview of existing history that can seed it:")
	preview := exec.Command(c.bin, "import", "--data", c.data)
	if out, err := preview.Output(); err == nil {
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			fmt.Fprintf(c.stdout, "    %s\n", l)
		}
	}
	if !isTTY(os.Stdin) {
		fmt.Fprintln(c.stdout, "  (non-interactive shell — not auto-seeding) seed later:  devbrain import --apply")
		return
	}
	fmt.Fprintf(c.stdout, "  Seed the brain from this history now? [Y/n] ")
	if strings.HasPrefix(strings.ToLower(readLine(c.stdin)), "n") {
		fmt.Fprintln(c.stdout, "  skipped — seed later:  devbrain import --apply")
		return
	}
	if run(c.bin, "import", "--data", c.data, "--apply") == nil {
		fmt.Fprintln(c.stdout, "  seeded. Run /distill (or /continue) per project to build searchable brain pages.")
	} else {
		fmt.Fprintln(c.stdout, "  import had an issue — run manually:  devbrain import --apply")
	}
}

func brainHasContent(data string) bool {
	entries, err := os.ReadDir(filepath.Join(data, "projects"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found := false
		_ = filepath.WalkDir(filepath.Join(data, "projects", e.Name()), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}

func (c *ctx) summary(o *options) {
	fmt.Fprintln(c.stdout, "Done.")
	if o.on["capture"] {
		fmt.Fprintln(c.stdout, "  capture is live on your NEXT prompt")
	}
	if o.on["nudge"] {
		fmt.Fprintln(c.stdout, "  nudge fires at the START of your next session (query-brain reminder)")
	}
	if o.on["flusher"] {
		fmt.Fprintln(c.stdout, "  flusher runs every 5 min (commits/pushes the data repo)")
	}
	if o.on["skills"] {
		fmt.Fprintln(c.stdout, "  skills: /continue, /work, /distill, /reconcile for Claude Code; $continue, $work, $distill, $reconcile for Codex (restart agent sessions to load them)")
	}
	if o.on["codex"] {
		if c.codexHooksEnabled {
			fmt.Fprintln(c.stdout, "  Codex: hooks feature enabled — restart Codex, then review/trust devbrain hooks with /hooks when prompted")
		} else {
			fmt.Fprintln(c.stdout, "  Codex: enable hooks yourself ('codex features enable hooks'), then restart Codex and trust them with /hooks")
		}
	}
	fmt.Fprintln(c.stdout, "  onboard older history anytime:  devbrain import --apply")
	fmt.Fprintln(c.stdout, "  uninstall: devbrain uninstall")
}

// openDashboard lands the user on the queue dashboard after an interactive
// install (never in CI / headless runs). Best-effort, detached.
func (c *ctx) openDashboard(o *options) {
	want := false
	switch o.open {
	case "1":
		want = true
	case "0":
		want = false
	default:
		want = isTTY(os.Stdout)
	}
	if !want {
		fmt.Fprintln(c.stdout, "  open the control plane anytime:  devbrain dashboard   (Board · Nightshift · Profile)")
		return
	}
	_ = os.MkdirAll(filepath.Join(c.data, "projects"), 0o755)
	cmd := exec.Command(c.bin, "dashboard")
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
		fmt.Fprintln(c.stdout, "  dashboard starting — it opens your browser (re-open later: devbrain dashboard)")
	}
}
