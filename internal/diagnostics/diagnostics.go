// Package diagnostics holds read-only health checks that explain where a
// project's context pipeline currently stands: capture logs, distill cursor,
// brain pages, gbrain source sync, and the Homebrew tap used for upgrades.
package diagnostics

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
)

var (
	lookPath       = exec.LookPath
	commandContext = exec.CommandContext
	entryRe        = regexp.MustCompile(`(?m)^## ([0-9]{2}:[0-9]{2}:[0-9]{2})\b`)
	timeRe         = regexp.MustCompile(`[0-9]{2}:[0-9]{2}:[0-9]{2}`)
	lastSyncRe     = regexp.MustCompile(`last sync\s+([^\s]+)`)
	conflictRe     = regexp.MustCompile(`(?m)^(<<<<<<<|=======|>>>>>>>)`)
)

// DataOptions selects which project to diagnose. Project is an explicit
// dashboard/CLI project key; DashboardURL may carry ?project=... instead.
type DataOptions struct {
	DataDir      string
	CWD          string
	Project      string
	DashboardURL string
}

// DataReport is the stable JSON shape for `devbrain doctor data`.
type DataReport struct {
	DataDir         string        `json:"data_dir"`
	CWD             string        `json:"cwd"`
	CWDProject      string        `json:"cwd_project"`
	SelectedProject string        `json:"selected_project"`
	ProjectSource   string        `json:"project_source"`
	ProjectMismatch bool          `json:"project_mismatch"`
	Raw             RawReport     `json:"raw"`
	Distill         DistillReport `json:"distill"`
	Brain           BrainReport   `json:"brain"`
	GBrain          GBrainReport  `json:"gbrain"`
	Warnings        []string      `json:"warnings"`
	Failures        []string      `json:"failures"`
	Diagnosis       string        `json:"diagnosis"`
}

type RawReport struct {
	LogDir      string `json:"log_dir"`
	Count       int    `json:"count"`
	NewestFile  string `json:"newest_file"`
	NewestDay   string `json:"newest_day"`
	NewestTime  string `json:"newest_time"`
	NewestEntry string `json:"newest_entry"`
}

type DistillReport struct {
	LedgerPath   string        `json:"ledger_path"`
	LedgerExists bool          `json:"ledger_exists"`
	Pending      []PendingFile `json:"pending"`
	PendingCount int           `json:"pending_count"`
}

type PendingFile struct {
	RelPath string `json:"rel_path"`
	Day     string `json:"day"`
	Newest  string `json:"newest"`
	Cursor  string `json:"cursor"`
}

type BrainReport struct {
	Dir   string `json:"dir"`
	Count int    `json:"count"`
}

type GBrainReport struct {
	Available     bool   `json:"available"`
	Binary        string `json:"binary"`
	SourcesOK     bool   `json:"sources_ok"`
	SourceHasData bool   `json:"source_has_data"`
	LastSync      string `json:"last_sync"`
	Output        string `json:"output"`
	Error         string `json:"error"`
}

// ReportData inspects a project's context state without mutating the data repo
// or the local gbrain index.
func ReportData(opts DataOptions) DataReport {
	data := opts.DataDir
	if data == "" {
		data = config.DataDir()
	}
	cwd := opts.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	dashProject := dashboardProject(opts.DashboardURL)
	cwdProject := projectkey.ProjectKey(cwd)
	selected, source := selectedProject(opts.Project, dashProject, cwdProject)
	r := DataReport{
		DataDir:         data,
		CWD:             cwd,
		CWDProject:      cwdProject,
		SelectedProject: selected,
		ProjectSource:   source,
		ProjectMismatch: selected != "" && cwdProject != "" && selected != cwdProject,
		Warnings:        []string{},
		Failures:        []string{},
	}
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() {
		r.Failures = append(r.Failures, "data repo not found")
		r.Diagnosis = "data repo not found; capture and brain state cannot be inspected"
		return r
	}
	if selected == "" {
		r.Failures = append(r.Failures, "no project selected")
		r.Diagnosis = "no project selected; run from a project checkout or pass --project"
		return r
	}

	r.Raw = rawReport(data, selected)
	r.Distill = distillReport(data, selected)
	r.Brain = brainReport(data, selected)
	r.GBrain = gbrainReport(data)

	if r.ProjectMismatch {
		r.Warnings = append(r.Warnings, "selected project differs from cwd project")
	}
	if r.Raw.Count == 0 {
		r.Warnings = append(r.Warnings, "no raw logs found for selected project")
	}
	if r.Distill.PendingCount > 0 {
		r.Warnings = append(r.Warnings, "distill has pending raw log files")
	}
	if !r.GBrain.Available {
		r.Warnings = append(r.Warnings, "gbrain not on PATH")
	} else if r.GBrain.SourcesOK && !r.GBrain.SourceHasData {
		r.Warnings = append(r.Warnings, "gbrain sources do not mention the devbrain data repo")
	}
	r.Diagnosis = diagnoseData(r)
	return r
}

func dashboardProject(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := urlParse(raw)
	if err != nil {
		return ""
	}
	return projectkey.Sanitize(u.Query().Get("project"))
}

// urlParse accepts full URLs and raw query strings such as "?project=x".
func urlParse(raw string) (*url.URL, error) {
	if strings.HasPrefix(raw, "?") {
		raw = "http://127.0.0.1/" + raw
	}
	return url.Parse(raw)
}

func selectedProject(explicit, dashboard, cwd string) (string, string) {
	switch {
	case explicit != "":
		return projectkey.Sanitize(explicit), "project"
	case dashboard != "":
		return dashboard, "dashboard-url"
	default:
		return cwd, "cwd"
	}
}

type rawFile struct {
	path, rel, day, newest string
}

func rawFiles(data, project string) []rawFile {
	logDir := filepath.Join(data, "projects", project, "log")
	files, _ := filepath.Glob(filepath.Join(logDir, "*", "*.md"))
	sort.Strings(files)
	out := make([]rawFile, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		matches := entryRe.FindAllStringSubmatch(string(b), -1)
		newest := ""
		if len(matches) > 0 {
			newest = matches[len(matches)-1][1]
		}
		rel, _ := filepath.Rel(logDir, f)
		out = append(out, rawFile{path: f, rel: rel, day: filepath.Base(filepath.Dir(f)), newest: newest})
	}
	return out
}

func rawReport(data, project string) RawReport {
	logDir := filepath.Join(data, "projects", project, "log")
	r := RawReport{LogDir: logDir}
	for _, f := range rawFiles(data, project) {
		r.Count++
		if f.newest == "" {
			continue
		}
		entry := f.day + " " + f.newest
		if entry > r.NewestEntry {
			r.NewestEntry = entry
			r.NewestDay = f.day
			r.NewestTime = f.newest
			r.NewestFile = f.rel
		}
	}
	return r
}

func distillReport(data, project string) DistillReport {
	ledger := filepath.Join(data, "projects", project, "distilled.md")
	r := DistillReport{LedgerPath: ledger, Pending: []PendingFile{}}
	b, err := os.ReadFile(ledger)
	if err == nil {
		r.LedgerExists = true
	}
	ledgerText := string(b)
	for _, f := range rawFiles(data, project) {
		if f.newest == "" {
			continue
		}
		rec := ledgerCursor(ledgerText, f.rel)
		if rec == "" || f.newest > rec {
			r.Pending = append(r.Pending, PendingFile{RelPath: f.rel, Day: f.day, Newest: f.newest, Cursor: rec})
		}
	}
	r.PendingCount = len(r.Pending)
	return r
}

func ledgerCursor(ledger, rel string) string {
	var cursor string
	for _, line := range strings.Split(ledger, "\n") {
		if !strings.Contains(line, rel) {
			continue
		}
		times := timeRe.FindAllString(line, -1)
		if len(times) > 0 {
			cursor = times[len(times)-1]
		}
	}
	return cursor
}

func brainReport(data, project string) BrainReport {
	dir := filepath.Join(data, "projects", project, "brain")
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	return BrainReport{Dir: dir, Count: len(files)}
}

func gbrainReport(data string) GBrainReport {
	name := os.Getenv("DEVBRAIN_GBRAIN")
	if name == "" {
		name = "gbrain"
	}
	gb, err := lookPath(name)
	if err != nil {
		return GBrainReport{Available: false, Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := commandContext(ctx, gb, "sources", "list").CombinedOutput()
	text := strings.TrimSpace(string(out))
	r := GBrainReport{Available: true, Binary: gb, Output: text}
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.SourcesOK = true
	dataAbs := resolve(data)
	r.SourceHasData = strings.Contains(text, data) || strings.Contains(text, dataAbs)
	if m := lastSyncRe.FindStringSubmatch(text); m != nil {
		r.LastSync = m[1]
	}
	return r
}

func diagnoseData(r DataReport) string {
	if len(r.Failures) > 0 {
		return r.Failures[0]
	}
	if r.ProjectMismatch {
		if r.Raw.Count > 0 {
			return "capture logs exist, but the selected project differs from the cwd-derived project"
		}
		return "selected project differs from cwd-derived project and no raw logs were found there"
	}
	if r.Raw.Count == 0 {
		return "no raw logs found for the selected project; capture may be routed elsewhere or not wired"
	}
	if r.Distill.PendingCount > 0 {
		return "capture logs exist; distill has pending files to fold into brain pages"
	}
	if r.Brain.Count == 0 {
		return "capture logs exist; no brain pages were found for the selected project"
	}
	if !r.GBrain.Available {
		return "capture logs and brain pages exist; gbrain is unavailable so search may use only fallback reads"
	}
	if r.GBrain.SourcesOK && !r.GBrain.SourceHasData {
		return "capture logs and brain pages exist; gbrain sources do not mention the devbrain data repo"
	}
	return "context diagnostics look healthy for the selected project"
}

// BrewReport is the stable JSON shape for `devbrain doctor brew`.
type BrewReport struct {
	Available     bool     `json:"available"`
	BrewPath      string   `json:"brew_path"`
	Installed     string   `json:"installed"`
	TapRepo       string   `json:"tap_repo"`
	FormulaPath   string   `json:"formula_path"`
	FormulaDirty  bool     `json:"formula_dirty"`
	TapDirty      bool     `json:"tap_dirty"`
	TapConflicted bool     `json:"tap_conflicted"`
	InfoOK        bool     `json:"info_ok"`
	InfoOutput    string   `json:"info_output"`
	Warnings      []string `json:"warnings"`
	Failures      []string `json:"failures"`
	Remediation   string   `json:"remediation"`
	Diagnosis     string   `json:"diagnosis"`
}

// ReportBrew inspects the local Homebrew tap without running update, upgrade,
// reset, or any other mutating Brew/Git command.
func ReportBrew() BrewReport {
	brew, err := lookPath("brew")
	if err != nil {
		return BrewReport{Available: false, Warnings: []string{"brew not found"}, Failures: []string{}, Diagnosis: "Homebrew not found; brew diagnostics skipped"}
	}
	r := BrewReport{Available: true, BrewPath: brew, Warnings: []string{}, Failures: []string{}}
	r.Installed, _ = runTrim(5*time.Second, brew, "list", "--versions", "devbrain")
	tap, err := runTrim(5*time.Second, brew, "--repo", "theweihu/devbrain")
	if err != nil {
		r.Failures = append(r.Failures, "could not resolve theweihu/devbrain tap")
		r.Diagnosis = "Homebrew is installed, but the devbrain tap could not be resolved"
		return r
	}
	r.TapRepo = tap
	r.FormulaPath = filepath.Join(tap, "Formula", "devbrain.rb")
	formula, err := os.ReadFile(r.FormulaPath)
	if err != nil {
		r.Failures = append(r.Failures, "could not read devbrain formula")
		r.Diagnosis = "Homebrew tap exists, but Formula/devbrain.rb could not be read"
		return r
	}
	text := string(formula)
	r.TapConflicted = conflictRe.MatchString(text)
	if r.TapConflicted {
		r.Failures = append(r.Failures, "devbrain formula contains unresolved merge-conflict markers")
		r.Remediation = "If you have no local tap edits to keep: git -C " + shellQuote(tap) + " fetch origin main && git -C " + shellQuote(tap) + " reset --hard origin/main && brew update"
		r.Diagnosis = "local Homebrew tap is conflicted; brew info/upgrade may fail before reaching devbrain"
		return r
	}
	if status, err := runTrim(5*time.Second, "git", "-C", tap, "status", "--porcelain"); err == nil && status != "" {
		r.TapDirty = true
		if formulaStatus, _ := runTrim(5*time.Second, "git", "-C", tap, "status", "--porcelain", "--", "Formula/devbrain.rb"); formulaStatus != "" {
			r.FormulaDirty = true
			r.Warnings = append(r.Warnings, "devbrain formula has local modifications")
			r.Remediation = "If you have no local tap edits to keep: git -C " + shellQuote(tap) + " fetch origin main && git -C " + shellQuote(tap) + " reset --hard origin/main && brew update"
		} else {
			r.Warnings = append(r.Warnings, "Homebrew tap has local modifications")
		}
	}
	info, err := runTrim(10*time.Second, brew, "info", "theweihu/devbrain/devbrain")
	r.InfoOutput = info
	if err != nil {
		r.Failures = append(r.Failures, "brew info theweihu/devbrain/devbrain failed")
		r.Diagnosis = "brew could not inspect the devbrain formula"
		return r
	}
	r.InfoOK = true
	if r.FormulaDirty {
		r.Diagnosis = "devbrain formula is locally modified; brew may not reflect the upstream release cleanly"
	} else if r.TapDirty {
		r.Diagnosis = "Homebrew tap has local modifications, but devbrain formula is parseable"
	} else {
		r.Diagnosis = "Homebrew tap looks parseable for devbrain"
	}
	return r
}

func runTrim(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := commandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func resolve(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
