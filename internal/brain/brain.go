// Package brain ports hooks/brain.sh: the brain query router that makes
// gbrain optional. gbrain on PATH -> transparent passthrough; otherwise an
// offline fallback implements the READ verbs (search/get/list) by grepping
// the on-disk pages, and index verbs become no-ops. It also ports
// scripts/rebuild-brain.sh (see rebuild.go).
package brain

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
)

// gbrainPath resolves the real gbrain binary ("" when absent). DEVBRAIN_GBRAIN
// overrides the command name/path so tests can inject a stub.
func gbrainPath() string {
	name := os.Getenv("DEVBRAIN_GBRAIN")
	if name == "" {
		name = "gbrain"
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

// Run routes one brain call: passthrough to gbrain when installed, else the
// offline fallback.
func Run(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	if gb := gbrainPath(); gb != "" {
		return passthrough(gb, args, stdout, stderr, stdin)
	}
	data, err := config.ResolveDataDir()
	if err != nil {
		fmt.Fprintf(stderr, "brain: %v\n", err)
		return 1
	}
	sub, rest := "", args
	if len(args) > 0 {
		sub, rest = args[0], args[1:]
	}
	switch sub {
	case "search", "query", "ask":
		return fallbackSearch(data, rest, stdout)
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

// ── offline fallback ─────────────────────────────────────────────────────────

// brainFiles ports `find $DATA/projects -type f -path '*/brain/*.md'`,
// sorted for determinism.
func brainFiles(data string) []string {
	var out []string
	filepath.WalkDir(filepath.Join(data, "projects"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") && strings.Contains(p, "/brain/") {
			out = append(out, p)
		}
		return nil
	})
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
// terms they hit, then total line hits, capped at 20, gbrain-shaped output.
func fallbackSearch(data string, args []string, stdout io.Writer) int {
	terms := searchTerms(strings.Join(args, " "))
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
	if len(hits) > 20 {
		hits = hits[:20]
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
