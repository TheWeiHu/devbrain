package brain

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
)

// Rebuild ports scripts/rebuild-brain.sh: re-put every on-disk brain page into
// gbrain (upsert by slug), tag it with its project, then embed incrementally.
// gbrain is optional — missing engine is a soft skip, not a failure.
func Rebuild(stdout, stderr io.Writer) int {
	gb := gbrainPath()
	if gb == "" {
		fmt.Fprintln(stdout, "gbrain not on PATH — skipping index rebuild (pages stay searchable offline via 'devbrain brain').")
		return 0
	}
	data, err := config.ResolveDataDir()
	if err != nil {
		fmt.Fprintf(stderr, "rebuild: %v\n", err)
		return 1
	}
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() {
		fmt.Fprintf(stdout, "data repo not found at %s — run ./setup to create your private devbrain-data there (or set $DEVBRAIN_DATA to where it lives)\n", data)
		return 1
	}
	fmt.Fprintf(stdout, "Loading brain pages from %s ...\n", data)
	pruned := 0
	for _, f := range brainFiles(data) {
		project := filepath.Base(filepath.Dir(filepath.Dir(f)))
		base := strings.TrimSuffix(filepath.Base(f), ".md")
		slug := project + "/" + strings.TrimPrefix(base, project+"-")
		in, err := os.Open(f)
		if err != nil {
			return 1 // bash: redirect failure under set -e
		}
		put := exec.Command(gb, "put", slug)
		put.Stdin, put.Stdout, put.Stderr = in, io.Discard, stderr
		err = put.Run()
		in.Close()
		if err != nil { // set -e: a failing put aborts the rebuild
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			return 1
		}
		tag := exec.Command(gb, "tag", slug, project)
		tag.Stdout, tag.Stderr = io.Discard, io.Discard
		_ = tag.Run() // || true
		// Prune the path-form TWIN a raw `gbrain import` of the data dir would
		// have created for this page — projects/<project>/brain/<page>. devbrain
		// owns the canonical <project>/<page> slug; the path-form is always a
		// duplicate, and gbrain surfaces both, splitting the page's count. Delete
		// is idempotent (a no-op when the twin was never created), so a clean
		// brain stays clean and a polluted one self-heals on the next rebuild.
		if rel, relErr := filepath.Rel(data, f); relErr == nil {
			if twin := strings.TrimSuffix(rel, ".md"); twin != slug {
				del := exec.Command(gb, "delete", twin)
				del.Stdout, del.Stderr = io.Discard, io.Discard
				if del.Run() == nil {
					pruned++
				}
			}
		}
		fmt.Fprintf(stdout, "  put %s\n", slug)
	}
	if pruned > 0 {
		fmt.Fprintf(stdout, "Pruned path-form twin slugs (projects/<project>/brain/*) for %d page(s).\n", pruned)
	}
	// Semantic ranking needs embeddings, which gbrain builds only with an OpenAI
	// key. Without one, embed --stale is a silent no-op and query falls back to
	// keyword — surface that so low relevance has a visible cause and fix.
	if hasOpenAIKey() {
		fmt.Fprintln(stdout, "Embedding (incremental) ...")
	} else {
		fmt.Fprintln(stdout, "No OpenAI key: index is keyword-only. Set OPENAI_API_KEY (or run")
		fmt.Fprintln(stdout, "'gbrain config set openai_api_key <key>') and re-run for semantic ranking.")
	}
	embed := exec.Command(gb, "embed", "--stale")
	embed.Stdout, embed.Stderr = io.Discard, io.Discard
	_ = embed.Run() // || true
	fmt.Fprintln(stdout, "Done. Verify:")
	fmt.Fprintln(stdout, "  gbrain list --tag devbrain")
	fmt.Fprintln(stdout, "  gbrain query \"how does devbrain handle concurrency\" --detail low")
	return 0
}

// hasOpenAIKey reports whether an OpenAI key is configured by either route
// gbrain honors: the OPENAI_API_KEY env var, or openai_api_key in
// ~/.gbrain/config.json. Mirrors the two paths documented in SECURITY.md.
func hasOpenAIKey() bool {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(home, ".gbrain", "config.json"))
	if err != nil {
		return false
	}
	var cfg struct {
		OpenAIAPIKey string `json:"openai_api_key"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	return strings.TrimSpace(cfg.OpenAIAPIKey) != ""
}
