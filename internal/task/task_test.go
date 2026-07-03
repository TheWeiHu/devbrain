package task

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = "---\nid: 0001-x\nstatus: review\npriority: 70\ncreated: 2026-06-19T11:00:00Z\nclaimed_at: 2026-06-20T13:00:00Z\napproved: True\n---\n\n# X\n\nbody\n"

func TestParse(t *testing.T) {
	t.Parallel()
	tk := Parse(sample, "proj__a")
	if tk.ID != "0001-x" || tk.Project != "proj__a" || tk.Status != "review" ||
		tk.Priority != 70 || !tk.Approved || tk.Title != "X" || tk.Body != "body" {
		t.Errorf("bad parse: %+v", tk)
	}
	if tk.Raw("claimed_at") != "2026-06-20T13:00:00Z" {
		t.Errorf("Raw(claimed_at) = %q", tk.Raw("claimed_at"))
	}
	if tk.Raw("priority") != "70" || tk.Raw("nope") != "" {
		t.Error("Raw must return verbatim values, \"\" when absent")
	}
}

func TestParseStatusDefault(t *testing.T) {
	t.Parallel()
	// absent status defaults open; present-but-empty stays empty
	if got := Parse("---\nid: x\n---\n\n# T\n", "").Status; got != "open" {
		t.Errorf("absent status = %q, want open", got)
	}
	if got := Parse("---\nid: x\nstatus:\n---\n\n# T\n", "").Status; got != "" {
		t.Errorf("empty status = %q, want \"\"", got)
	}
}

func TestLoadIDFallback(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "0002-from-name.md")
	if err := os.WriteFile(p, []byte("---\nstatus: open\n---\n\n# T\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tk, err := Load(p, "proj__a")
	if err != nil {
		t.Fatal(err)
	}
	if tk.ID != "0002-from-name" {
		t.Errorf("id fallback = %q", tk.ID)
	}
}

func TestSameCheckout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if !SameCheckout(dir, dir) || !SameCheckout(link, dir) {
		t.Errorf("same dir via symlink must match: %q vs %q", link, dir)
	}
	if SameCheckout(dir, t.TempDir()) {
		t.Error("different dirs must not match")
	}
	// A dead path never matches a different live one (raw-equal still does).
	gone := filepath.Join(dir, "gone")
	if SameCheckout(gone, dir) || !SameCheckout(gone, gone) {
		t.Error("nonexistent path: raw-equal only")
	}
}
