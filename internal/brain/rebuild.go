package brain

import (
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
	data := config.DataDir()
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() {
		fmt.Fprintf(stdout, "data repo not found at %s — run ./setup to create your private devbrain-data there (or set $DEVBRAIN_DATA to where it lives)\n", data)
		return 1
	}
	fmt.Fprintf(stdout, "Loading brain pages from %s ...\n", data)
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
		fmt.Fprintf(stdout, "  put %s\n", slug)
	}
	fmt.Fprintln(stdout, "Embedding (incremental) ...")
	embed := exec.Command(gb, "embed", "--stale")
	embed.Stdout, embed.Stderr = io.Discard, io.Discard
	_ = embed.Run() // || true
	fmt.Fprintln(stdout, "Done. Verify:")
	fmt.Fprintln(stdout, "  gbrain list --tag devbrain")
	fmt.Fprintln(stdout, "  gbrain query \"how does devbrain handle concurrency\" --detail low")
	return 0
}
