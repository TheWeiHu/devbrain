package nightshift_test

// Port of scripts/test-nightshift-backfill.sh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestNightshiftBackfill(t *testing.T) {
	h := clitest.New(t)

	// Isolate HOME so no real installed importer is found.
	fakeHome := t.TempDir()
	h.Env["HOME"] = fakeHome

	// claude stub on PATH so preflight passes.
	binDir := filepath.Join(h.Data, "bin")
	clitest.WriteExec(t, filepath.Join(binDir, "claude"), "#!/usr/bin/env bash\nexit 0\n")
	h.Env["PATH"] = binDir + string(os.PathListSeparator) + os.Getenv("PATH")

	// A real repo dir is required for --repo.
	base := filepath.Join(h.Data, "repo")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	ns := func(extraEnv map[string]string, args ...string) clitest.Result {
		full := append([]string{"nightshift", "internal"}, args...)
		full = append(full, "--repo", base)
		return h.RunWith(clitest.RunOpts{Env: extraEnv}, full...)
	}

	// Build a stub importer that records the exact args it was called with.
	hookDir := filepath.Join(fakeHome, ".claude", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	importerPath := filepath.Join(hookDir, "devbrain-import")
	sentinel := filepath.Join(h.Data, "import.args")

	clitest.WriteExec(t, importerPath,
		"#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" > "+sentinel+"\nexit 0\n")

	customData := filepath.Join(h.Data, "custom-data")
	_ = os.Remove(sentinel)
	env := map[string]string{
		"DEVBRAIN_DATA":       customData,
		"DEVBRAIN_IMPORT_CMD": importerPath,
	}
	out := ns(env, "backfill-tokens")

	if _, err := os.Stat(sentinel); err != nil {
		t.Error("backfill-tokens did not invoke the importer")
	}

	sentinelContent := ""
	if b, err := os.ReadFile(sentinel); err == nil {
		sentinelContent = string(b)
	}
	if !strings.Contains(sentinelContent, "--apply") || !strings.Contains(sentinelContent, "--tokens-only") {
		t.Errorf("importer not called with --apply --tokens-only; args: %q", sentinelContent)
	}
	if !strings.Contains(sentinelContent, "--data "+customData) {
		t.Errorf("importer not called with --data <DEVBRAIN_DATA>; args: %q", sentinelContent)
	}
	if !strings.Contains(strings.ToLower(out.Stdout+out.Stderr), "backfill") {
		t.Errorf("backfill-tokens did not announce the backfill; stdout=%q stderr=%q", out.Stdout, out.Stderr)
	}

	// Failing importer must not cause a non-zero exit.
	clitest.WriteExec(t, importerPath, "#!/usr/bin/env bash\nexit 1\n")
	r := ns(map[string]string{"DEVBRAIN_IMPORT_CMD": importerPath}, "backfill-tokens")
	if r.Code != 0 {
		t.Errorf("failing importer must be best-effort (exit 0), got exit %d", r.Code)
	}

	// Absent importer is a clean no-op.
	_ = os.Remove(importerPath)
	r = ns(map[string]string{"DEVBRAIN_IMPORT_CMD": importerPath}, "backfill-tokens")
	if r.Code != 0 {
		t.Errorf("absent importer must be a clean no-op (exit 0), got exit %d", r.Code)
	}
}
