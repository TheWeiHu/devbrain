package retro

import (
	"encoding/json"
	"fmt"
	"math"
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
	// 1M in-tokens of opus-4-8 = $5.00; 1M cache_read of fable-5 = $1.00.
	// The two opus rows are the SAME turn re-captured by the Stop hook as it
	// grew — the (session, turn) keep-latest dedup must count only the second
	// ($5), never $2.50 + $5.
	mk("projects/theweihu__devbrain/tokens.jsonl",
		`{"ts":"2026-07-04T10:00:00Z","session":"s1","turn":"t1","model":"claude-opus-4-8","in":500000,"out":0,"cache_create":0,"cache_read":0}
{"ts":"2026-07-04T10:00:30Z","session":"s1","turn":"t1","model":"claude-opus-4-8","in":1000000,"out":0,"cache_create":0,"cache_read":0}
{"ts":"2026-07-03T09:00:00Z","model":"claude-fable-5","in":0,"out":0,"cache_create":0,"cache_read":1000000}
{"ts":"2020-01-01T09:00:00Z","model":"claude-opus-4-8","in":9000000,"out":0,"cache_create":0,"cache_read":0}
{"ts":"2027-01-01T09:00:00Z","model":"claude-opus-4-8","in":9000000,"out":0,"cache_create":0,"cache_read":0}
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
		"Jun 5 → Jul 5, 2026", "<b>2</b><span>prompts</span>", "<b>1</b><span>sessions</span>",
		"<b>$6</b><span>total spend</span>",            // $5 opus + $1 fable
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
	// grade badge renders a letter + /100 score and the rubric breakdown
	if !strings.Contains(html, "/100") || !strings.Contains(html, `class="gradebadge"`) {
		t.Error("grade badge missing")
	}
	for _, dim := range []string{"flow (shipped ÷ opened)", "cycle time", "queue hygiene",
		"delegation share", "cache discipline", "brain usage"} {
		if !strings.Contains(html, dim) {
			t.Errorf("grade rubric row %q missing", dim)
		}
	}
	if strings.Contains(html, "model mix") {
		t.Error("removed 'model mix' dimension still renders")
	}
	if !strings.Contains(html, `data-def="median created → done per shipped task`) {
		t.Error("grade rows missing hover definitions")
	}
	if strings.Contains(html, "rubric lives in internal/retro") {
		t.Error("removed Grade-header caption still renders")
	}
	// fixture cycle time: created 07-03T08 → done 07-04T09 ≈ 1.04d → full 8/8
	if !strings.Contains(html, ">cycle time</span>") || !strings.Contains(html, ">8/8<") {
		t.Error("cycle-time full marks missing for the 1-day fixture task")
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

// Window boundaries are inclusive on both ends, and the TODO-fold bullet
// shape `- proj opened: …` parses (the space-before-colon form).
func TestWindowBoundariesAndTodoBullets(t *testing.T) {
	data := t.TempDir()
	for name, body := range map[string]string{
		"2026-06-05.md": "- devbrain: oldest boundary day.\n",
		"2026-07-05.md": "- devbrain: today boundary day.\n- devbrain opened: boundary follow-up task.\n- redlens shipped: boundary ship.\n",
	} {
		p := filepath.Join(data, "journal", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	html, err := Generate(Opts{Data: data, Days: 30, Now: time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"oldest boundary day", "today boundary day",
		"opened: boundary follow-up task", "shipped: boundary ship"} {
		if !strings.Contains(html, w) {
			t.Errorf("missing %q", w)
		}
	}
}

// The cost-per-task curve is log-scaled, not linear: the geometric midpoint of
// $5..$50 (~$15.81/task) earns exactly half marks, and below-$5 stays clamped full.
func TestGradeCostCurve(t *testing.T) {
	base := gradeInput{WindowDays: 30, CycleMedianDays: -1}
	mid := base
	mid.Shipped, mid.Spend = 1, math.Sqrt(250)
	if _, parts := grade(mid); math.Abs(parts[3].Earned-4) > 0.01 {
		t.Errorf("log midpoint: earned %.2f, want 4", parts[3].Earned)
	}
	low := base
	low.Shipped, low.Spend = 10, 20 // $2/task, below the $5 floor
	if _, parts := grade(low); parts[3].Earned != 8 {
		t.Errorf("below-floor: earned %.2f, want 8", parts[3].Earned)
	}
}

func TestLetterOf(t *testing.T) {
	// uOttawa boundaries, one probe per band edge
	for _, c := range []struct {
		score int
		want  string
	}{{100, "A+"}, {90, "A+"}, {89, "A"}, {85, "A"}, {84, "A-"}, {80, "A-"},
		{79, "B+"}, {75, "B+"}, {74, "B"}, {70, "B"}, {69, "C+"}, {66, "C+"},
		{65, "C"}, {60, "C"}, {59, "D+"}, {55, "D+"}, {54, "D"}, {50, "D"},
		{49, "E"}, {40, "E"}, {39, "F"}, {0, "F"}} {
		if got := letterOf(c.score); got != c.want {
			t.Errorf("letterOf(%d) = %s, want %s", c.score, got, c.want)
		}
	}
}

func TestGradeBands(t *testing.T) {
	// one probe per rubric branch: index into parts, expected earned points
	for _, c := range []struct {
		name string
		g    gradeInput
		idx  int
		want float64
	}{
		{"throughput full", gradeInput{Shipped: 90, WindowDays: 30}, 0, 12},
		{"flow default when none opened", gradeInput{WindowDays: 30}, 1, 12},
		{"flow half", gradeInput{Shipped: 2, Opened: 4, WindowDays: 30}, 1, 6},
		{"cycle default when no cycles", gradeInput{WindowDays: 30, CycleMedianDays: -1}, 2, 8},
		{"cycle 8d midband", gradeInput{WindowDays: 30, CycleMedianDays: 8}, 2, 4},
		{"cycle 14d zero", gradeInput{WindowDays: 30, CycleMedianDays: 14}, 2, 0},
		{"cost $5/task full", gradeInput{Shipped: 2, Spend: 10, WindowDays: 30, CycleMedianDays: -1}, 3, 8},
		{"cost $50/task zero", gradeInput{Shipped: 1, Spend: 50, WindowDays: 30, CycleMedianDays: -1}, 3, 0},
		{"spend but nothing shipped", gradeInput{Spend: 10, WindowDays: 30, CycleMedianDays: -1}, 3, 0},
		{"delegation in band", gradeInput{AutoShare: 0.5, WindowDays: 30, CycleMedianDays: -1}, 4, 12},
		{"delegation low", gradeInput{AutoShare: 0.15, WindowDays: 30, CycleMedianDays: -1}, 4, 6},
		{"delegation high", gradeInput{AutoShare: 0.85, WindowDays: 30, CycleMedianDays: -1}, 4, 6},
		{"cache all reads zero", gradeInput{CacheShare: 1, WindowDays: 30, CycleMedianDays: -1}, 5, 0},
		{"cache at 75% full", gradeInput{CacheShare: 0.75, WindowDays: 30, CycleMedianDays: -1}, 5, 8},
		{"hit rate at target", gradeInput{Queries: 10, Hits: 7, WindowDays: 30, CycleMedianDays: -1}, 6, 8},
		{"hit rate no queries", gradeInput{WindowDays: 30, CycleMedianDays: -1}, 6, 0},
		{"brain usage 3/day full", gradeInput{Queries: 6, ActiveDays: 2, WindowDays: 30, CycleMedianDays: -1}, 7, 8},
		{"journal half of active days", gradeInput{JournalDays: 1, ActiveDays: 2, WindowDays: 30, CycleMedianDays: -1}, 8, 3},
		{"journal default when idle", gradeInput{WindowDays: 30, CycleMedianDays: -1}, 8, 6},
		{"active days half", gradeInput{ActiveDays: 15, WindowDays: 30, CycleMedianDays: -1}, 9, 3},
		{"hygiene 2 stale half", gradeInput{StaleTasks: 2, WindowDays: 30, CycleMedianDays: -1}, 10, 4},
		{"hygiene 4 stale zero", gradeInput{StaleTasks: 4, WindowDays: 30, CycleMedianDays: -1}, 10, 0},
		{"smooth 10x zero", gradeInput{PeakDay: 10, AvgDay: 1, WindowDays: 30, CycleMedianDays: -1}, 11, 0},
		{"smooth 3x full", gradeInput{PeakDay: 3, AvgDay: 1, WindowDays: 30, CycleMedianDays: -1}, 11, 4},
		{"smooth default no spend", gradeInput{WindowDays: 30, CycleMedianDays: -1}, 11, 4},
	} {
		score, parts := grade(c.g)
		if got := parts[c.idx].Earned; math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: parts[%d].Earned = %v, want %v", c.name, c.idx, got, c.want)
		}
		sum := 0.0
		for _, p := range parts {
			sum += p.Earned
			if p.Earned < 0 || p.Earned > p.Max {
				t.Errorf("%s: %s earned %v outside [0,%v]", c.name, p.Label, p.Earned, p.Max)
			}
		}
		if score != int(sum+0.5) {
			t.Errorf("%s: score %d != rounded sum %v", c.name, score, sum)
		}
	}
}

func TestGradeColor(t *testing.T) {
	for _, c := range []struct {
		score int
		want  string
	}{{95, "#3fb950"}, {80, "#3fb950"}, {79, "#58a6ff"}, {70, "#58a6ff"},
		{69, "#8b949e"}, {50, "#8b949e"}, {49, "#f85149"}, {0, "#f85149"}} {
		if got := gradeColor(c.score); got != c.want {
			t.Errorf("gradeColor(%d) = %s, want %s", c.score, got, c.want)
		}
	}
}

func TestNiceRangeNextDay(t *testing.T) {
	if got := niceRange("2026-06-05", "2026-07-05"); got != "Jun 5 → Jul 5, 2026" {
		t.Errorf("same-year range = %q", got)
	}
	if got := niceRange("2025-12-20", "2026-01-05"); got != "Dec 20, 2025 → Jan 5, 2026" {
		t.Errorf("cross-year range = %q", got)
	}
	if got := niceRange("garbage", "2026-07-05"); got != "garbage → 2026-07-05" {
		t.Errorf("bad-date fallback = %q", got)
	}
	if got := nextDay("2026-06-30"); got != "2026-07-01" {
		t.Errorf("nextDay month rollover = %q", got)
	}
	if got := nextDay("junk"); got != "9999-99-99" {
		t.Errorf("nextDay bad input = %q (must terminate the day loop)", got)
	}
}

func TestTopRows(t *testing.T) {
	m := map[string]float64{"a": 100, "b": 50, "c": 10, "d": 5}
	rows := topRows(m, 2, func(string) string { return "" }, func(v float64) string { return money(v) })
	if len(rows) != 3 {
		t.Fatalf("want top 2 + others, got %d rows", len(rows))
	}
	if rows[0].Label != "a" || rows[0].Pct != 100 || rows[0].Color != "#58a6ff" {
		t.Errorf("top row = %+v (empty color must default)", rows[0])
	}
	if rows[2].Label != "2 others" || rows[2].Pct != 15 || rows[2].Value != "$15" {
		t.Errorf("others row = %+v", rows[2])
	}
	if rows := topRows(map[string]float64{}, 3, func(string) string { return "x" }, money); rows != nil {
		t.Errorf("empty map should yield no rows, got %+v", rows)
	}
	// keysBy tie-break: equal values order by name
	tied := topRows(map[string]float64{"b": 1, "a": 1}, 2, func(string) string { return "" }, money)
	if tied[0].Label != "a" || tied[1].Label != "b" {
		t.Errorf("tie-break order = %s, %s; want a, b", tied[0].Label, tied[1].Label)
	}
}

func TestScalarHelpers(t *testing.T) {
	if num(json.Number("1.5")) != 1.5 || num(float64(2)) != 2 || num(3) != 3 || num("nope") != 0 || num(nil) != 0 {
		t.Error("num coercion wrong")
	}
	if str("x") != "x" || str(7) != "" {
		t.Error("str coercion wrong")
	}
	if comma(1234567) != "1,234,567" || comma(999) != "999" {
		t.Error("comma grouping wrong")
	}
	if money(1499.6) != "$1,500" {
		t.Errorf("money rounding = %q", money(1499.6))
	}
	if short("owner__proj") != "proj" || short("plain") != "plain" {
		t.Error("short prefix strip wrong")
	}
	if got := renderText("a <b> & `code`"); string(got) != "a &lt;b&gt; &amp; <code>code</code>" {
		t.Errorf("renderText = %q", got)
	}
	if min(2, 4) != 2 || min(5, 4) != 4 {
		t.Error("min wrong")
	}
	for _, c := range []struct {
		xs   []float64
		want float64
	}{{nil, -1}, {[]float64{3}, 3}, {[]float64{5, 1, 3}, 3}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if got := median(append([]float64(nil), c.xs...)); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.xs, got, c.want)
		}
	}
}

func TestGenerateEmptyDataDir(t *testing.T) {
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	html, err := Generate(Opts{Data: t.TempDir(), Days: 0, Now: now}) // Days<=0 → 30 default
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"Jun 5 → Jul 5, 2026", // default 30-day window applied
		"<b>—</b><span>brain hit rate", "<b>$0</b><span>total spend</span>"} {
		if !strings.Contains(html, w) {
			t.Errorf("empty-dir output missing %q", w)
		}
	}
}

func TestGenerateMalformedInputs(t *testing.T) {
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
	// journal: an in-window name that isn't a real date, a day with no bullets,
	// and one good day for a project outside the pinned palette
	mk("journal/2026-06-31.md", "- devbrain: impossible date, must be skipped.\n")
	mk("journal/2026-07-03.md", "prose only, no bullet lines\n")
	mk("journal/2026-07-04.md", "- zeta: unpinned project gets a fallback color.\n- omega: no spend and unpinned, gets the gray default.\n- zeta: second bullet joins the same group.\n")
	// gbrain log: 50 valid lines (20 hits → 40% rate suggestion), then garbage
	// mid-file that must cost one record, not the tail
	var log strings.Builder
	for i := 0; i < 50; i++ {
		h := 0
		if i < 20 {
			h = 1
		}
		fmt.Fprintf(&log, `{"ts":"2026-07-04T10:00:00Z","hits":%d}`+"\n", h)
	}
	log.WriteString("{not json\n")
	fmt.Fprintf(&log, `{"ts":"2026-07-04T10:00:00Z","hits":0}`+"\n") // after the bad line — must still count
	mk("projects/theweihu__zeta/gbrain-queries.log", log.String())
	// todo: unparseable created, and a done_at whose date prefix is in-window
	// but whose timestamp won't parse (shipped counts, cycle time doesn't)
	mk("projects/theweihu__zeta/todo/0001-bad.md",
		"---\nid: 0001-bad\nstatus: done\ncreated: whenever\ndone_at: 2026-07-04Tnotatime\n---\n")
	// stale claims: taken since January, plus a held task with no claimed_at
	// (falls back to created) → 2 stale → queue hygiene 4/8
	mk("projects/theweihu__zeta/todo/0002-stale.md",
		"---\nid: 0002-stale\nstatus: taken\nclaimed_at: 2026-01-01T00:00:00Z\ncreated: 2026-01-01T00:00:00Z\n---\n")
	mk("projects/theweihu__zeta/todo/0003-held.md",
		"---\nid: 0003-held\nstatus: held\ncreated: 2026-01-01T00:00:00Z\n---\n")
	// spend: one huge day amid nothing → spiky suggestion; one auto turn
	mk("projects/theweihu__zeta/tokens.jsonl",
		`{"ts":"2026-07-04T10:00:00Z","model":"claude-opus-4-8","in":10000000,"out":0,"cache_create":0,"cache_read":0,"auto":true}`+"\n")
	// a log day outside the window contributes no sessions or prompts
	mk("projects/theweihu__zeta/log/2020-01-01/main.old.md", "## 10:00:00\n\nancient\n")

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	html, err := Generate(Opts{Data: data, Days: 30, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "impossible date") {
		t.Error("unparseable journal date rendered")
	}
	if strings.Contains(html, "20260703") {
		t.Error("bullet-less journal day rendered a card")
	}
	for _, w := range []string{
		"unpinned project gets a fallback color", "color:#76e3ea", // first fallback color
		"gets the gray default", "color:#8b949e", // unpinned + no spend → gray
		"Brain hit rate is 39.2%",     // 20/51 < 50% with ≥50 queries (bad line skipped, not tail-truncating)
		"<b>Spend is spiky</b>",       // single peak day
		"<b>1</b><span>tasks shipped", // bad done_at timestamp still counts by date
		"51 queries",
	} {
		if !strings.Contains(html, w) {
			t.Errorf("output missing %q", w)
		}
	}
	if !strings.Contains(html, ">queue hygiene</span>") || !strings.Contains(html, ">4/8<") {
		t.Error("two stale claims should score queue hygiene 4/8")
	}
	if !strings.Contains(html, "second bullet joins the same group") {
		t.Error("same-project second bullet missing")
	}
	if !strings.Contains(html, "<b>0</b><span>sessions</span>") {
		t.Error("out-of-window log day should contribute no sessions")
	}
}

func TestRunErrors(t *testing.T) {
	var so, se strings.Builder
	if rc := Run([]string{"--bogus"}, &so, &se); rc != 2 {
		t.Errorf("bad flag rc=%d, want 2", rc)
	}
	if !strings.Contains(se.String(), "usage: devbrain retro") {
		t.Error("bad flag should print usage to stderr")
	}
	// out path whose parent is a file → MkdirAll fails → rc=1
	dir := t.TempDir()
	blocker := filepath.Join(dir, "f")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	so.Reset()
	se.Reset()
	if rc := Run([]string{"--data", dir, "--out", filepath.Join(blocker, "x.html"), "--no-open"}, &so, &se); rc != 1 {
		t.Errorf("unwritable out rc=%d, want 1 (stderr=%s)", rc, se.String())
	}
	// out is an existing directory → MkdirAll ok, WriteFile fails → rc=1
	so.Reset()
	se.Reset()
	if rc := Run([]string{"--data", dir, "--out", dir, "--no-open"}, &so, &se); rc != 1 {
		t.Errorf("out-is-a-dir rc=%d, want 1 (stderr=%s)", rc, se.String())
	}
	// default out path lands under $DATA/retro/<today>.html
	so.Reset()
	se.Reset()
	if rc := Run([]string{"--data", dir, "--no-open"}, &so, &se); rc != 0 {
		t.Fatalf("default-out rc=%d stderr=%s", rc, se.String())
	}
	dest := strings.TrimSpace(so.String())
	if filepath.Dir(dest) != filepath.Join(dir, "retro") || !strings.HasSuffix(dest, ".html") {
		t.Errorf("default out path = %q", dest)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("default out file not written: %v", err)
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
