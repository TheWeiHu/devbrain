package harvest

import (
	"os"
	"path/filepath"
	"testing"
)

// write a file at root/.context/rel with the given body.
func ctxWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, ".context", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func actions(res []result) map[string]string {
	m := map[string]string{}
	for _, r := range res {
		m[filepath.ToSlash(r.rel)] = r.action
	}
	return m
}

func TestHarvestSelectsDurableAndRedacts(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()

	// durable: named docs + anything under docs/
	ctxWrite(t, root, "PRODUCT.md", "the product, token sk-abcdefghijklmnopqrstuvwx here")
	ctxWrite(t, root, "migration-plan.md", "plan")
	ctxWrite(t, root, "design.md", "design")
	ctxWrite(t, root, "docs/overview.md", "overview")
	// ephemeral: wrong extension, scratch name, excluded subtrees
	ctxWrite(t, root, "verify.html", "<html>")
	ctxWrite(t, root, "scratch.md", "throwaway")
	ctxWrite(t, root, "backfill/proto.md", "proto")
	ctxWrite(t, root, "pull-requests/stub.md", "stub")

	res, err := harvest(root, data, "acme__widget", true)
	if err != nil {
		t.Fatal(err)
	}
	got := actions(res)

	for _, keep := range []string{"PRODUCT.md", "migration-plan.md", "design.md", "docs/overview.md"} {
		if got[keep] != "new" {
			t.Errorf("%s: want new, got %q", keep, got[keep])
		}
	}
	// non-durable files are listed as skip; whole ephemeral subtrees are pruned.
	for _, drop := range []string{"verify.html", "scratch.md"} {
		if got[drop] != "skip" {
			t.Errorf("%s: want skip, got %q", drop, got[drop])
		}
	}
	for _, pruned := range []string{"backfill/proto.md", "pull-requests/stub.md"} {
		if _, ok := got[pruned]; ok {
			t.Errorf("%s: ephemeral subtree should be pruned, got %q", pruned, got[pruned])
		}
	}

	// durable docs are mirrored under context/, secrets redacted
	body, err := os.ReadFile(filepath.Join(data, "projects", "acme__widget", "context", "PRODUCT.md"))
	if err != nil {
		t.Fatalf("mirrored PRODUCT.md missing: %v", err)
	}
	if want := "the product, token [REDACTED] here"; string(body) != want {
		t.Errorf("redaction: got %q want %q", string(body), want)
	}
	// ephemeral files are never written
	if _, err := os.Stat(filepath.Join(data, "projects", "acme__widget", "context", "backfill")); !os.IsNotExist(err) {
		t.Errorf("backfill subtree should not be mirrored")
	}
}

func TestHarvestDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	ctxWrite(t, root, "PRODUCT.md", "x")

	res, err := harvest(root, data, "k", false)
	if err != nil {
		t.Fatal(err)
	}
	if actions(res)["PRODUCT.md"] != "new" {
		t.Errorf("dry-run should still classify as new")
	}
	if _, err := os.Stat(filepath.Join(data, "projects", "k", "context", "PRODUCT.md")); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write")
	}
}

func TestHarvestIdempotentAndUpdate(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	ctxWrite(t, root, "PRODUCT.md", "v1")

	if _, err := harvest(root, data, "k", true); err != nil {
		t.Fatal(err)
	}
	// second run, unchanged → same
	res, _ := harvest(root, data, "k", true)
	if actions(res)["PRODUCT.md"] != "same" {
		t.Errorf("unchanged doc should be 'same', got %q", actions(res)["PRODUCT.md"])
	}
	// change source → updated
	ctxWrite(t, root, "PRODUCT.md", "v2")
	res, _ = harvest(root, data, "k", true)
	if actions(res)["PRODUCT.md"] != "updated" {
		t.Errorf("changed doc should be 'updated', got %q", actions(res)["PRODUCT.md"])
	}
	body, _ := os.ReadFile(filepath.Join(data, "projects", "k", "context", "PRODUCT.md"))
	if string(body) != "v2" {
		t.Errorf("dest not updated: %q", string(body))
	}
}

func TestHarvestNoContextDir(t *testing.T) {
	res, err := harvest(t.TempDir(), t.TempDir(), "k", true)
	if err != nil {
		t.Fatalf("missing .context should be a no-op, got %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected no results, got %v", res)
	}
}

func TestHarvestSizeCap(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	big := make([]byte, maxSize+1)
	for i := range big {
		big[i] = 'a'
	}
	ctxWrite(t, root, "design.md", string(big))
	res, _ := harvest(root, data, "k", true)
	if actions(res)["design.md"] != "skip" {
		t.Errorf("oversized doc should be skipped, got %q", actions(res)["design.md"])
	}
}
