package transcript

import "testing"

func TestSourceCWD(t *testing.T) {
	t.Parallel()
	observer := "/home/me/.claude-mem/observer-sessions"
	prompt := "<observed_from_primary_session>\n<working_directory>/work/acme/widget</working_directory>\n</observed_from_primary_session>"
	if got := SourceCWD(observer, prompt); got != "/work/acme/widget" {
		t.Fatalf("SourceCWD() = %q", got)
	}
	if got := SourceCWD("/work/other", prompt); got != "/work/other" {
		t.Fatalf("ordinary cwd changed to %q", got)
	}
	if got := SourceCWD(observer, "no source"); got != observer {
		t.Fatalf("missing source changed to %q", got)
	}
	if !IsClaudeMemObserverCWD(observer) || IsClaudeMemObserverCWD("/work/other") {
		t.Fatal("observer cwd detection is incorrect")
	}
}
