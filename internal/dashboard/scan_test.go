package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// seedScanLogs builds the classification fixture from scripts/test-queue.sh:
// an interactive session (with the adversarial prose quote of a skill meta
// line) and an autonomous nightshift worker session.
func seedScanLogs(t *testing.T, q *Queue, day string) {
	t.Helper()
	logdir := filepath.Join(q.Data, "projects", "proj__a", "log", day)
	if err := os.MkdirAll(logdir, 0o755); err != nil {
		t.Fatal(err)
	}
	interactive := "# header\n> worktree: edmonton · cwd: /Users/x/conductor/edmonton · times in UTC\n\n" +
		// A Conductor session's FIRST message: the harness prepends a
		// <system_instruction> block ahead of the real /distill the user typed.
		// The wrapper must be peeled so the turn counts as a /distill command.
		"## 09:10:00\n\n<system_instruction>\nYou are working inside Conductor.\n</system_instruction>\n\n/distill and then release\n\n" +
		// Plain Claude Code slash-command expansion: rewritten to the bare /ship command.
		"## 09:12:00\n\n<command-message>ship</command-message>\n<command-name>/ship</command-name>\n\n" +
		"## 09:15:00\n\nhow do we fix the parser?\n\n" +
		"↳ 09:16 — a model response summary that must be ignored\n" +
		"   touched: x.py  ·  tools: Skill:distill×1, Bash×3\n" + // named skill in the meta line
		"   ⤷ response sample:\n" +
		"   > I wrote tools: Skill×9 and Skill:ship×4 into the meta line.\n\n" + // PROSE quote — must NOT count
		"## 09:20:00\n\n/continue\n\n" +
		"↳ 09:21 — another summary\n" +
		"   tools: Skill×1\n\n" + // older log: call recorded, name unknown (?)
		"## 09:25:00\n\nPLANNING TURN: do not write code\n\n" +
		"## 09:30:00\n\ncommit and push it\n"
	if err := os.WriteFile(filepath.Join(logdir, "edmonton.sess.md"), []byte(interactive), 0o644); err != nil {
		t.Fatal(err)
	}
	auton := "# header\n> worktree: proj-a-w2 · cwd: /Users/x/nightshift/proj-a-w2 · times in UTC\n\n" +
		"## 10:00:00\n\nadd a minimal test\n"
	if err := os.WriteFile(filepath.Join(logdir, "proj-a-w2.ns.md"), []byte(auton), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanPromptsClassification(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	if err := os.MkdirAll(filepath.Join(q.Data, "projects", "proj__a", "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	day := fixedClock().Format("2006-01-02")
	seedScanLogs(t, q, day)
	scan := q.ScanPrompts(30, "")
	kinds := map[string]string{}
	recaps := map[string]string{}
	skills := map[string][]string{}
	for _, r := range scan {
		kinds[r.X] = r.Kind
		recaps[r.X] = r.Recap
		skills[r.X] = r.Skills
	}
	if kinds["how do we fix the parser?"] != "human" {
		t.Errorf("interactive prose -> %q, want human", kinds["how do we fix the parser?"])
	}
	if kinds["/continue"] != "command" {
		t.Errorf("interactive slash -> %q, want command", kinds["/continue"])
	}
	// The Conductor wrapper is peeled: the turn is the /distill underneath, a
	// typed command — not a "system" turn dropped from the Skills count.
	if kinds["/distill and then release"] != "command" {
		t.Errorf("wrapped first-prompt slash -> %q, want command", kinds["/distill and then release"])
	}
	if _, ok := kinds["<system_instruction>\nYou are working inside Conductor.\n</system_instruction>\n\n/distill and then release"]; ok {
		t.Error("scan must strip the <system_instruction> wrapper from the prompt text")
	}
	// Claude Code's <command-name> expansion is rewritten to the bare /ship command.
	if kinds["/ship"] != "command" {
		t.Errorf("command-name expansion -> %q, want command (/ship)", kinds["/ship"])
	}
	if kinds["PLANNING TURN: do not write code"] != "nightshift" {
		t.Errorf("planning text -> %q, want nightshift", kinds["PLANNING TURN: do not write code"])
	}
	if kinds["add a minimal test"] != "nightshift" {
		t.Errorf("autonomous session prose -> %q, want nightshift", kinds["add a minimal test"])
	}
	for _, r := range scan {
		if strings.Contains(r.X, "model response") {
			t.Error("scan must strip the response line from the prompt text")
		}
	}
	// recap contract: the ↳-line summary is lifted into r
	if recaps["how do we fix the parser?"] != "a model response summary that must be ignored" {
		t.Errorf("recap = %q", recaps["how do we fix the parser?"])
	}
	if recaps["commit and push it"] != "" {
		t.Errorf("no ↳ line must yield empty recap, got %q", recaps["commit and push it"])
	}
	// skill-meta parsing incl. the prose-quote adversarial case
	if !reflect.DeepEqual(skills["how do we fix the parser?"], []string{"distill"}) {
		t.Errorf("meta-named skill = %v (prose quote must NOT count)", skills["how do we fix the parser?"])
	}
	if !reflect.DeepEqual(skills["/continue"], []string{"?"}) {
		t.Errorf("unnamed Skill meta = %v, want ?", skills["/continue"])
	}
	if !reflect.DeepEqual(skills["commit and push it"], []string{}) {
		t.Errorf("no skill meta = %v, want empty list", skills["commit and push it"])
	}
	// typed/bot toggles
	typed := xs(FilterKind(scan, "typed"))
	sort.Strings(typed)
	if !reflect.DeepEqual(typed, []string{"/continue", "/distill and then release", "/ship", "commit and push it", "how do we fix the parser?"}) {
		t.Errorf("typed = %v", typed)
	}
	bot := xs(FilterKind(scan, "bot"))
	sort.Strings(bot)
	if !reflect.DeepEqual(bot, []string{"PLANNING TURN: do not write code", "add a minimal test"}) {
		t.Errorf("bot = %v", bot)
	}
	if len(FilterKind(scan, "all")) != 7 {
		t.Errorf("all = %d, want 7", len(FilterKind(scan, "all")))
	}
}

func xs(recs []*Prompt) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.X
	}
	return out
}

// writeSession writes one session log for `proj` with the given (HH:MM, text) prompts.
func writeSession(t *testing.T, q *Queue, proj, day, sess string, prompts [][2]string) {
	t.Helper()
	dir := filepath.Join(q.Data, "projects", proj, "log", day)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("# header\n> worktree: x · cwd: /Users/x/conductor/x · times in UTC\n\n")
	for _, p := range prompts {
		b.WriteString("## " + p[0] + ":00\n\n" + p[1] + "\n\n")
	}
	if err := os.WriteFile(filepath.Join(dir, sess+".md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReclassifyRepeats(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	// A long rubric (>repeatLongWords) whose only varying part is the trailing item — tests
	// both near-dup prefix collapse AND that length makes 2 copies enough to flip.
	rubric := "You are a seasoned reviewer scoring applications. " + strings.Repeat("weigh the evidence carefully. ", 60)
	var pr [][2]string
	pr = append(pr, [2]string{"09:00", rubric + "item one"}) // long payload, only 2 copies ->
	pr = append(pr, [2]string{"09:01", rubric + "item two"}) // "repeat" (length lowers the bar)
	for i := 0; i < 3; i++ {                                 // short prompt 3x -> "repeat"
		pr = append(pr, [2]string{fmt.Sprintf("10:%02d", i), "rerun the same short line"})
	}
	for i := 0; i < 2; i++ { // short prompt 2x -> stays "human" (twice is fine)
		pr = append(pr, [2]string{fmt.Sprintf("11:%02d", i), "a short line said twice"})
	}
	pr = append(pr, [2]string{"12:00", "a unique one-off prompt"}) // singleton -> "human"
	for i := 0; i < 4; i++ {                                       // a skill command fired 4x -> stays "command" (deliberate re-invocation, exempt)
		pr = append(pr, [2]string{fmt.Sprintf("14:%02d", i), "/distill"})
	}
	for i := 0; i < 3; i++ { // a path-like slash prompt 3x -> "repeat" (not a skill command, still collapses)
		pr = append(pr, [2]string{fmt.Sprintf("15:%02d", i), "/Users/x/notes.md please read this"})
	}
	for i := 0; i < 3; i++ { // Codex-style $command 3x -> "command", exempt like /distill
		pr = append(pr, [2]string{fmt.Sprintf("16:%02d", i), "$distill"})
	}
	writeSession(t, q, "proj__a", day, "s1", pr)
	// Same short line 2x in each of two projects must NOT merge into a >2 group.
	shared := [][2]string{{"13:00", "line shared across two projects"}, {"13:01", "line shared across two projects"}}
	writeSession(t, q, "proj__a", day, "s2", shared)
	writeSession(t, q, "proj__b", day, "s3", shared)

	byText := map[string]string{}
	for _, r := range q.ScanPrompts(30, "") {
		byText[r.X] = r.Kind
	}
	if k := byText[rubric+"item one"]; k != "repeat" {
		t.Errorf("long payload sent 2x -> %q, want repeat (length lowers the bar)", k)
	}
	if k := byText["rerun the same short line"]; k != "repeat" {
		t.Errorf("short prompt 3x -> %q, want repeat", k)
	}
	if k := byText["a short line said twice"]; k != "human" {
		t.Errorf("short prompt 2x -> %q, want human (twice is fine)", k)
	}
	if k := byText["a unique one-off prompt"]; k != "human" {
		t.Errorf("singleton -> %q, want human", k)
	}
	if k := byText["/distill"]; k != "command" {
		t.Errorf("skill command fired 4x -> %q, want command (re-invocation is not a repeat)", k)
	}
	if k := byText["/Users/x/notes.md please read this"]; k != "repeat" {
		t.Errorf("path-like slash prompt 3x -> %q, want repeat (not a skill command)", k)
	}
	if k := byText["$distill"]; k != "command" {
		t.Errorf("Codex $command 3x -> %q, want command (skill re-invocation, exempt)", k)
	}
	if k := byText["line shared across two projects"]; k != "human" {
		t.Errorf("2+2 across projects must not merge -> %q, want human", k)
	}
}

func TestReclassifyPayloads(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	long := func(head string) string {
		return strings.TrimSpace(head + " " + strings.Repeat("weigh the evidence carefully. ", 60))
	}
	// Signal 1: a single-instance review payload that OPENS in agent voice.
	review := long("You are reviewing a pull request. Focus only on bugs.")
	// Signal 1 must NOT fire on a long first-person brain dump (no agent-voice opener).
	braindump := long("here is a 10-minute brain dump of how I want this project to go.")
	// Below the length floor even in agent voice -> stays human.
	shortReview := "Review this diff and tell me what's off."
	// Signal 2: identical long NON-voice opener once in each of two projects -> both payload.
	shared := long("finalize the ingest and reconcile the last few brand subs.")
	// Same shape but only in ONE project once -> singleton, stays human (signal 2 needs ≥2 projects).
	lonely := long("stand up the staging box and point the CLI at it.")

	writeSession(t, q, "proj__a", day, "s1", [][2]string{
		{"09:00", review}, {"09:01", braindump}, {"09:02", shortReview},
		{"09:03", shared}, {"09:04", lonely},
	})
	writeSession(t, q, "proj__b", day, "s2", [][2]string{{"10:00", shared}})

	byText := map[string]string{}
	for _, r := range q.ScanPrompts(30, "") {
		byText[r.X] = r.Kind
	}
	if k := byText[review]; k != "payload" {
		t.Errorf("agent-voice review payload -> %q, want payload", k)
	}
	if k := byText[braindump]; k != "human" {
		t.Errorf("first-person brain dump -> %q, want human", k)
	}
	if k := byText[shortReview]; k != "human" {
		t.Errorf("short agent-voice line -> %q, want human (below length floor)", k)
	}
	if k := byText[shared]; k != "payload" {
		t.Errorf("identical long opener across 2 projects -> %q, want payload", k)
	}
	if k := byText[lonely]; k != "human" {
		t.Errorf("long non-voice singleton -> %q, want human", k)
	}

	// A project filter must not change any record's kind: the same opener is "payload" whether
	// scanned globally or scoped to its project (classification runs over the full corpus).
	for _, r := range q.ScanPrompts(30, "proj__a") {
		if r.X == shared && r.Kind != "payload" {
			t.Errorf("project-scoped scan flipped cross-project payload -> %q, want payload", r.Kind)
		}
	}
}

// Opener pasted 2x in project A (-> repeat) + once in B still spans 2 projects, so B flips.
func TestReclassifyPayloadsRepeatEvidence(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	op := "please crunch the batch now. " + strings.Repeat("iterate over every row. ", 60)
	writeSession(t, q, "proj__a", day, "s1", [][2]string{{"09:00", op}, {"09:01", op}}) // 2x -> repeat
	writeSession(t, q, "proj__b", day, "s2", [][2]string{{"10:00", op}})                // singleton
	got := map[string][]string{}
	for _, r := range q.ScanPrompts(30, "") {
		got[r.P] = append(got[r.P], r.Kind)
	}
	if got["proj__a"][0] != "repeat" {
		t.Errorf("2x in one project -> %q, want repeat", got["proj__a"][0])
	}
	if got["proj__b"][0] != "payload" {
		t.Errorf("singleton whose opener is repeat'd elsewhere -> %q, want payload", got["proj__b"][0])
	}
}

// A repeat group split across the window boundary must classify by the FULL history, not
// the window — otherwise the same prompt is "human" in a 30d scan and "repeat" in a 0d one.
func TestReclassifyRepeatsWindowStable(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	recent := fixedClock().Format("2006-01-02")
	old := fixedClock().AddDate(0, 0, -60).Format("2006-01-02")
	mk := func(day, sess string, n int) {
		var pr [][2]string
		for i := 0; i < n; i++ {
			pr = append(pr, [2]string{fmt.Sprintf("09:%02d", i), "the same rubric pasted many times"})
		}
		writeSession(t, q, "proj__a", day, sess, pr)
	}
	mk(old, "sOld", 3)    // 3 copies outside the 30d window
	mk(recent, "sNew", 3) // 3 inside — 6 across history, so the group is > repeatMax
	got := q.ScanPrompts(30, "")
	if len(got) != 3 {
		t.Fatalf("30d scan returned %d recs, want 3 in-window", len(got))
	}
	for _, r := range got {
		if r.Kind != "repeat" {
			t.Errorf("in-window copy of a 6x-repeated prompt -> %q, want repeat (grouped over full history)", r.Kind)
		}
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s          string
		autonomous bool
		want       string
	}{
		{"/continue", false, "command"},
		{"/continue", true, "nightshift"},
		{"merged", true, "nightshift"},
		{"<task-notification> x", true, "system"},
		{"<system_instruction>hi", false, "system"},
		{"<command-name>/foo</command-name>", false, "system"},
		{"You are generating a short conversation title for this chat", false, "title-gen"},
		{"Caveat: The messages below were generated by the user", false, "system"},
		{"Check in on the nightshift fleet", false, "nightshift"},
		{"Check on the nightshift run", false, "nightshift"},
		{"PLANNING TURN: propose tasks", false, "nightshift"},
		{"normal prose", false, "human"},
		{"   ", false, ""},
		{"", true, ""},
	}
	rb := defaultClassifier()
	for _, c := range cases {
		if got := rb.Classify(c.s, c.autonomous); got != c.want {
			t.Errorf("Classify(%q, %v) = %q, want %q", c.s, c.autonomous, got, c.want)
		}
	}
}

func TestSessionIsAutonomous(t *testing.T) {
	t.Parallel()
	rb := defaultClassifier()
	if !rb.SessionIsAutonomous("/Users/x/drain/foo-w1", "foo-w1") {
		t.Error("drain worker must be autonomous")
	}
	if !rb.SessionIsAutonomous("/Users/x/nightshift/foo-w2", "foo") {
		t.Error("nightshift cwd must be autonomous")
	}
	if !rb.SessionIsAutonomous("/Users/x/src/foo", "foo-w3") {
		t.Error("-wN worktree name must be autonomous")
	}
	if rb.SessionIsAutonomous("/Users/x/conductor/edmonton", "edmonton") {
		t.Error("normal cwd must not be autonomous")
	}
}

func TestScanWindow(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	seedScanLogs(t, q, day)
	oldd := filepath.Join(q.Data, "projects", "proj__a", "log", "2020-01-01")
	if err := os.MkdirAll(oldd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldd, "x.s.md"), []byte("## 01:00:00\n\nancient prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in30 := xs(q.ScanPrompts(30, ""))
	for _, x := range in30 {
		if x == "ancient prompt" {
			t.Error("30-day window must exclude 2020 prompts")
		}
	}
	all := xs(q.ScanPrompts(0, ""))
	found := false
	for _, x := range all {
		if x == "ancient prompt" {
			found = true
		}
	}
	if !found {
		t.Error("days=0 means all history")
	}
	// project filter
	if got := q.ScanPrompts(0, "proj__zzz"); len(got) != 0 {
		t.Errorf("project filter leaked %d records", len(got))
	}
}

// The scan is cached incrementally: an unchanged corpus reuses the snapshot;
// a new/changed file re-parses (only) that file and surfaces on the next call.
func TestScanCacheInvalidation(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	day := fixedClock().Format("2006-01-02")
	writeSession(t, q, "proj__a", day, "s1", [][2]string{{"09:00", "first prompt"}})

	if got := len(q.ScanPrompts(0, "")); got != 1 {
		t.Fatalf("want 1 prompt, got %d", got)
	}
	sig := q.promptCache.derivSig
	if sig == "" {
		t.Fatal("expected a cached signature after first scan")
	}
	snap := q.promptCache.derived
	f1 := q.promptCache.files[filepath.Join(q.Data, "projects", "proj__a", "log", day, "s1.md")]
	if f1 == nil {
		t.Fatal("expected a per-file parse entry for s1")
	}

	// Same corpus → cache hit: signature unchanged and the SAME snapshot reused.
	_ = q.ScanPrompts(0, "")
	if q.promptCache.derivSig != sig {
		t.Error("unchanged corpus should not re-sign")
	}
	if !sameSnapshot(q.promptCache.derived, snap) {
		t.Error("unchanged corpus should reuse the derived snapshot, not rebuild")
	}

	// A new session file bumps the signature, re-parses only it, and shows up.
	// The untouched file's parse template must be reused verbatim.
	writeSession(t, q, "proj__a", day, "s2", [][2]string{{"10:00", "second prompt"}})
	recs := q.ScanPrompts(0, "")
	if len(recs) != 2 {
		t.Fatalf("new sweep not picked up: want 2, got %d", len(recs))
	}
	if q.promptCache.derivSig == sig {
		t.Error("corpus grew but signature did not change")
	}
	f1b := q.promptCache.files[filepath.Join(q.Data, "projects", "proj__a", "log", day, "s1.md")]
	if f1b != f1 {
		t.Error("unchanged file s1 should reuse its cached parse template, not re-parse")
	}
}

func sameSnapshot(a, b []*Prompt) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The committed dashboard fixture: 11 prompts, 7 typed / 4 bot — the same
// numbers pinned by testdata/golden/api/prompts-*.json.
func TestScanDashboardFixture(t *testing.T) {
	t.Parallel()
	q := New(filepath.Join("..", "..", "testdata", "dashboard-fixture"))
	q.Now = fixedClock
	recs := q.ScanPrompts(0, "")
	if len(recs) != 11 {
		t.Fatalf("fixture scan = %d prompts, want 11", len(recs))
	}
	typed := 0
	for _, r := range recs {
		if typedKinds[r.Kind] {
			typed++
		}
	}
	if typed != 7 || len(recs)-typed != 4 {
		t.Errorf("fixture counts = %d typed / %d bot, want 7/4", typed, len(recs)-typed)
	}
	// the prose "tools: Skill×9" quote in the response sample is not counted
	for _, r := range recs {
		if r.X == "Fix the flaky test in the importer — it fails every third run on CI." {
			if len(r.Skills) != 0 {
				t.Errorf("prose skill quote counted: %v", r.Skills)
			}
			if r.Recap != "Pinned the importer clock and the flake is gone; suite green twice in a row." {
				t.Errorf("recap = %q", r.Recap)
			}
		}
		if r.X == "/distill" && !reflect.DeepEqual(r.Skills, []string{"distill"}) {
			t.Errorf("/distill skills = %v", r.Skills)
		}
	}
}

func TestGBrainQueries(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	today := fixedClock().Format("2006-01-02")
	gblog := filepath.Join(q.Data, "projects", "proj__a", "gbrain-queries.log")
	if err := os.MkdirAll(filepath.Dir(gblog), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"ts": "` + today + `T10:00:00Z", "project": "proj__a", "cmd": "gbrain search \"edge cases retry\"", "modes": ["search"], "hits": 3, "slugs": ["proj__a/impl", "proj__a/impl"]}`,
		`{"ts": "` + today + `T10:05:00Z", "project": "proj__a", "cmd": "gbrain put \"$x\"", "modes": ["put"], "hits": 0, "slugs": []}`,
		`{"ts": "2020-01-01T00:00:00Z", "project": "proj__a", "cmd": "gbrain query \"ancient\"", "modes": ["query"], "hits": 0, "slugs": []}`,
		// an explicit `ok` wins over the hits fallback: a get that returned a page
		// whose target we couldn't parse is useful context at hits 0
		`{"ts": "` + today + `T10:06:00Z", "project": "proj__a", "cmd": "gbrain get \"a;b<c\"", "modes": ["get"], "hits": 0, "slugs": [], "auto": false, "ok": true}`,
	}
	if err := os.WriteFile(gblog, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gq := q.GBrainQueries(0, "")
	if len(gq) != 4 {
		t.Fatalf("parsed %d entries, want 4", len(gq))
	}
	reads := 0
	for _, r := range gq {
		if r.Read {
			reads++
		}
	}
	if reads != 3 {
		t.Errorf("read = search/query/get, not put: %d reads", reads)
	}
	found := false
	for _, r := range gq {
		if r.Q == "edge cases retry" && r.Hits.(interface{ String() string }).String() == "3" {
			found = true
		}
	}
	if !found {
		t.Error("topic + hits extraction failed")
	}
	for _, r := range q.GBrainQueries(30, "") {
		if strings.Contains(r.TS, "2020") {
			t.Error("30-day window must exclude 2020")
		}
	}
	// Pre-`ok` records fall back to hits>0 — including hits:0, which must stay
	// false (the normalized 0 is an untyped int that pyTruthy would call true).
	// An explicit `ok` wins over that fallback even at hits 0.
	wantOk := map[string]bool{"10:00:00": true, "10:05:00": false, "10:06:00": true, "00:00:00": false}
	for _, r := range gq {
		hhmmss := r.TS[11:19]
		if want, ok := wantOk[hhmmss]; ok && r.Ok != want {
			t.Errorf("ok for record at %s: got %v want %v", hhmmss, r.Ok, want)
		}
		// Only a pre-`ok` get that scored 0 is UNKNOWN — everything else is a real
		// signal. None of the records above is that shape, so all are known.
		if !r.OkKnown {
			t.Errorf("record at %s should have a known ok signal", hhmmss)
		}
	}
	// A pre-`ok` get that scored 0 is unknown, not a miss: rates must exclude it.
	legacy := `{"ts": "` + today + `T12:00:00Z", "project": "proj__a", "cmd": "gbrain get \"$OLD\"", "modes": ["get"], "hits": 0, "slugs": []}` + "\n"
	if err := os.WriteFile(gblog, []byte(strings.Join(lines, "\n")+"\n"+legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, r := range q.GBrainQueries(0, "") {
		if strings.HasPrefix(r.TS[11:], "12:00") && r.OkKnown {
			t.Error("pre-`ok` get with hits 0 must be unknown, not a counted miss")
		}
	}
	// a not-found get exposes its attempted page via `target`
	extra := `{"ts": "` + today + `T11:00:00Z", "project": "proj__a", "cmd": "gbrain get \"proj__a/missing\" --fuzzy", "modes": ["get"], "hits": 0, "slugs": []}` + "\n"
	f, err := os.OpenFile(gblog, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(extra); err != nil {
		t.Fatal(err)
	}
	f.Close()
	gq2 := q.GBrainQueries(0, "")
	found = false
	for _, r := range gq2 {
		if len(r.Modes) == 1 && r.Modes[0] == "get" && r.Target == "proj__a/missing" {
			found = true
		} else if r.Target != "" {
			t.Errorf("non-get record has target %q", r.Target)
		}
	}
	if !found {
		t.Error("get-miss record must carry its target page")
	}
}

func TestGBGetTargetQueueFilter(t *testing.T) {
	t.Parallel()
	// The full 16-case adversarial table lives in internal/gbrainlog; these
	// pin the queue wrapper's slash-requiring fullmatch.
	cases := []struct{ cmd, want string }{
		{`gbrain get "proj__a/page" --fuzzy`, "proj__a/page"},
		{`gbrain get pagename`, ""},          // bare name (no slash) rejected
		{`credit a gbrain get as a hit`, ""}, // prose 'as' has no slug shape
		{`gbrain get --help 2>&1`, ""},       // option-only get is not a page
		{`gbrain get "$PAGE"`, ""},           // $VAR fails the slug fullmatch
		{"echo `gbrain get proj__a/x`", "proj__a/x"},
	}
	for _, c := range cases {
		if got := GBGetTarget(c.cmd); got != c.want {
			t.Errorf("GBGetTarget(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestTokenUsage(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	today := fixedClock().Format("2006-01-02")
	toklog := filepath.Join(q.Data, "projects", "proj__a", "tokens.jsonl")
	if err := os.MkdirAll(filepath.Dir(toklog), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"ts": "` + today + `T10:00:00Z", "session": "s1", "model": "claude-opus-4-8", "in": 100, "out": 200, "cache_create": 0, "cache_read": 5000, "auto": true}`,
		`{"ts": "` + today + `T10:00:00Z", "session": "s1", "model": "claude-opus-4-8", "in": 100, "out": 200, "cache_create": 0, "cache_read": 5000, "auto": true}`, // exact dup -> dropped
		`{"ts": "` + today + `T11:00:00Z", "session": "s2", "model": "claude-sonnet-4-6", "in": 10, "out": 20, "cache_create": 0, "cache_read": 0}`,                  // no auto -> interactive
		`{"ts": "2020-01-01T00:00:00Z", "session": "s0", "model": "claude-haiku-4-5", "in": 1, "out": 1, "cache_create": 0, "cache_read": 0}`,
	}
	if err := os.WriteFile(toklog, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tu := q.TokenUsage(0, "")
	if len(tu) != 3 {
		t.Fatalf("dedup on (session,ts): got %d rows, want 3", len(tu))
	}
	var opus, sonnet *TokenRec
	for _, r := range tu {
		switch r.Model {
		case "claude-opus-4-8":
			opus = r
		case "claude-sonnet-4-6":
			sonnet = r
		}
	}
	if opus == nil || numStr(opus.Out) != "200" || numStr(opus.CR) != "5000" || !opus.Auto {
		t.Errorf("opus row wrong: %+v", opus)
	}
	if sonnet == nil || sonnet.Auto {
		t.Errorf("missing auto must read as interactive: %+v", sonnet)
	}
	for _, r := range q.TokenUsage(30, "") {
		if strings.Contains(r.TS, "2020") {
			t.Error("30-day window must exclude 2020")
		}
	}
}

// The Stop hook can capture the same turn more than once as it grows: each
// record is the turn's CUMULATIVE usage under a new last-assistant ts, so
// (session, ts) can't collapse them. Records stamped with a stable "turn"
// key must dedup on (session, turn), keeping the latest (complete) capture.
func TestTokenUsageTurnDedup(t *testing.T) {
	t.Parallel()
	q := newTestQueue(t)
	today := fixedClock().Format("2006-01-02")
	toklog := filepath.Join(q.Data, "projects", "proj__a", "tokens.jsonl")
	if err := os.MkdirAll(filepath.Dir(toklog), 0o755); err != nil {
		t.Fatal(err)
	}
	turn := today + "T01:30:00Z"
	lines := []string{
		// partial capture, then the grown re-capture of the SAME turn
		`{"ts": "` + today + `T01:34:18Z", "session": "s1", "model": "claude-fable-5", "in": 10, "out": 100, "cache_create": 0, "cache_read": 16447173, "auto": false, "turn": "` + turn + `"}`,
		`{"ts": "` + today + `T01:34:59Z", "session": "s1", "model": "claude-fable-5", "in": 13, "out": 154, "cache_create": 0, "cache_read": 17245684, "auto": false, "turn": "` + turn + `"}`,
		// same turn key in a DIFFERENT session must stay separate
		`{"ts": "` + today + `T01:35:00Z", "session": "s2", "model": "claude-fable-5", "in": 1, "out": 2, "cache_create": 0, "cache_read": 30, "auto": false, "turn": "` + turn + `"}`,
		// legacy row without turn: (session, ts) first-wins as before
		`{"ts": "` + today + `T02:00:00Z", "session": "s1", "model": "claude-opus-4-8", "in": 5, "out": 6, "cache_create": 0, "cache_read": 70, "auto": false}`,
	}
	if err := os.WriteFile(toklog, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tu := q.TokenUsage(0, "")
	if len(tu) != 3 {
		t.Fatalf("turn dedup: got %d rows, want 3", len(tu))
	}
	var recapped *TokenRec
	for _, r := range tu {
		if r.Session == "s1" && r.Model == "claude-fable-5" {
			recapped = r
		}
	}
	if recapped == nil || numStr(recapped.CR) != "17245684" || !strings.Contains(recapped.TS, "01:34:59") {
		t.Fatalf("re-captured turn must keep the LATEST capture, got %+v", recapped)
	}
}

func numStr(v any) string {
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return ""
}

func TestCutoffUsesInjectedClock(t *testing.T) {
	t.Parallel()
	q := New(t.TempDir())
	q.Now = func() time.Time { return time.Date(2020, 1, 31, 0, 0, 0, 0, time.UTC) }
	if got := q.cutoffDate(30); got != "2020-01-01" {
		t.Errorf("cutoff = %q, want 2020-01-01", got)
	}
	if got := q.cutoffDate(0); got != "0000-00-00" {
		t.Errorf("days=0 cutoff = %q", got)
	}
}
