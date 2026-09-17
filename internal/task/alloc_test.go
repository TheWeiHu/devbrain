package task

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// prefixes collects the NNNN of every id and fails on a duplicate.
func prefixes(t *testing.T, ids []string) map[string]string {
	t.Helper()
	seen := map[string]string{}
	for _, id := range ids {
		n := id[:4]
		if prev, dup := seen[n]; dup {
			t.Errorf("duplicate prefix %s: %s and %s", n, prev, id)
		}
		seen[n] = id
	}
	return seen
}

func TestAllocIDConcurrentDistinctSlugs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "todo")
	// Pre-existing top id, plus an archived one that must stay counted and a
	// non-canonical name that must not take part in the scan.
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"0003-live.md", "archive/0005-moved.md", "archive/12-short.md", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const n = 32
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := AllocID(dir, "slug-"+strings.Repeat("x", i%5)+string(rune('a'+i%26)))
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = id
		}()
	}
	wg.Wait()
	if t.Failed() {
		return
	}
	seen := prefixes(t, ids)
	if len(seen) != n {
		t.Fatalf("got %d unique prefixes, want %d: %v", len(seen), n, ids)
	}
	for _, want := range []string{"0006", "0037"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("prefix %s missing — sequence should run 0006..0037 after archived 0005: %v", want, ids)
		}
	}
	if _, ok := seen["0004"]; ok {
		t.Errorf("0004 was issued: archived 0005 must be counted: %v", ids)
	}
	for _, id := range ids {
		if _, err := os.Stat(filepath.Join(dir, id+".md")); err != nil {
			t.Errorf("%s not reserved on disk: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, LockName)); err != nil {
		t.Errorf("lock file missing: %v", err)
	}
}

func TestAllocIDSkipsReservedSlot(t *testing.T) {
	dir := t.TempDir()
	// A writer that bypassed the lock already holds 0001-taken: the O_EXCL
	// create still moves past it rather than clobbering.
	if _, err := AllocID(dir, "taken"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0002-bypass.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := AllocID(dir, "next")
	if err != nil {
		t.Fatal(err)
	}
	if id != "0003-next" {
		t.Errorf("id = %q, want 0003-next", id)
	}
}
