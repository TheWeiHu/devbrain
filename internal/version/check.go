package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LatestURL is the GitHub API endpoint for the newest published release.
// Overridable in tests.
var LatestURL = "https://api.github.com/repos/TheWeiHu/devbrain/releases/latest"

// checkTTL bounds how often Notice hits the network — at most once per interval.
const checkTTL = 24 * time.Hour

// fetchTimeout caps a single release lookup so Notice never blocks for long.
const fetchTimeout = 1500 * time.Millisecond

type cacheEntry struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// cachePath is the machine-local (never in the data repo) file that remembers
// the last-seen latest version and when we checked. Honors DEVBRAIN_CACHE_DIR
// for tests, else the OS cache dir.
func cachePath() string {
	if d := os.Getenv("DEVBRAIN_CACHE_DIR"); d != "" {
		return filepath.Join(d, "version-check.json")
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "devbrain", "version-check.json")
}

// Notice returns a one-line upgrade nudge like
// "devbrain 1.2.1 → 1.3.0 available · run `brew upgrade devbrain`" when a newer
// release exists, or "" otherwise. It refreshes the cached latest version at
// most once per checkTTL, stamping the check time even on failure so a network
// outage doesn't retry every call. Disabled for source builds (Version=="dev")
// and when DEVBRAIN_NO_UPDATE_CHECK is set.
func Notice() string {
	if Version == "dev" || Version == "" {
		return ""
	}
	if os.Getenv("DEVBRAIN_NO_UPDATE_CHECK") != "" {
		return ""
	}
	c := readCache()
	if time.Since(c.CheckedAt) > checkTTL {
		c = refresh()
	}
	if c.Latest == "" || compare(c.Latest, Version) <= 0 {
		return ""
	}
	cur := strings.TrimPrefix(Version, "v")
	next := strings.TrimPrefix(c.Latest, "v")
	return "⬆ devbrain " + cur + " → " + next + " available · run `brew upgrade devbrain`"
}

func readCache() cacheEntry {
	var c cacheEntry
	if p := cachePath(); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(b, &c)
		}
	}
	return c
}

func writeCache(c cacheEntry) {
	p := cachePath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(p, b, 0o644)
	}
}

// refresh does a time-boxed release lookup and persists the result. The check
// time is stamped regardless of outcome; a failed lookup keeps any previously
// known latest so one transient error doesn't drop the nudge.
func refresh() cacheEntry {
	c := cacheEntry{CheckedAt: time.Now(), Latest: readCache().Latest}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	if latest, err := fetchLatest(ctx); err == nil {
		c.Latest = latest
	}
	writeCache(c)
	return c
}

func fetchLatest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release lookup: status %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

// compare returns -1/0/1 comparing dotted numeric versions a vs b, ignoring a
// leading "v" and any "-prerelease" suffix. Good enough for our clean tags.
func compare(a, b string) int {
	pa, pb := parse(a), parse(b)
	for i := 0; i < 3; i++ {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

func parse(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(part)
	}
	return out
}
