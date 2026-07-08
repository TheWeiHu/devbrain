// Package contextpack builds a bounded, read-only briefing for an agent that is
// starting or resuming work on a project. It intentionally reads the markdown
// source of truth directly; it does not require embeddings, gbrain, or a worker.
package contextpack

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
	"github.com/TheWeiHu/devbrain/internal/task"
)

const (
	defaultMaxPages      = 6
	defaultMaxTodos      = 5
	defaultMaxLogEntries = 3
	maxPagesLimit        = 20
	maxTodosLimit        = 20
	maxLogEntriesLimit   = 10
)

// Options controls the context brief builder.
type Options struct {
	DataDir       string
	CWD           string
	Project       string
	Query         string
	MaxPages      int
	MaxTodos      int
	MaxLogEntries int
}

// Brief is the stable JSON shape for devbrain context.
type Brief struct {
	Project    string       `json:"project"`
	CWD        string       `json:"cwd,omitempty"`
	ProjectDir string       `json:"project_dir"`
	Query      string       `json:"query,omitempty"`
	Brain      BrainSummary `json:"brain"`
	TODO       TODOSummary  `json:"todo"`
	RawLogs    LogSummary   `json:"raw_logs"`
	Warnings   []string     `json:"warnings,omitempty"`
}

type BrainSummary struct {
	Count   int         `json:"count"`
	Matched int         `json:"matched"`
	Pages   []BrainPage `json:"pages"`
}

type BrainPage struct {
	Slug      string `json:"slug"`
	Title     string `json:"title,omitempty"`
	Excerpt   string `json:"excerpt,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Score     int    `json:"score,omitempty"`
}

type TODOSummary struct {
	ActiveCount int        `json:"active_count"`
	Tasks       []TODOTask `json:"tasks"`
}

type TODOTask struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
	Excerpt  string `json:"excerpt,omitempty"`
}

type LogSummary struct {
	FileCount int        `json:"file_count"`
	NewestAt  string     `json:"newest_at,omitempty"`
	Entries   []LogEntry `json:"entries"`
}

type LogEntry struct {
	At      string `json:"at"`
	File    string `json:"file"`
	Excerpt string `json:"excerpt"`
}

// Run handles devbrain context.
func Run(args []string, stdout, stderr io.Writer) int {
	opts := Options{}
	var jsonOut bool
	fs := flag.NewFlagSet("devbrain context", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.CWD, "cwd", "", "working directory used to resolve the project key")
	fs.StringVar(&opts.Project, "project", "", "project key to brief, overriding cwd resolution")
	fs.StringVar(&opts.Query, "query", "", "terms used to rank brain pages")
	fs.IntVar(&opts.MaxPages, "max-pages", defaultMaxPages, "maximum brain pages to show")
	fs.IntVar(&opts.MaxTodos, "max-todos", defaultMaxTodos, "maximum active tasks to show")
	fs.IntVar(&opts.MaxLogEntries, "max-log-entries", defaultMaxLogEntries, "maximum recent raw log entries to show")
	fs.BoolVar(&jsonOut, "json", false, "emit machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: devbrain context [--cwd PATH] [--project KEY] [--query TEXT] [--max-pages N] [--max-todos N] [--max-log-entries N] [--json]

Print a compact, read-only project brief for agent startup/resume. The brief is
bounded and comes from on-disk brain pages, active TODOs, and recent raw logs.
`)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	brief, err := Build(opts)
	if err != nil {
		fmt.Fprintf(stderr, "devbrain context: %v\n", err)
		return 1
	}
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(brief); err != nil {
			fmt.Fprintf(stderr, "devbrain context: encode json: %v\n", err)
			return 1
		}
		return 0
	}
	RenderText(stdout, brief)
	return 0
}

// Build reads project context from the devbrain data repo.
func Build(opts Options) (Brief, error) {
	if opts.DataDir == "" {
		opts.DataDir = config.DataDir()
	}
	if opts.DataDir == "" {
		return Brief{}, errors.New("could not resolve devbrain data dir")
	}
	if opts.CWD == "" {
		if wd, err := os.Getwd(); err == nil {
			opts.CWD = wd
		}
	}
	project := projectkey.Sanitize(opts.Project)
	if project == "" {
		project = projectkey.ProjectKey(opts.CWD)
	}
	if project == "" {
		return Brief{}, errors.New("cwd is inside the devbrain data repo; pass --project to choose a project")
	}
	opts.MaxPages = normalizeLimit(opts.MaxPages, defaultMaxPages, maxPagesLimit)
	opts.MaxTodos = normalizeLimit(opts.MaxTodos, defaultMaxTodos, maxTodosLimit)
	opts.MaxLogEntries = normalizeLimit(opts.MaxLogEntries, defaultMaxLogEntries, maxLogEntriesLimit)

	projectDir := filepath.Join(opts.DataDir, "projects", project)
	brief := Brief{
		Project:    project,
		CWD:        opts.CWD,
		ProjectDir: projectDir,
		Query:      strings.TrimSpace(opts.Query),
	}
	if fi, err := os.Stat(projectDir); err != nil || !fi.IsDir() {
		brief.Warnings = append(brief.Warnings, "project directory not found in devbrain data")
	}
	brief.Brain = readBrain(projectDir, project, brief.Query, opts.MaxPages)
	brief.TODO = readTODO(projectDir, project, opts.MaxTodos)
	brief.RawLogs = readLogs(projectDir, opts.MaxLogEntries)
	if brief.Brain.Count == 0 {
		brief.Warnings = append(brief.Warnings, "no brain pages found; run /distill or /continue after capture")
	} else if brief.Query != "" && brief.Brain.Matched == 0 {
		brief.Warnings = append(brief.Warnings, "no brain pages matched --query")
	}
	if brief.RawLogs.FileCount == 0 {
		brief.Warnings = append(brief.Warnings, "no raw prompt logs found for this project")
	}
	return brief, nil
}

func normalizeLimit(n, fallback, cap int) int {
	if n <= 0 {
		n = fallback
	}
	if n > cap {
		return cap
	}
	return n
}

// RenderText prints the human-facing brief.
func RenderText(w io.Writer, brief Brief) {
	fmt.Fprintf(w, "devbrain context - project %s\n", brief.Project)
	if brief.Query != "" {
		fmt.Fprintf(w, "query: %s\n", brief.Query)
	}
	if len(brief.Warnings) > 0 {
		fmt.Fprintln(w, "\nWarnings:")
		for _, warning := range brief.Warnings {
			fmt.Fprintf(w, "- %s\n", warning)
		}
	}
	fmt.Fprintf(w, "\nBrain pages (%d total", brief.Brain.Count)
	if brief.Query != "" {
		fmt.Fprintf(w, ", %d matched", brief.Brain.Matched)
	}
	fmt.Fprintf(w, "; showing %d):\n", len(brief.Brain.Pages))
	for _, page := range brief.Brain.Pages {
		score := ""
		if page.Score > 0 {
			score = fmt.Sprintf(" score=%d", page.Score)
		}
		title := page.Title
		if title == "" {
			title = page.Slug
		}
		fmt.Fprintf(w, "- %s%s - %s\n", page.Slug, score, title)
		if page.Excerpt != "" {
			fmt.Fprintf(w, "  %s\n", page.Excerpt)
		}
	}
	if len(brief.Brain.Pages) == 0 {
		fmt.Fprintln(w, "- none")
	}

	fmt.Fprintf(w, "\nActive TODOs (%d total; showing %d):\n", brief.TODO.ActiveCount, len(brief.TODO.Tasks))
	for _, todo := range brief.TODO.Tasks {
		fmt.Fprintf(w, "- %s [%s p%d] %s\n", todo.ID, todo.Status, todo.Priority, todo.Title)
		if todo.Excerpt != "" {
			fmt.Fprintf(w, "  %s\n", todo.Excerpt)
		}
	}
	if len(brief.TODO.Tasks) == 0 {
		fmt.Fprintln(w, "- none")
	}

	fmt.Fprintf(w, "\nRecent raw log entries (%d file(s); showing %d):\n", brief.RawLogs.FileCount, len(brief.RawLogs.Entries))
	for _, entry := range brief.RawLogs.Entries {
		fmt.Fprintf(w, "- %s %s\n", entry.At, entry.File)
		if entry.Excerpt != "" {
			fmt.Fprintf(w, "  %s\n", entry.Excerpt)
		}
	}
	if len(brief.RawLogs.Entries) == 0 {
		fmt.Fprintln(w, "- none")
	}
}

type brainCandidate struct {
	BrainPage
	scoreTerms int
	lineHits   int
	updated    time.Time
}

func readBrain(projectDir, project, query string, limit int) BrainSummary {
	files, _ := filepath.Glob(filepath.Join(projectDir, "brain", "*.md"))
	sort.Strings(files)
	terms := searchTerms(query)
	out := BrainSummary{Count: len(files)}
	var candidates []brainCandidate
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		text := string(raw)
		distinct, hits := scoreText(text, terms)
		if len(terms) > 0 && distinct == 0 {
			continue
		}
		info, _ := os.Stat(file)
		updated := time.Time{}
		updatedAt := ""
		if info != nil {
			updated = info.ModTime().UTC()
			updatedAt = updated.Format(time.RFC3339)
		}
		score := 0
		if len(terms) > 0 {
			score = distinct*100 + hits
		}
		candidates = append(candidates, brainCandidate{
			BrainPage: BrainPage{
				Slug:      project + "/" + strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)),
				Title:     markdownTitle(text),
				Excerpt:   excerpt(text, terms),
				UpdatedAt: updatedAt,
				Score:     score,
			},
			scoreTerms: distinct,
			lineHits:   hits,
			updated:    updated,
		})
	}
	out.Matched = len(candidates)
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if len(terms) > 0 {
			if a.scoreTerms != b.scoreTerms {
				return a.scoreTerms > b.scoreTerms
			}
			if a.lineHits != b.lineHits {
				return a.lineHits > b.lineHits
			}
		}
		if !a.updated.Equal(b.updated) {
			return a.updated.After(b.updated)
		}
		return a.Slug < b.Slug
	})
	for _, c := range candidates[:min(limit, len(candidates))] {
		out.Pages = append(out.Pages, c.BrainPage)
	}
	return out
}

func readTODO(projectDir, project string, limit int) TODOSummary {
	files, _ := filepath.Glob(filepath.Join(projectDir, "todo", "*.md"))
	sort.Strings(files)
	var tasks []TODOTask
	for _, file := range files {
		t, err := task.Load(file, project)
		if err != nil || t.Status == "done" {
			continue
		}
		tasks = append(tasks, TODOTask{
			ID:       t.ID,
			Title:    cleanLine(t.Title),
			Status:   t.Status,
			Priority: t.Priority,
			Excerpt:  excerpt(t.Body, nil),
		})
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if statusRank(a.Status) != statusRank(b.Status) {
			return statusRank(a.Status) < statusRank(b.Status)
		}
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.ID < b.ID
	})
	out := TODOSummary{ActiveCount: len(tasks)}
	out.Tasks = append(out.Tasks, tasks[:min(limit, len(tasks))]...)
	return out
}

func statusRank(status string) int {
	switch status {
	case "open":
		return 0
	case "taken":
		return 1
	case "review":
		return 2
	case "held":
		return 3
	default:
		return 4
	}
}

type logCandidate struct {
	LogEntry
	sortKey string
}

func readLogs(projectDir string, limit int) LogSummary {
	files, _ := filepath.Glob(filepath.Join(projectDir, "log", "*", "*.md"))
	sort.Strings(files)
	out := LogSummary{FileCount: len(files)}
	var entries []logCandidate
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		day := filepath.Base(filepath.Dir(file))
		for _, entry := range parseLogEntries(day, filepath.Base(file), string(raw)) {
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].sortKey != entries[j].sortKey {
			return entries[i].sortKey > entries[j].sortKey
		}
		return entries[i].File < entries[j].File
	})
	for _, entry := range entries[:min(limit, len(entries))] {
		out.Entries = append(out.Entries, entry.LogEntry)
	}
	if len(out.Entries) > 0 {
		out.NewestAt = out.Entries[0].At
	}
	return out
}

func parseLogEntries(day, file, content string) []logCandidate {
	lines := strings.Split(content, "\n")
	var out []logCandidate
	var at, sortKey string
	var body []string
	flush := func() {
		if at == "" {
			return
		}
		out = append(out, logCandidate{
			LogEntry: LogEntry{
				At:      at,
				File:    file,
				Excerpt: excerpt(strings.Join(body, "\n"), nil),
			},
			sortKey: sortKey,
		})
	}
	for _, line := range lines {
		if t, ok := logHeadingTime(line); ok {
			flush()
			at = day + "T" + t + "Z"
			sortKey = day + "T" + t
			body = body[:0]
			continue
		}
		if at != "" {
			body = append(body, line)
		}
	}
	flush()
	return out
}

func logHeadingTime(line string) (string, bool) {
	if !strings.HasPrefix(line, "## ") {
		return "", false
	}
	t := strings.TrimSpace(strings.TrimPrefix(line, "## "))
	parts := strings.Split(t, ":")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if len(part) != 2 {
			return "", false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return "", false
		}
	}
	return t, true
}

func markdownTitle(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return cleanLine(strings.TrimSpace(strings.TrimPrefix(line, "# ")))
		}
		if strings.HasPrefix(line, "## ") {
			return cleanLine(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		}
	}
	return ""
}

func excerpt(text string, terms []string) string {
	lines := strings.Split(text, "\n")
	if len(terms) > 0 {
		var headingMatch string
		for _, line := range lines {
			if lineMatches(line, terms) {
				cleaned := truncate(cleanLine(line), 240)
				if strings.HasPrefix(strings.TrimSpace(line), "#") {
					if headingMatch == "" {
						headingMatch = cleaned
					}
					continue
				}
				return cleaned
			}
		}
		if headingMatch != "" {
			return headingMatch
		}
	}
	for _, line := range lines {
		cleaned := cleanLine(line)
		if cleaned == "" || strings.HasPrefix(cleaned, "devbrain Stage A raw prompt log") {
			continue
		}
		if strings.HasPrefix(cleaned, "agent: ") || strings.HasPrefix(cleaned, "cost: ") {
			continue
		}
		return truncate(cleaned, 240)
	}
	return ""
}

func cleanLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#>*- \t")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-3]) + "..."
}

func searchTerms(query string) []string {
	seen := map[string]bool{}
	var terms []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		term := strings.ToLower(b.String())
		b.Reset()
		if len(term) <= 2 || seen[term] || stopword(term) {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return terms
}

func stopword(s string) bool {
	switch s {
	case "and", "the", "for", "with", "that", "this", "from", "your", "you", "are", "not", "how", "does", "into":
		return true
	default:
		return false
	}
}

func scoreText(text string, terms []string) (int, int) {
	if len(terms) == 0 {
		return 0, 0
	}
	seen := map[string]bool{}
	hits := 0
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		lineHit := false
		for _, term := range terms {
			if strings.Contains(lower, term) {
				seen[term] = true
				lineHit = true
			}
		}
		if lineHit {
			hits++
		}
	}
	return len(seen), hits
}

func lineMatches(line string, terms []string) bool {
	lower := strings.ToLower(line)
	for _, term := range terms {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}
