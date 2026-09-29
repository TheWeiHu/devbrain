package heartbeat

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheWeiHu/devbrain/internal/gitx"
)

func selectTestBrain(t *testing.T, dir string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("DEVBRAIN_DATA", dir)
	t.Setenv("DEVBRAIN_BRAIN", "")
	if err := os.MkdirAll(filepath.Join(base, "devbrain"), 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"brains": map[string]string{"test": dir}, "default_brain": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "devbrain", "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRetirementIsReversibleAndScopedToBrain(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	Now = func() time.Time { return now }
	defer func() { Now = time.Now }()
	entries := []Host{{Name: "old-box", Last: now.Add(-7 * 24 * time.Hour)}}
	dir, other := repoWith(t, entries), repoWith(t, entries)
	selectTestBrain(t, dir)
	var out, errs bytes.Buffer
	if code := Run([]string{"retire", "old-box"}, &out, &errs); code != 0 {
		t.Fatalf("retire: %d %s", code, &errs)
	}
	if Warning(dir) != "" || Warning(other) == "" {
		t.Fatal("retirement must silence only the selected brain")
	}
	out.Reset()
	if Run([]string{"list"}, &out, &errs) != 0 || !strings.Contains(out.String(), "old-box\tretired") {
		t.Fatalf("list: %s %s", &out, &errs)
	}
	if Run([]string{"restore", "old-box"}, &out, &errs) != 0 || Warning(dir) == "" {
		t.Fatalf("restore must restore warning: %s", &errs)
	}
}

func TestNewCaptureRevivesRetiredHost(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	Now = func() time.Time { return now }
	defer func() { Now = time.Now }()
	last := now.Add(-7 * 24 * time.Hour)
	dir := repoWith(t, []Host{{Name: "old-box", Last: last}})
	if err := writeRetired(dir, map[string]time.Time{"old-box": last}); err != nil {
		t.Fatal(err)
	}
	repo := gitx.Repo{Dir: dir}
	stamp := now.Add(-72 * time.Hour).Format(time.RFC3339)
	if _, err := repo.RunEnv([]string{"GIT_COMMITTER_DATE=" + stamp, "GIT_AUTHOR_DATE=" + stamp},
		"commit", "--allow-empty", "-qm", "capture: again on old-box"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(Warning(dir), "old-box") {
		t.Fatal("new capture must reactivate stale-host warnings")
	}
}

func TestRetirementCannotHideLocalFailureOrMalformedState(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	Now = func() time.Time { return now }
	defer func() { Now = time.Now }()
	last := now.Add(-72 * time.Hour)
	dir := repoWith(t, []Host{{Name: selfHost(), Last: last}})
	selectTestBrain(t, dir)
	var out, errs bytes.Buffer
	if Run([]string{"retire", selfHost()}, &out, &errs) == 0 {
		t.Fatal("must reject local retirement")
	}
	if Run([]string{"retire", "typo-host"}, &out, &errs) == 0 {
		t.Fatal("must reject unknown host")
	}
	if err := writeRetired(dir, map[string]time.Time{selfHost(): last}); err != nil {
		t.Fatal(err)
	}
	if Warning(dir) == "" {
		t.Fatal("handwritten retirement must not hide local failure")
	}
	p := filepath.Join(dir, retiredFile)
	if err := os.WriteFile(p, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Run([]string{"restore", selfHost()}, &out, &errs) == 0 || Warning(dir) == "" {
		t.Fatal("malformed state must fail mutation and retain warnings")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "broken" {
		t.Fatal("malformed retirement state was overwritten")
	}
}
