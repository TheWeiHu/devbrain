package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/transcript"
)

// matchKnown: segment matching with -w/-v suffix strip, strict exact or
// `repo-` prefix, longest-repo-wins, alias applied at the segment level.
func TestMatchKnown(t *testing.T) {
	t.Parallel()
	known := map[string]string{
		"widgets":     "acme__widgets",
		"widgets-api": "acme__widgets-api",
		"app":         "acme__app",
	}
	cases := []struct {
		name, cwd string
		renames   map[string]string
		want      string
	}{
		{"exact segment", "/tmp/acme/widgets", nil, "acme__widgets"},
		{"worker suffix stripped", "/tmp/nightshift/widgets-w3", nil, "acme__widgets"},
		{"variant suffix stripped", "/tmp/drain/widgets-v2", nil, "acme__widgets"},
		{"longest repo wins", "/tmp/x/widgets-api", nil, "acme__widgets-api"},
		{"repo- prefix matches", "/tmp/x/widgets-feature-branch", nil, "acme__widgets"},
		{"short name never matches longer repo", "/tmp/x/widget", nil, ""},
		{"no match", "/tmp/other/thing", nil, ""},
		{"rename routes the segment", "/tmp/x/oldname",
			map[string]string{"oldname": "acme__app"}, "acme__app"},
		{"rename of bare (suffix-stripped) segment", "/tmp/x/oldname-w1",
			map[string]string{"oldname": "acme__app"}, "acme__app"},
	}
	for _, c := range cases {
		if got := matchKnown(c.cwd, known, c.renames); got != c.want {
			t.Errorf("%s: matchKnown(%q) = %q, want %q", c.name, c.cwd, got, c.want)
		}
	}
}

// route confidence levels: alias = high; path match = medium; unresolved =
// miscellaneous/low. (The live-remote high path needs a real git repo and is
// covered by scripts/test-import.sh.)
func TestRouteConfidence(t *testing.T) {
	t.Parallel()
	aliases := map[string]string{"widgets": "acme__widgets"}
	known := map[string]string{"gadgets": "acme__gadgets"}
	if k, c := route("/gone/widgets", aliases, known); k != "acme__widgets" || c != "high" {
		t.Errorf("alias route = %q/%q", k, c)
	}
	if k, c := route("/gone/gadgets-w2", nil, known); k != "acme__gadgets" || c != "medium" {
		t.Errorf("path route = %q/%q", k, c)
	}
	if k, c := route("/gone/unknown", nil, known); k != "miscellaneous" || c != "low" {
		t.Errorf("unresolved route = %q/%q", k, c)
	}
}

func TestCachedRouteResolvesEachCWDOnce(t *testing.T) {
	t.Parallel()
	cache := map[string][2]string{}
	calls := 0
	resolve := func(cwd string) (string, string) {
		calls++
		return "key:" + cwd, "high"
	}
	for range 3 {
		if key, confidence := cachedRoute(cache, "/repo", resolve); key != "key:/repo" || confidence != "high" {
			t.Fatalf("cached result = %q/%q", key, confidence)
		}
	}
	cachedRoute(cache, "/other", resolve)
	if calls != 2 {
		t.Fatalf("resolver calls = %d, want one per distinct cwd", calls)
	}
}

// liveSessions: only non-BACKFILLED logs gate; the banner check reads the
// file head, and the (session, day) view has one pair per live log file.
func TestLiveSessions(t *testing.T) {
	t.Parallel()
	data := t.TempDir()
	write := func(day, name, content string) {
		d := filepath.Join(data, "projects", "p__x", "log", day)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("2026-05-20", "wt.sess1.md", "# live\n\n## 10:00:00\n\nhi\n")
	write("2026-05-21", "wt.sess1.md", "# imported\n"+Banner+"\n## 10:00:00\n\nhi\n")
	live, liveDays := liveSessions(data)
	if !live["sess1"] {
		t.Error("sess1 has a live day and must be live")
	}
	if !liveDays[[2]string{"sess1", "2026-05-20"}] {
		t.Error("live day missing")
	}
	if liveDays[[2]string{"sess1", "2026-05-21"}] {
		t.Error("BACKFILLED day must not count as live")
	}
}

func TestTokenRowJSON(t *testing.T) {
	t.Parallel()
	r := tokenRow{ts: "2026-05-20T10:01:00Z", session: "s1", model: "claude-opus-4-8",
		in: 120, out: 340, cacheCreate: 50, cacheCreate1H: 40, cacheRead: 7000, auto: false,
		longInput: 10, longOutput: 20, longCacheRead: 6000, longContextKnown: true,
		turn: "2026-05-20T10:00:00Z"}
	want := `{"ts": "2026-05-20T10:01:00Z", "session": "s1", "model": "claude-opus-4-8", ` +
		`"in": 120, "out": 340, "cache_create": 50, "cache_create_1h": 40, "cache_read": 7000, ` +
		`"long_input": 10, "long_output": 20, "long_cache_read": 6000, "long_context_known": true, "auto": false, ` +
		`"turn": "2026-05-20T10:00:00Z"}`
	if got := r.json(); got != want {
		t.Errorf("sidecar row:\ngot  %s\nwant %s", got, want)
	}
}

func TestObserverRouting(t *testing.T) {
	t.Parallel()
	observer := "/home/me/.claude-mem/observer-sessions"
	if !autoCWD(observer) {
		t.Fatal("observer import must be autonomous")
	}
	if key, confidence := route(observer, nil, nil); key != transcript.ClaudeMemObserverProject || confidence != "high" {
		t.Fatalf("observer fallback = %q/%q", key, confidence)
	}
	prompt := "<working_directory>/work/acme/widget</working_directory>"
	if got := transcript.SourceCWD(observer, prompt); got != "/work/acme/widget" {
		t.Fatalf("observer source = %q", got)
	}
}
