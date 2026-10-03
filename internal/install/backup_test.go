package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// backup keeps a restore point per settings edit but must not grow without
// bound: only the newest keepBackups timestamped copies survive, and backups
// with any other suffix are left alone.
func TestBackupPrunesOldTimestampedCopies(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ { // eight stale copies, .bak.1001 oldest
		if err := os.WriteFile(fmt.Sprintf("%s.bak.%d", p, 1000+i), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	foreign := p + ".bak-jbrain" // not our pattern
	if err := os.WriteFile(foreign, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	backup(p)

	got, _ := filepath.Glob(p + ".bak.*")
	if len(got) != keepBackups {
		t.Fatalf("kept %d timestamped backups, want %d: %v", len(got), keepBackups, got)
	}
	for _, g := range got {
		for _, old := range []string{".bak.1001", ".bak.1002", ".bak.1003", ".bak.1004"} {
			if strings.HasSuffix(g, old) {
				t.Errorf("stale backup survived: %s", g)
			}
		}
	}
	if !exists(foreign) {
		t.Error("a backup with a foreign suffix was deleted")
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "{}" {
		t.Errorf("source file changed: %q %v", b, err)
	}
}
