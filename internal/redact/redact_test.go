package redact

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func repoPath(t *testing.T, rel string) string {
	t.Helper()
	return filepath.Join("..", "..", rel)
}

// The redact corpus is piped through the legacy `devbrain_lib.py redact` as one
// blob; the golden is its exact output. Go must match byte-for-byte.
func TestRedactGolden(t *testing.T) {
	t.Parallel()
	in, err := os.ReadFile(repoPath(t, "testdata/corpus/redact.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(repoPath(t, "testdata/golden/redact.golden"))
	if err != nil {
		t.Fatal(err)
	}
	got := Redact(string(in))
	if got != string(want) {
		diffLines(t, string(want), got)
	}
}

func TestPromptFilterGolden(t *testing.T) {
	t.Parallel()
	cases := readJSONL(t, repoPath(t, "testdata/corpus/prompt-filter-cases.jsonl"))
	golds := readJSONL(t, repoPath(t, "testdata/golden/prompt-filter.jsonl"))
	if len(cases) != len(golds) {
		t.Fatalf("corpus/golden mismatch: %d vs %d", len(cases), len(golds))
	}
	for i, c := range cases {
		c, g := c, golds[i]
		t.Run(c["name"].(string), func(t *testing.T) {
			t.Parallel()
			if got, want := PromptFilter(c["text"].(string)), g["out"].(string); got != want {
				t.Errorf("got %q want %q", got, want)
			}
		})
	}
}

func TestRedactEmpty(t *testing.T) {
	t.Parallel()
	if Redact("") != "" {
		t.Error("empty must stay empty")
	}
	if IsSynthetic("") {
		t.Error("empty is not synthetic")
	}
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func diffLines(t *testing.T, want, got string) {
	t.Helper()
	w, g := splitLines(want), splitLines(got)
	n := len(w)
	if len(g) > n {
		n = len(g)
	}
	for i := 0; i < n; i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			t.Errorf("line %d:\n want %q\n got  %q", i+1, wl, gl)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
