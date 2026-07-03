package brain

import (
	"os"
	"path/filepath"
	"testing"
)

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
