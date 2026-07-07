package nightshift

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/config"
)

func TestPrependPATH(t *testing.T) {
	sep := string(os.PathListSeparator)
	// dir prepended in front of an existing PATH
	got := prependPATH([]string{"FOO=1", "PATH=/usr/bin" + sep + "/bin"}, "/opt/x")
	want := "PATH=/opt/x" + sep + "/usr/bin" + sep + "/bin"
	if got[1] != want {
		t.Errorf("prepend: got %q want %q", got[1], want)
	}
	// empty dir is a no-op
	in := []string{"PATH=/usr/bin"}
	if out := prependPATH(in, ""); out[0] != "PATH=/usr/bin" {
		t.Errorf("empty dir must no-op, got %q", out[0])
	}
	// no PATH present -> one is created
	out := prependPATH([]string{"FOO=1"}, "/opt/x")
	if out[len(out)-1] != "PATH=/opt/x" {
		t.Errorf("missing PATH must be created, got %v", out)
	}
}

func TestShSingleQuote(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/opt/bun/bin", `'/opt/bun/bin'`},
		{"/has space/bin", `'/has space/bin'`},
		{"/it's/bin", `'/it'\''s/bin'`},
	} {
		if got := shSingleQuote(c.in); got != c.want {
			t.Errorf("shSingleQuote(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestWorkerGbrainDirHeadless(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")
	// Force LookPath("gbrain") to fail: a PATH with no gbrain in it.
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	// (1) no config recorded -> nothing to add
	if got := workerGbrainDir(true); got != "" {
		t.Errorf("no config: got %q want empty", got)
	}

	// (2) config points at a dir that really holds gbrain -> return it
	gbdir := t.TempDir()
	writeExec(t, filepath.Join(gbdir, "gbrain"))
	if err := config.SetGbrainDir(gbdir); err != nil {
		t.Fatal(err)
	}
	if got := workerGbrainDir(true); got != gbdir {
		t.Errorf("valid hint: got %q want %q", got, gbdir)
	}

	// (3) recorded dir no longer holds gbrain, none on PATH -> self-heal to empty
	if err := os.Remove(filepath.Join(gbdir, "gbrain")); err != nil {
		t.Fatal(err)
	}
	if got := workerGbrainDir(true); got != "" {
		t.Errorf("stale hint must self-heal, got %q", got)
	}

	// (4) headless: gbrain already on PATH -> nothing to add (workers inherit it)
	writeExec(t, filepath.Join(gbdir, "gbrain"))
	t.Setenv("PATH", gbdir+string(os.PathListSeparator)+empty)
	if got := workerGbrainDir(true); got != "" {
		t.Errorf("headless, gbrain on PATH: got %q want empty", got)
	}
}

func TestWorkerGbrainDirTmux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEVBRAIN_DATA", "")

	// tmux panes may not inherit the orchestrator PATH, so even when gbrain IS
	// resolvable here the recorded dir must still be returned for injection.
	gbdir := t.TempDir()
	writeExec(t, filepath.Join(gbdir, "gbrain"))
	if err := config.SetGbrainDir(gbdir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", gbdir) // gbrain reachable from the orchestrator too
	if got := workerGbrainDir(false); got != gbdir {
		t.Errorf("tmux must inject dir even when gbrain on PATH: got %q want %q", got, gbdir)
	}

	// Config hint wins over PATH order: a valid recorded dir is preferred.
	other := t.TempDir()
	writeExec(t, filepath.Join(other, "gbrain"))
	t.Setenv("PATH", other)
	if got := workerGbrainDir(false); got != gbdir {
		t.Errorf("config hint should win: got %q want %q", got, gbdir)
	}

	// No config + none on PATH -> empty.
	if err := config.SetGbrainDir(""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if got := workerGbrainDir(false); got != "" {
		t.Errorf("no gbrain anywhere: got %q want empty", got)
	}
}

func writeExec(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
