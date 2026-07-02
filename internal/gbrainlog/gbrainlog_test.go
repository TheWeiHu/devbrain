package gbrainlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

func readJSONL(t *testing.T, rel string) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", rel))
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
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Record output must equal the golden byte-for-byte (golden produced by the
// legacy devbrain_lib.py gbrain_record).
func TestRecordGolden(t *testing.T) {
	t.Parallel()
	cases := readJSONL(t, "testdata/corpus/gbrain-record-cases.jsonl")
	golds := readJSONL(t, "testdata/golden/gbrain-record.jsonl")
	if len(cases) != len(golds) {
		t.Fatalf("corpus/golden mismatch: %d vs %d", len(cases), len(golds))
	}
	for i, c := range cases {
		c, g := c, golds[i]
		name := c["name"].(string)
		if g["name"].(string) != name {
			t.Fatalf("case %d: corpus %q vs golden %q", i, name, g["name"])
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := Record(c["cmd"].(string), c["out"].(string), c["project"].(string), c["ts"].(string))
			if want := g["out"].(string); got != want {
				t.Errorf("got  %s\nwant %s", got, want)
			}
		})
	}
}

// queueSlug is the dashboard-side filter (scripts/queue.py gb_get_target):
// a real page is always <project>/<page>, so slash-less targets are dropped.
// It lives in the queue port, not this package; replicated here only to pin
// both columns of the shared adversarial table.
var queueSlug = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._/-]+\z`)

func queueFilter(target string) string {
	if target != "" && queueSlug.MatchString(target) {
		return target
	}
	return ""
}

// The 16 adversarial cases from scripts/test-queue.sh (gb_get_target section).
// `lib` is this package's GetTarget(cmd, false); `queue` is what the queue
// wrapper reports after its slug-shape filter.
func TestGetTargetTable(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, cmd, lib, queue string }{
		{"plain slug", `gbrain get "proj__a/page" --fuzzy`, "proj__a/page", "proj__a/page"},
		{"cmd substitution", `body=$(gbrain get proj__a/page)`, "proj__a/page", "proj__a/page"},
		{"chained after echo", `echo hi; gbrain get proj__a/smoke-testing 2>&1`, "proj__a/smoke-testing", "proj__a/smoke-testing"},
		{"quoted query is NOT a get", `gbrain search "why is gbrain get a miss"`, "", ""},
		{"prose 'gbrain get as' has no slug", `credit a gbrain get as a hit`, "as", ""},
		{"bare name (no slash) rejected by queue", `gbrain get pagename`, "pagename", ""},
		{"--help with redirection is not a page", `gbrain get --help 2>&1`, "", ""},
		{"redirection fd not mistaken for slug", `gbrain get proj__a/page 2>&1 | head`, "proj__a/page", "proj__a/page"},
		{"unparseable cmd -> no fabricated page", `gbrain search "why gbrain get proj__a/missing" ; echo don't`, "", ""},
		{"option-only get before a real get", `gbrain get --help; gbrain get proj__a/page`, "proj__a/page", "proj__a/page"},
		{"quoted command substitution", `echo "$(gbrain get proj__a/page)"`, "proj__a/page", "proj__a/page"},
		{"assigned quoted cmd-subst", `body="$(gbrain get proj__a/page)"; echo "$body"`, "proj__a/page", "proj__a/page"},
		{"backtick substitution", "echo `gbrain get proj__a/page`", "proj__a/page", "proj__a/page"},
		{"query that IS the verb words", `gbrain search "gbrain get proj__a/page"`, "", ""},
		{"chained get inside quoted substitution", `echo "$(cd repo && gbrain get proj__a/page)"`, "proj__a/page", "proj__a/page"},
		{"path-prefixed get inside quoted substitution", `echo "$(/home/u/.bun/bin/gbrain get proj__a/page)"`, "proj__a/page", "proj__a/page"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := GetTarget(c.cmd, false)
			if got != c.lib {
				t.Errorf("GetTarget(%q, false) = %q, want %q", c.cmd, got, c.lib)
			}
			if q := queueFilter(got); q != c.queue {
				t.Errorf("queue filter of %q = %q, want %q", got, q, c.queue)
			}
		})
	}
}

// fallback=true retries an unparseable line with a crude split (the Record
// path); it deliberately trades precision for recall.
func TestGetTargetFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cmd      string
		fallback bool
		want     string
	}{
		// unterminated quote: strict parse fails, fallback recovers a target
		{`gbrain get "unclosed quote fix__demo/frag`, false, ""},
		{`gbrain get "unclosed quote fix__demo/frag`, true, "unclosed"},
		// fallback strips "'(); off each raw token
		{`gbrain search "why gbrain get proj__a/missing" ; echo don't`, true, "proj__a/missing"},
		// parseable lines never take the fallback branch
		{`gbrain get proj__a/page`, true, "proj__a/page"},
		// guards
		{"", true, ""},
		{"gbrain search only", true, ""},
		// multi-line: first line has no get, second does
		{"ls -la\ngbrain get proj__a/two", false, "proj__a/two"},
		// $VAR target returned as-is
		{`gbrain get "$PAGE"`, false, "$PAGE"},
		// shell meta in the candidate arg aborts
		{`gbrain get "a;b<c"`, false, ""},
		// flags and digits skipped
		{`gbrain get --fuzzy fix__demo/flagged`, false, "fix__demo/flagged"},
		{`gbrain get 42 fix__demo/numbered`, false, "fix__demo/numbered"},
	}
	for _, c := range cases {
		if got := GetTarget(c.cmd, c.fallback); got != c.want {
			t.Errorf("GetTarget(%q, %v) = %q, want %q", c.cmd, c.fallback, got, c.want)
		}
	}
}

// gbTok expectations verified against CPython while porting:
// shlex.shlex(s, posix=True, punctuation_chars="();<>|&`"),
// whitespace_split=True, commenters="".
func TestGbTok(t *testing.T) {
	t.Parallel()
	ok := []struct {
		in   string
		want []string
	}{
		{``, nil},
		{`a"b c"d`, []string{"ab cd"}},
		{`a'b c'd`, []string{"ab cd"}},
		{`gbrain get "proj/page" --fuzzy`, []string{"gbrain", "get", "proj/page", "--fuzzy"}},
		{`body=$(gbrain get proj/page)`, []string{"body=$", "(", "gbrain", "get", "proj/page", ")"}},
		{`echo hi; gbrain get p 2>&1`, []string{"echo", "hi", ";", "gbrain", "get", "p", "2", ">&", "1"}},
		{`a && b || c;(d)`, []string{"a", "&&", "b", "||", "c", ";(", "d", ")"}},
		{`""`, []string{""}},
		{`''`, []string{""}},
		{`x ""`, []string{"x", ""}},
		{`back\slash out`, []string{"backslash", "out"}},
		{`"esc \" and \\ and \n stay"`, []string{`esc " and \ and \n stay`}},
		{`'no \ escape'`, []string{`no \ escape`}},
		{`punct);(run`, []string{"punct", ");(", "run"}},
		{`a&&b`, []string{"a", "&&", "b"}},
		{"echo `gbrain get p`", []string{"echo", "`", "gbrain", "get", "p", "`"}},
		{"mix\"ed\"$(sub)`tick`", []string{"mixed$", "(", "sub", ")`", "tick", "`"}},
		{"OUT=`x`", []string{"OUT=", "`", "x", "`"}},
		{"a\tb\nc", []string{"a", "b", "c"}},
		{`2>&1`, []string{"2", ">&", "1"}},
		{"w<>|&`;()x", []string{"w", "<>|&`;()", "x"}},
		{`\"start`, []string{`"start`}},
		{`a\ b`, []string{"a b"}},
	}
	for _, c := range ok {
		got, k := gbTok(c.in)
		if !k {
			t.Errorf("gbTok(%q) errored, want %q", c.in, c.want)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("gbTok(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Python raises ValueError -> nil, false
	for _, in := range []string{`unterminated "quote`, `also 'bad`, `trailing backslash \`, `"inner \`} {
		if got, k := gbTok(in); k {
			t.Errorf("gbTok(%q) = %q, want error", in, got)
		}
	}
}

func TestModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cmd  string
		want []string
	}{
		{"gbrain search x; gbrain get y; gbrain search z; gbrain frob w", []string{"search", "get"}},
		{"gbrain\n\tget x", []string{"get"}},
		{"nothing here", nil},
		{"", nil},
		{"gbrain import a && gbrain export b", []string{"import", "export"}},
	}
	for _, c := range cases {
		if got := Modes(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Modes(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestRecordNoModes(t *testing.T) {
	t.Parallel()
	if got := Record("ls -la", "files", "p", "2026-01-01T00:00:00Z"); got != "" {
		t.Errorf("no gbrain verb must yield empty record, got %s", got)
	}
}
