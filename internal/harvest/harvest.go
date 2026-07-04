// Package harvest selectively mirrors durable .context/ workspace docs into the
// data repo. Conductor gives each workspace a gitignored, per-workspace
// .context/ dir where agents drop durable knowledge (PRODUCT.md, plans, design
// and decision notes) alongside ephemeral scratch (verification HTML, backfill
// prototypes). Nothing captures it, so those docs vanish when the workspace is
// deleted — a brain blind spot. This is the sibling of the memory/ harvest in
// internal/importer: same "durable side-channel artifact, ephemeral storage"
// shape, so it mirrors its structure — redact each file and copy it under
// $DATA/projects/<key>/context/. Folding these into brain pages stays the
// distill skill's job.
package harvest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TheWeiHu/devbrain/internal/config"
	"github.com/TheWeiHu/devbrain/internal/projectkey"
	"github.com/TheWeiHu/devbrain/internal/redact"
)

// maxSize caps a harvested file: durable docs are prose, not datasets, so
// anything larger is almost certainly a dump/scratch artifact we don't want.
const maxSize = 256 * 1024

// durablePats matches basenames (lowercased) that hold durable knowledge worth
// folding into the brain. Selectivity is the point — everything else under
// .context is treated as ephemeral scratch and skipped.
var durablePats = []string{
	"product.md",
	"*plan*.md",
	"design*.md", "*design*.md",
	"*decision*.md",
	"*spec*.md",
	"architecture*.md", "*architecture*.md",
	"notes.md",
}

// ephemeralDirs are .context subtrees that are known scratch (devbrain's own
// backfill prototype and pull-request stubs); their whole subtree is skipped.
var ephemeralDirs = map[string]bool{
	"backfill":      true,
	"pull-requests": true,
}

type result struct {
	rel    string
	action string // new | updated | same | skip
}

// durable reports whether a .context-relative markdown path is worth keeping:
// a durable-named file, or any .md under a docs/ segment. Only .md is ever
// considered, which structurally drops HTML verification pages and binaries.
func durable(rel string) bool {
	if strings.ToLower(filepath.Ext(rel)) != ".md" {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.EqualFold(seg, "docs") {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(rel))
	for _, pat := range durablePats {
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
	}
	return false
}

// harvest is the pure filesystem core: scan <root>/.context, and (when apply)
// mirror durable redacted docs into <dataDir>/projects/<key>/context. It never
// deletes and skips byte-identical writes, so it is idempotent and append-only.
func harvest(root, dataDir, key string, apply bool) ([]result, error) {
	src := filepath.Join(root, ".context")
	dstRoot := filepath.Join(dataDir, "projects", key, "context")
	var out []result
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, never block
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil || rel == "." {
			return nil
		}
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if info.IsDir() {
			if ephemeralDirs[top] {
				return filepath.SkipDir
			}
			return nil
		}
		if ephemeralDirs[top] {
			return nil
		}
		if !durable(rel) {
			out = append(out, result{rel, "skip"})
			return nil
		}
		if info.Size() > maxSize {
			out = append(out, result{rel, "skip"})
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		content := redact.Redact(string(raw))
		dst := filepath.Join(dstRoot, rel)
		action := "new"
		if prev, e := os.ReadFile(dst); e == nil {
			if string(prev) == content {
				out = append(out, result{rel, "same"})
				return nil
			}
			action = "updated"
		}
		if apply {
			if e := os.MkdirAll(filepath.Dir(dst), 0o755); e != nil {
				return e
			}
			if e := os.WriteFile(dst, []byte(content), 0o644); e != nil {
				return e
			}
		}
		out = append(out, result{rel, action})
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil // no .context/ here — nothing to harvest
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, err
}

// Run is the `devbrain harvest-context [--apply] [dir]` entrypoint. Default is
// a dry-run listing (mirrors `devbrain import`); --apply writes.
func Run(args []string, stdout, stderr io.Writer) int {
	apply := false
	dir := ""
	for _, a := range args {
		switch {
		case a == "--apply":
			apply = true
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, "usage: devbrain harvest-context [--apply] [dir]")
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "harvest-context: unknown flag: %s\n", a)
			return 2
		default:
			dir = a
		}
	}
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	key := projectkey.ProjectKey(dir)
	if key == "" {
		fmt.Fprintln(stderr, "harvest-context: could not resolve project key")
		return 1
	}
	res, err := harvest(dir, config.DataDir(), key, apply)
	if err != nil {
		fmt.Fprintf(stderr, "harvest-context: %v\n", err)
		return 1
	}
	var kept int
	for _, r := range res {
		fmt.Fprintf(stdout, "  %-8s %s\n", r.action, r.rel)
		if r.action != "skip" {
			kept++
		}
	}
	if apply {
		fmt.Fprintf(stdout, "harvest-context: %d durable doc(s) → projects/%s/context/\n", kept, key)
	} else {
		fmt.Fprintf(stdout, "DRY-RUN. %d durable doc(s). Re-run with --apply to write into projects/%s/context/.\n", kept, key)
	}
	return 0
}
