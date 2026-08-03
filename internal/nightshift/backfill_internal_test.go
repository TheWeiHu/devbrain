package nightshift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackfillTokenCostTimesOut(t *testing.T) {
	t.Setenv("DEVBRAIN_DATA", t.TempDir())

	importer := filepath.Join(t.TempDir(), "hanging-importer")
	if err := os.WriteFile(importer, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVBRAIN_IMPORT_CMD", importer)

	var log strings.Builder
	o := &Orch{Out: &log}
	started := time.Now()
	o.backfillTokenCost(100 * time.Millisecond)

	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("best-effort backfill blocked cleanup for %s", elapsed)
	}
	if !strings.Contains(log.String(), "timed out after 100ms; continuing shutdown") {
		t.Fatalf("missing timeout diagnostic: %q", log.String())
	}
}
