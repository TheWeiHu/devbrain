package install

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func doctor(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	rc := Doctor(args, &out, &out)
	return out.String(), rc
}

// A stale absolute hook path (the binary moved/was replaced after an upgrade) is
// the usual reason capture silently stops. doctor must flag it, --fix must
// re-point the hooks at the current binary, and a re-run must then pass.
func TestDoctorDetectsAndRepairsStaleHookPath(t *testing.T) {
	home := setupHome(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	// The friend's state: hooks point at a binary that no longer exists.
	os.WriteFile(settings, []byte(`{"hooks":{
      "UserPromptSubmit":[{"hooks":[{"type":"command","command":"/gone/old/devbrain hook capture"}]}],
      "Stop":[{"hooks":[{"type":"command","command":"/gone/old/devbrain hook response"}]}]
    }}`), 0o644)

	if out, rc := doctor(t); rc != 1 || !strings.Contains(out, "STALE") {
		t.Fatalf("report should flag stale wiring (rc=%d):\n%s", rc, out)
	}

	if out, rc := doctor(t, "--fix"); rc != 0 || !strings.Contains(out, "re-pointed") {
		t.Fatalf("--fix should repair (rc=%d):\n%s", rc, out)
	}

	// Hooks now point at the current binary, and the dead path is gone.
	got := mustRead(t, settings)
	want := BinaryPath() + " hook capture"
	if !strings.Contains(got, want) {
		t.Errorf("settings not re-pointed at current binary; want %q in:\n%s", want, got)
	}
	if strings.Contains(got, "/gone/old/devbrain") {
		t.Errorf("stale hook command should have been stripped:\n%s", got)
	}

	if out, rc := doctor(t); rc != 0 || !strings.Contains(out, "healthy") {
		t.Fatalf("report should pass after --fix (rc=%d):\n%s", rc, out)
	}
}

// No devbrain hooks at all → capture is not wired; say so and fail.
func TestDoctorFlagsUnwired(t *testing.T) {
	home := setupHome(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte(`{"hooks":{}}`), 0o644)

	out, rc := doctor(t)
	if rc != 1 || !strings.Contains(out, "not wired") {
		t.Fatalf("empty wiring should fail with a wire hint (rc=%d):\n%s", rc, out)
	}
}

// --fix must never claim success on a file it can't read/parse.
func TestDoctorRefusesMissingAndMalformed(t *testing.T) {
	home := setupHome(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)

	if out, rc := doctor(t, "--fix"); rc != 1 || !strings.Contains(out, "not wired") {
		t.Fatalf("missing settings.json: want rc 1 + no false repair, got rc=%d:\n%s", rc, out)
	}
	os.WriteFile(settings, []byte(`{ not json`), 0o644)
	if out, rc := doctor(t, "--fix"); rc != 1 || !strings.Contains(out, "not valid JSON") {
		t.Fatalf("malformed settings.json: want rc 1 + refuse, got rc=%d:\n%s", rc, out)
	}
}

// --fix strips ALL our stale duplicates, leaves third-party hooks alone, and
// backfill guidance points at the same import first-install uses.
func TestDoctorFixKeepsThirdPartyAndDedups(t *testing.T) {
	home := setupHome(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte(`{"hooks":{"UserPromptSubmit":[
      {"hooks":[{"type":"command","command":"/gone/devbrain hook capture"}]},
      {"hooks":[{"type":"command","command":"/also/gone/devbrain hook capture"}]},
      {"hooks":[{"type":"command","command":"/usr/local/bin/othertool hook capture"}]}
    ]}}`), 0o644)

	out, rc := doctor(t, "--fix")
	if rc != 0 || !strings.Contains(out, "import --apply") {
		t.Fatalf("--fix should repair + recommend backfill (rc=%d):\n%s", rc, out)
	}
	got := mustRead(t, settings)
	if !strings.Contains(got, "/usr/local/bin/othertool hook capture") {
		t.Errorf("a third-party hook must not be touched:\n%s", got)
	}
	if strings.Contains(got, "/gone/") || strings.Contains(got, "/also/gone/") {
		t.Errorf("every stale devbrain duplicate must be stripped:\n%s", got)
	}
	if n := strings.Count(got, BinaryPath()+" hook capture"); n != 1 {
		t.Errorf("want exactly one re-pointed capture hook, got %d:\n%s", n, got)
	}
}
