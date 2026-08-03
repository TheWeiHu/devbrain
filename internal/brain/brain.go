// Package brain is devbrain's sole gbrain boundary. Retrieval is project-first
// when the engine is installed; otherwise an offline fallback implements the
// read verbs over on-disk pages. Index verbs become no-ops offline. It also
// ports scripts/rebuild-brain.sh (see rebuild.go).
package brain

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
)

// gbrainPath resolves the real gbrain binary ("" when absent). DEVBRAIN_GBRAIN
// is also the installer's 1/0 consent flag; other values override the command
// name/path so tests can inject a stub.
func gbrainPath() string {
	name := os.Getenv("DEVBRAIN_GBRAIN")
	if name == "" || name == "1" || name == "0" {
		name = "gbrain"
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

// Run is the one public brain entry point. Retrieval is current-project-first;
// --global preserves the engine's original all-project ordering. Other verbs
// pass through unchanged when gbrain is installed.
func Run(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	sub, rest := "", args
	if len(args) > 0 {
		sub, rest = args[0], args[1:]
	}
	retrieval := sub == "search" || sub == "query" || sub == "ask"
	global, cleanRest := stripGlobal(rest)
	if retrieval {
		args = append([]string{sub}, cleanRest...)
	}
	project := ""
	if retrieval && !global {
		cwd, _ := os.Getwd()
		project = projectkey.ProjectKey(cwd)
	}

	if gb := gbrainPath(); gb != "" {
		if retrieval && project != "" {
			return projectFirstPassthrough(gb, args, project, stdout, stderr, stdin)
		}
		return passthrough(gb, args, stdout, stderr, stdin)
	}
	data, err := config.ResolveDataDir()
	if err != nil {
		fmt.Fprintf(stderr, "brain: %v\n", err)
		return 1
	}
	switch sub {
	case "search", "query", "ask":
		return fallbackSearch(data, cleanRest, project, stdout)
	case "get":
		return fallbackGet(data, rest, stdout, stderr)
	case "put", "tag", "embed", "link", "import", "sync", "delete":
		// index ops are gbrain-only; on-disk pages are the source, so skipping is safe.
		return 0
	case "list":
		for _, f := range brainFiles(data) {
			fmt.Fprintln(stdout, slugOf(f))
		}
		return 0
	case "", "help", "--help", "-h":
		fmt.Fprintln(stdout, "brain — offline brain reader (gbrain not installed)")
		fmt.Fprintln(stdout, "  brain search <terms>     keyword search over on-disk pages")
		fmt.Fprintln(stdout, "  brain get <slug> [--fuzzy]  read a page")
		fmt.Fprintln(stdout, "  brain list               list page slugs")
		return 0
	default:
		fmt.Fprintf(stderr, "brain: '%s' needs gbrain; only search/get/list work offline\n", sub)
		return 0
	}
}

func stripGlobal(args []string) (bool, []string) {
	clean := make([]string, 0, len(args))
	global := false
	for _, arg := range args {
		if arg == "--global" {
			global = true
			continue
		}
		clean = append(clean, arg)
	}
	return global, clean
}

// passthrough hands the whole call to the real gbrain (exec gbrain "$@").
func passthrough(gb string, args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	cmd := exec.Command(gb, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 127
}

var resultHeaderRe = regexp.MustCompile(`^\[[0-9.]+\]\s+(\S+)\s+--`)

type engineResult struct {
	slug string
	text string
}

func projectFirstPassthrough(gb string, args []string, project string, stdout, stderr io.Writer, stdin io.Reader) int {
	limit := retrievalLimit(args[1:])
	fetchArgs := withRetrievalLimit(args, 100)
	var out bytes.Buffer
	code := passthrough(gb, fetchArgs, &out, stderr, stdin)
	results := parseEngineResults(out.String())
	if len(results) == 0 {
		_, _ = io.Copy(stdout, &out)
		return code
	}
	for _, result := range prioritizeEngineResults(results, project, limit) {
		_, _ = io.WriteString(stdout, result.text)
	}
	return code
}

func retrievalLimit(args []string) int {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--limit" {
			continue
		}
		n, err := strconv.Atoi(args[i+1])
		if err == nil && n > 0 {
			if n > 100 {
				return 100
			}
			return n
		}
	}
	return 20
}

func withRetrievalLimit(args []string, limit int) []string {
	out := append([]string(nil), args...)
	for i := 1; i+1 < len(out); i++ {
		if out[i] == "--limit" {
			out[i+1] = strconv.Itoa(limit)
			return out
		}
	}
	return append(out, "--limit", strconv.Itoa(limit))
}

func parseEngineResults(out string) []engineResult {
	var results []engineResult
	for _, line := range strings.SplitAfter(out, "\n") {
		if m := resultHeaderRe.FindStringSubmatch(line); m != nil {
			results = append(results, engineResult{slug: m[1], text: line})
			continue
		}
		if len(results) > 0 {
			results[len(results)-1].text += line
		}
	}
	return results
}

func prioritizeEngineResults(results []engineResult, project string, limit int) []engineResult {
	seen := make(map[string]bool)
	var localCurated, localLogs, crossCurated, crossLogs []engineResult
	for _, result := range results {
		if seen[result.slug] {
			continue
		}
		seen[result.slug] = true
		local := resultProject(result.slug) == project
		logPage := strings.Contains("/"+strings.TrimPrefix(result.slug, "projects/")+"/", "/log/")
		switch {
		case local && !logPage:
			localCurated = append(localCurated, result)
		case local:
			localLogs = append(localLogs, result)
		case !logPage:
			crossCurated = append(crossCurated, result)
		default:
			crossLogs = append(crossLogs, result)
		}
	}
	local := append(localCurated, localLogs...)
	cross := append(crossCurated, crossLogs...)
	return projectFirstSlice(local, cross, limit)
}

func resultProject(slug string) string {
	slug = strings.TrimPrefix(slug, "projects/")
	project, _, _ := strings.Cut(slug, "/")
	return project
}

func projectFirstSlice[T any](local, cross []T, limit int) []T {
	if limit <= 0 {
		return nil
	}
	crossN := min(2, len(cross), limit)
	if len(local) > 0 && crossN == limit {
		crossN--
	}
	localN := min(len(local), limit-crossN)
	out := make([]T, 0, localN+crossN)
	out = append(out, local[:localN]...)
	out = append(out, cross[:crossN]...)
	return out
}

// ── offline fallback ─────────────────────────────────────────────────────────

// brainFiles ports `find $DATA/projects -type f -path '*/brain/*.md'`,
// sorted for determinism.
func brainFiles(data string) []string {
	// The live namespace is deliberately narrow: projects/<project>/brain/*.md.
	// Archived pages also contain a /brain/ path component, but they are evidence,
	// not current context, and must never re-enter search during a rebuild.
	projectsRoot := filepath.Join(data, "projects")
	projects, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil
	}
	var out []string
	for _, project := range projects {
		if !project.IsDir() {
			continue
		}
		brainDir := filepath.Join(projectsRoot, project.Name(), "brain")
		pages, err := os.ReadDir(brainDir)
		if err != nil {
			continue
		}
		for _, page := range pages {
			if !page.IsDir() && strings.HasSuffix(page.Name(), ".md") {
				out = append(out, filepath.Join(brainDir, page.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

// slugOf: projects/<project>/brain/<page>.md -> <project>/<page>.
func slugOf(f string) string {
	return filepath.Base(filepath.Dir(filepath.Dir(f))) + "/" + strings.TrimSuffix(filepath.Base(f), ".md")
}

// stopwords is the tiny set the search tokenizer drops.
var stopwords = map[string]bool{
	"and": true, "the": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "your": true, "you": true, "are": true,
	"not": true, "how": true, "does": true, "into": true,
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

// searchTerms tokenizes the query like `tr -cs '[:alnum:]_' ' '`: split on
// runs of non-word bytes, lowercase, drop <=2-char tokens and stopwords.
func searchTerms(query string) []string {
	var terms []string
	i := 0
	for i < len(query) {
		if !isWordByte(query[i]) {
			i++
			continue
		}
		j := i
		for j < len(query) && isWordByte(query[j]) {
			j++
		}
		lc := strings.ToLower(query[i:j])
		i = j
		if len(lc) <= 2 || stopwords[lc] {
			continue
		}
		terms = append(terms, lc)
	}
	return terms
}

// fileLines splits like grep counts lines: a trailing newline does not add an
// empty final line.
func fileLines(content string) []string {
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// stripExcerpt is sed 's/^[[:space:]#>*-]*//'.
func stripExcerpt(s string) string {
	return strings.TrimLeft(s, " \t\v\f\r\n#>*-")
}

type hit struct {
	matched, score int
	slug, first    string
}

func (h hit) line() string {
	return fmt.Sprintf("%d\t%d\t%s\t%s", h.matched, h.score, h.slug, h.first)
}

// fallbackSearch: OR-keyword scoring — pages ranked by how many DISTINCT
// terms they hit, then total line hits, with the same project-first policy as
// the ranked engine.
func fallbackSearch(data string, args []string, project string, stdout io.Writer) int {
	limit := retrievalLimit(args)
	terms := searchTerms(strings.Join(withoutRetrievalOptions(args), " "))
	if len(terms) == 0 {
		fmt.Fprintln(stdout, "No results.")
		return 0
	}
	var hits []hit
	for _, f := range brainFiles(data) {
		b, err := os.ReadFile(f)
		if err != nil {
			continue // grep -c on an unreadable file -> 0 hits
		}
		lines := fileLines(string(b))
		lower := make([]string, len(lines))
		for i, l := range lines {
			lower[i] = strings.ToLower(l)
		}
		matched, score := 0, 0
		for _, t := range terms {
			c := 0
			for _, l := range lower {
				if strings.Contains(l, t) {
					c++
				}
			}
			if c > 0 {
				matched++
				score += c
			}
		}
		if matched == 0 {
			continue
		}
		// excerpt: first line containing any term, trimmed.
		first := ""
		for _, t := range terms {
			first = ""
			for i, l := range lower {
				if strings.Contains(l, t) {
					first = stripExcerpt(lines[i])
					break
				}
			}
			if first != "" {
				break
			}
		}
		hits = append(hits, hit{matched, score, slugOf(f), first})
	}
	// sort -k1,1rn -k2,2rn with sort's whole-line last-resort tie-break
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].matched != hits[j].matched {
			return hits[i].matched > hits[j].matched
		}
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].line() < hits[j].line()
	})
	if project != "" {
		var local, cross []hit
		for _, h := range hits {
			if resultProject(h.slug) == project {
				local = append(local, h)
			} else {
				cross = append(cross, h)
			}
		}
		hits = projectFirstSlice(local, cross, limit)
	} else if len(hits) > limit {
		hits = hits[:limit]
	}
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "No results.")
		return 0
	}
	for _, h := range hits {
		fmt.Fprintf(stdout, "[%d.%04d] %s -- %s\n", h.matched, h.score, h.slug, h.first)
	}
	return 0
}

func withoutRetrievalOptions(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--limit" && i+1 < len(args) {
			i++
			continue
		}
		if args[i] == "--offset" && i+1 < len(args) {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// fallbackGet reads a page by <project>/<page> slug; --fuzzy resolves a
// bare/near slug by unique basename, multiple matches -> "Did you mean".
func fallbackGet(data string, args []string, stdout, stderr io.Writer) int {
	fuzzy, slug := false, ""
	for _, a := range args {
		switch {
		case a == "--fuzzy":
			fuzzy = true
		case strings.HasPrefix(a, "--"):
			// ignored
		default:
			if slug == "" {
				slug = a
			}
		}
	}
	if slug == "" {
		fmt.Fprintln(stderr, "usage: brain get <project>/<page> [--fuzzy]")
		return 1
	}
	pagePath := func(s string) string { // ${s%%/*} / ${s#*/}
		proj, page := s, s
		if i := strings.Index(s, "/"); i >= 0 {
			proj, page = s[:i], s[i+1:]
		}
		return filepath.Join(data, "projects", proj, "brain", page+".md")
	}
	if b, err := os.ReadFile(pagePath(slug)); err == nil {
		stdout.Write(b)
		return 0
	}
	if fuzzy {
		page := slug
		if i := strings.LastIndex(slug, "/"); i >= 0 {
			page = slug[i+1:]
		}
		var hits []string
		for _, f := range brainFiles(data) {
			if strings.TrimSuffix(filepath.Base(f), ".md") == page {
				hits = append(hits, slugOf(f))
			}
		}
		if len(hits) == 1 {
			if b, err := os.ReadFile(pagePath(hits[0])); err == nil {
				stdout.Write(b)
			}
			return 0
		}
		if len(hits) > 0 {
			fmt.Fprintf(stdout, "page_not_found: %s\n", slug)
			fmt.Fprintln(stdout, "Did you mean:")
			for _, h := range hits {
				fmt.Fprintf(stdout, "  %s\n", h)
			}
			return 0
		}
	}
	fmt.Fprintf(stdout, "page_not_found: %s (gbrain not installed; offline read found no such page)\n", slug)
	return 0
}
