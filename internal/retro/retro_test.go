package retro

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One fixture data dir exercises the whole report: journal cache, tokens,
// todo frontmatter, gbrain log — then many assertions against one Generate.
func fixture(t *testing.T) string {
	t.Helper()
	data := t.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(data, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("journal/2026-07-04.md", "**20260704**\n- devbrain: shipped `doctor` for silent capture-stops.\n- redlens: 13 PRs in one session.\n")
	mk("journal/2026-07-03.md", "**20260703**\n- devbrain: merged the nightshift batch <#213>.\n")
	mk("journal/2020-01-01.md", "**20200101**\n- devbrain: ancient, outside the window.\n")
	// 1M in-tokens of opus-4-8 = $5.00; 1M cache_read of fable-5 = $1.00
	mk("projects/theweihu__devbrain/tokens.jsonl",
		`{"ts":"2026-07-04T10:00:00Z","model":"claude-opus-4-8","in":1000000,"out":0,"cache_create":0,"cache_read":0}
{"ts":"2026-07-03T09:00:00Z","model":"claude-fable-5","in":0,"out":0,"cache_create":0,"cache_read":1000000}
{"ts":"2020-01-01T09:00:00Z","model":"claude-opus-4-8","in":9000000,"out":0,"cache_create":0,"cache_read":0}
`)
	mk("projects/theweihu__devbrain/log/2026-07-04/main.abc.md",
		"# log\n\n## 10:00:01\n\nhello\n\n## 11:00:02\n\nworld\n")
	mk("projects/theweihu__devbrain/todo/0001-x.md",
		"---\nid: 0001-x\nstatus: done\ncreated: 2026-07-03T08:00:00Z\ndone_at: 2026-07-04T09:00:00Z\n---\n\n# Ship the thing\n")
	mk("projects/theweihu__devbrain/todo/0002-y.md",
		"---\nid: 0002-y\nstatus: open\ncreated: 2026-07-04T08:00:00Z\n---\n\n# Still open\n")
	mk("projects/theweihu__devbrain/gbrain-queries.log",
		`{"ts":"2026-07-04T10:00:00Z","hits":1}
{"ts":"2026-07-04T10:01:00Z","hits":0}
{"ts":"2020-01-01T10:00:00Z","hits":1}
`)
	return data
}

func TestGenerate(t *testing.T) {
	data := fixture(t)
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	html, err := Generate(Opts{Data: data, Days: 30, Now: now})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		// header + stats (window-scoped: the 2020 rows are excluded everywhere)
		"2026-06-05 → 2026-07-05", "<b>2</b><span>prompts</span>", "<b>1</b><span>sessions</span>",
		"<b>$6</b><span>total spend</span>",           // $5 opus + $1 fable
		"<b>50.0%</b><span>brain hit rate · 2 queries", // 1 of 2 in-window
		// charts
		">devbrain</span>", ">opus-4-8</span>", ">fable-5</span>", "$5</span>", "$1</span>",
		// journal: both cached days verbatim, newest first, code + escaping intact
		"20260704", "20260703", "<code>doctor</code>", "the nightshift batch &lt;#213&gt;",
		"color:#58a6ff", "color:#3fb950", // pinned devbrain + redlens colors
		"<b>1</b><span>tasks shipped <small>(2 opened)</small></span>",
	}
	for _, w := range want {
		if !strings.Contains(html, w) {
			t.Errorf("output missing %q", w)
		}
	}
	if strings.Contains(html, "ancient, outside the window") {
		t.Error("out-of-window journal day leaked in")
	}
	if strings.Contains(html, "20200101") {
		t.Error("out-of-window date rendered")
	}
	// suggestion rules: opus share 5/6 = 83% ≥ 60%; opened(2) > shipped(1)
	if !strings.Contains(html, "83% of spend is opus-4-8") {
		t.Error("model-concentration suggestion missing")
	}
	if !strings.Contains(html, "<b>2 tasks opened vs 1 shipped</b>") {
		t.Error("backlog-grew suggestion missing")
	}

	// determinism: byte-identical on a second run
	html2, err := Generate(Opts{Data: data, Days: 30, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if html != html2 {
		t.Error("Generate is not deterministic")
	}
}

func TestRunWritesFile(t *testing.T) {
	data := fixture(t)
	out := filepath.Join(t.TempDir(), "r.html")
	var so, se strings.Builder
	if rc := Run([]string{"--data", data, "--out", out, "--no-open"}, &so, &se); rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, se.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "devbrain retro") {
		t.Error("written file missing page title")
	}
	if strings.TrimSpace(so.String()) != out {
		t.Errorf("stdout should print the output path, got %q", so.String())
	}
}
