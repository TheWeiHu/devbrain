package nightshift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunEventsAndFailureArtifactsAreBoundedAndRedacted(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755); err != nil {
		t.Fatal(err)
	}
	opt := DefaultOptions()
	opt.Repo = repo
	o := NewOrch(opt, os.Stdout)
	o.RunID = "run-1"
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	o.emitEvent(runEvent{Type: "turn_end", Worker: 0, Task: "0001-test", Outcome: "failed", Detail: "token=" + secret})
	b, err := os.ReadFile(opt.EventsFile())
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, secret) || !strings.Contains(text, "[REDACTED]") || !strings.Contains(text, `"worker":0`) {
		t.Fatalf("event must retain worker zero and redact secrets: %s", text)
	}

	rel := o.writeFailureArtifact("0001-test", "gate", strings.Repeat("failure ", 4000)+secret)
	if rel == "" || filepath.IsAbs(rel) {
		t.Fatalf("artifact path = %q", rel)
	}
	artifact, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact) > (16<<10)+1 || strings.Contains(string(artifact), secret) {
		t.Fatalf("artifact len=%d secret_present=%v", len(artifact), strings.Contains(string(artifact), secret))
	}
}

func TestFailureCategory(t *testing.T) {
	for input, want := range map[string]string{
		"merge conflict with nightshift":        "merge_conflict",
		"gate failed: test":                     "gate",
		"worker turn produced no pushed branch": "no_branch",
		"git push to nightshift failed":         "git_push",
	} {
		if got := failureCategory(input); got != want {
			t.Errorf("failureCategory(%q)=%q want %q", input, got, want)
		}
	}
}
