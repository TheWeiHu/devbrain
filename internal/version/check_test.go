package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.1", "1.3.0", -1},
		{"1.3.0", "1.2.1", 1},
		{"1.3.0", "1.3.0", 0},
		{"v1.3.0", "1.3.0", 0},    // leading v ignored
		{"1.10.0", "1.9.0", 1},    // numeric, not lexical
		{"2.0.0", "1.99.99", 1},   // major wins
		{"1.3.0-rc1", "1.3.0", 0}, // prerelease suffix stripped
	}
	for _, c := range cases {
		if got := compare(c.a, c.b); got != c.want {
			t.Errorf("compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

// withCache points the cache at a temp dir and restores Version afterward.
func withCache(t *testing.T, ver string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEVBRAIN_CACHE_DIR", dir)
	t.Setenv("DEVBRAIN_NO_UPDATE_CHECK", "")
	old := Version
	Version = ver
	t.Cleanup(func() { Version = old })
}

func seedCache(t *testing.T, latest string, checkedAt time.Time) {
	t.Helper()
	p := cachePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cacheEntry{CheckedAt: checkedAt, Latest: latest})
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNoticeFromFreshCache(t *testing.T) {
	withCache(t, "1.2.1")
	seedCache(t, "v1.3.0", time.Now()) // fresh: no network

	got := Notice()
	if got == "" {
		t.Fatal("expected an upgrade notice, got empty")
	}
	if want := "1.3.0"; !contains(got, want) {
		t.Errorf("notice %q missing %q", got, want)
	}

	// Already current → no notice.
	seedCache(t, "v1.2.1", time.Now())
	if got := Notice(); got != "" {
		t.Errorf("expected no notice when current, got %q", got)
	}
}

func TestNoticeDisabled(t *testing.T) {
	withCache(t, "1.2.1")
	seedCache(t, "v9.9.9", time.Now())

	t.Setenv("DEVBRAIN_NO_UPDATE_CHECK", "1")
	if got := Notice(); got != "" {
		t.Errorf("opt-out should silence notice, got %q", got)
	}

	t.Setenv("DEVBRAIN_NO_UPDATE_CHECK", "")
	Version = "dev"
	if got := Notice(); got != "" {
		t.Errorf("source build should silence notice, got %q", got)
	}
}

func TestNoticeRefreshesStaleCache(t *testing.T) {
	withCache(t, "1.2.1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.4.0"}`))
	}))
	defer srv.Close()
	oldURL := LatestURL
	LatestURL = srv.URL
	defer func() { LatestURL = oldURL }()

	seedCache(t, "v1.2.1", time.Now().Add(-48*time.Hour)) // stale → triggers fetch

	got := Notice()
	if !contains(got, "1.4.0") {
		t.Errorf("stale cache should refresh to 1.4.0, got %q", got)
	}
	// Cache should now be updated and stamped fresh.
	if c := readCache(); c.Latest != "v1.4.0" || time.Since(c.CheckedAt) > time.Minute {
		t.Errorf("cache not refreshed: %+v", c)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
