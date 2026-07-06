package brain

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rebuild puts each page under the canonical <project>/<page> slug AND deletes
// the path-form twin (projects/<project>/brain/<page>) a raw `gbrain import`
// would have created — so a polluted brain self-heals on rebuild.
func TestRebuildPrunesPathFormTwin(t *testing.T) {
	data := t.TempDir()
	bp := filepath.Join(data, "projects", "ns__demo", "brain")
	if err := os.MkdirAll(bp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bp, "nightshift.md"), []byte("# ns\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("OPENAI_API_KEY", "")

	// Stub gbrain: log each invocation's args, one space-joined line per call.
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls.log")
	stub := filepath.Join(bin, "gbrain")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+calls+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVBRAIN_GBRAIN", stub)

	if rc := Rebuild(io.Discard, io.Discard); rc != 0 {
		t.Fatalf("rebuild rc=%d", rc)
	}
	b, _ := os.ReadFile(calls)
	got := string(b)
	for _, want := range []string{
		"put ns__demo/nightshift",                   // canonical slug
		"delete projects/ns__demo/brain/nightshift", // path-form twin pruned
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in gbrain calls:\n%s", want, got)
		}
	}
}

// hasOpenAIKey drives whether `devbrain rebuild` reports keyword-only vs
// semantic embedding — so pin both detection routes (env, ~/.gbrain config).
func TestHasOpenAIKey(t *testing.T) {
	// Isolate HOME so the machine's real ~/.gbrain/config.json can't leak in.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "")

	t.Run("none configured", func(t *testing.T) {
		if hasOpenAIKey() {
			t.Error("want false with no env key and no config file")
		}
	})

	t.Run("env key", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-env")
		if !hasOpenAIKey() {
			t.Error("want true when OPENAI_API_KEY is set")
		}
	})

	t.Run("blank env key ignored", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "   ")
		if hasOpenAIKey() {
			t.Error("want false for whitespace-only OPENAI_API_KEY")
		}
	})

	t.Run("gbrain config key", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		dir := filepath.Join(home, ".gbrain")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"),
			[]byte(`{"openai_api_key":"sk-cfg"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if !hasOpenAIKey() {
			t.Error("want true when config.json carries openai_api_key")
		}
	})
}
