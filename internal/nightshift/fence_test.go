package nightshift

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// WriteOnlySet records a fixed-set run's scope and clears it for a full-drain
// run, so a stale file from a prior --only run never mis-scopes the next.
func TestWriteOnlySet(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".nightshift"), 0o755)
	f := filepath.Join(dir, ".nightshift", "only.txt")

	fixed := NewOrch(Options{Repo: dir, FixedSet: true, Only: "0001,0003-gamma"}, io.Discard)
	fixed.WriteOnlySet()
	if b, _ := os.ReadFile(f); string(b) != "0001,0003-gamma\n" {
		t.Fatalf("fixed-set only.txt = %q", b)
	}

	NewOrch(Options{Repo: dir}, io.Discard).WriteOnlySet() // full-drain clears it
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatalf("full-drain run must clear only.txt, stat err = %v", err)
	}
}

func TestNormalizeOnly(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"0001,0002", []string{"0001", "0002"}},
		{" 0001 , 0002 ", []string{"0001", "0002"}},
		{"", nil},
		{" , , ", nil},
		{",,0003-x,,", []string{"0003-x"}},
		// bash strips whitespace WITHIN a token (comma is the only separator)
		{"00 01", []string{"0001"}},
	}
	for _, c := range cases {
		if got := NormalizeOnly(c.raw); !reflect.DeepEqual(got, c.want) {
			t.Errorf("NormalizeOnly(%q) = %v want %v", c.raw, got, c.want)
		}
	}
}

func TestResolveOnly(t *testing.T) {
	ids := []string{"0001-alpha", "0002-beta", "0010-gamma"}
	cases := []struct {
		toks          []string
		resolved, unk []string
	}{
		{[]string{"0001-alpha"}, []string{"0001-alpha"}, nil},                // full slug
		{[]string{"0002"}, []string{"0002-beta"}, nil},                       // bare number
		{[]string{"9999"}, nil, []string{"9999"}},                            // unknown
		{[]string{"0001", "7777"}, []string{"0001-alpha"}, []string{"7777"}}, // mixed
		{[]string{"0010-wrong-slug"}, []string{"0010-gamma"}, nil},           // number side wins
	}
	for _, c := range cases {
		r, u := ResolveOnly(c.toks, ids)
		if !reflect.DeepEqual(r, c.resolved) || !reflect.DeepEqual(u, c.unk) {
			t.Errorf("ResolveOnly(%v) = (%v,%v) want (%v,%v)", c.toks, r, u, c.resolved, c.unk)
		}
	}
}

func TestInOnly(t *testing.T) {
	only := "0002-beta,0003"
	cases := []struct {
		id   string
		want bool
	}{
		{"0002-beta", true},  // full slug
		{"0002", true},       // bare number vs slug token
		{"0003-gamma", true}, // slug vs bare-number token
		{"0003", true},
		{"0001-alpha", false},
		{"0001", false},
	}
	for _, c := range cases {
		if got := InOnly(only, c.id); got != c.want {
			t.Errorf("InOnly(%q, %q) = %v want %v", only, c.id, got, c.want)
		}
	}
	if InOnly("", "0001-alpha") {
		t.Error("empty set contains nothing")
	}
}

func TestListParsers(t *testing.T) {
	open := "queue: p\n  [ 90] 0001-alpha                       Build the alpha thing\n  [ 80] 0002-beta                        Wire beta"
	if got := listIDs(open); !reflect.DeepEqual(got, []string{"0001-alpha", "0002-beta"}) {
		t.Errorf("listIDs = %v", got)
	}
	all := "queue: p (all)\n  [ 90] open    0001-alpha   A\n  [ 80] held    0002-beta    B"
	rows := listStatusIDs(all)
	want := [][2]string{{"open", "0001-alpha"}, {"held", "0002-beta"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("listStatusIDs = %v", rows)
	}
	// a title containing an NNNN-word pattern is NOT mistaken for an id
	trap := "  [ 90] open    0001-alpha   see 9999-imposter in the title"
	rows = listStatusIDs(trap)
	if len(rows) != 1 || rows[0][1] != "0001-alpha" {
		t.Errorf("title id-lookalike leaked: %v", rows)
	}

	st, id := matchRow(want, "0002")
	if st != "held" || id != "0002-beta" {
		t.Errorf("matchRow bare-number = (%s,%s)", st, id)
	}
	if st, _ := matchRow(want, "8888"); st != "" {
		t.Errorf("matchRow unknown should be empty, got %s", st)
	}
}
