package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The recap corpus was run through the legacy devbrain_lib.py recap()/sample()
// to produce the golden; Recap and Sample must match byte-for-byte.
func TestRecapSampleGolden(t *testing.T) {
	t.Parallel()
	var cases []struct {
		Name  string   `json:"name"`
		Texts []string `json:"texts"`
	}
	var golds []struct {
		Name   string `json:"name"`
		Recap  string `json:"recap"`
		Sample string `json:"sample"`
	}
	readJSON(t, filepath.Join("..", "..", "testdata", "corpus", "recap-cases.json"), &cases)
	readJSON(t, filepath.Join("..", "..", "testdata", "golden", "recap.json"), &golds)
	if len(cases) == 0 || len(cases) != len(golds) {
		t.Fatalf("corpus/golden mismatch: %d vs %d", len(cases), len(golds))
	}
	for i, c := range cases {
		g := golds[i]
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			if c.Name != g.Name {
				t.Fatalf("case/golden name mismatch: %q vs %q", c.Name, g.Name)
			}
			if got := Recap(c.Texts); got != g.Recap {
				t.Errorf("Recap:\n got: %q\nwant: %q", got, g.Recap)
			}
			if got := Sample(c.Texts); got != g.Sample {
				t.Errorf("Sample:\n got: %q\nwant: %q", got, g.Sample)
			}
		})
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
