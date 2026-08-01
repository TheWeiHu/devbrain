package brain_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestBrainInstructionsUseWrapper(t *testing.T) {
	repo := filepath.Clean(filepath.Join("..", ".."))
	paths := []string{
		filepath.Join(repo, "assets", "skills"),
		filepath.Join(repo, "assets", "prompts"),
		filepath.Join(repo, "internal", "install", "markers.go"),
		filepath.Join(repo, "internal", "hooks", "hooks.go"),
	}
	forbidden := regexp.MustCompile(`\bgbrain\s+(search|query|ask|get|put|tag|embed|link|import|sync|delete|list|config|doctor|code-[a-z-]+)\b`)

	for _, path := range paths {
		err := filepath.WalkDir(path, func(file string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			body, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if match := forbidden.Find(body); match != nil {
				t.Errorf("%s contains direct brain-engine command %q; use devbrain brain", file, match)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
