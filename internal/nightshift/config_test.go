package nightshift

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDefaults pins every orchestrator default against the script's header.
func TestDefaults(t *testing.T) {
	o := DefaultOptions()
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"N", o.Workers, 3}, {"POLL", o.Poll, 15}, {"TURN_MAX", o.TurnMax, 1800},
		{"HANG", o.Hang, 600}, {"REPLAN", o.Replan, 300}, {"RETRIES", o.Retries, 2},
		{"CLAIM_TTL", o.ClaimTTL, 5400}, {"STALL_K", o.StallK, 8},
		{"RECON_EVERY", o.ReconEvery, 8}, {"LIMIT_BACKOFF", o.LimitBackoff, 300},
		{"RESEND_GRACE", o.ResendGrace, 60}, {"LOW", o.Low, 2},
		{"MODE", o.Mode, "headless"}, {"BASE_BRANCH", o.BaseBranch, "main"},
		{"FOREVER", o.Forever, true}, {"GATE_PY", o.GatePy, "python3"},
		{"MAXTURNS", o.MaxTurns, 0}, {"MAXWALL", o.MaxWall, 0},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("default %s = %v want %v", c.name, c.got, c.want)
		}
	}
}

func TestParseArgs(t *testing.T) {
	o, err := ParseArgs([]string{
		"--repo", "/r", "--workers", "5", "--tmux", "--turn-timeout", "900",
		"--only", "0001,0002", "--max-turns", "4", "--base-branch", "dev",
		"--keep-nightshift", "--test-cmd", "make test", "--no-gate",
		"--strict-gate", "--retries", "7", "--notify", "--replan", "60",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Repo != "/r" || o.Workers != 5 || o.Mode != "tmux" || o.TurnMax != 900 ||
		o.Only != "0001,0002" || !o.OnlyGiven || o.MaxTurns != 4 || o.Forever ||
		o.BaseBranch != "dev" || !o.KeepNightshift || o.TestCmd != "make test" ||
		!o.NoGate || !o.Strict || o.Retries != 7 || !o.Notify || o.Replan != 60 {
		t.Errorf("ParseArgs mis-parsed: %+v", o)
	}
	if _, err := ParseArgs([]string{"--bogus"}); err == nil {
		t.Error("unknown arg must error (the script exits 1)")
	}
	if _, err := ParseArgs([]string{"--workers"}); err == nil {
		t.Error("missing value must error")
	}
}

func TestParseCodexMode(t *testing.T) {
	o, err := ParseArgs([]string{"--repo", "/r", "--codex-model", "gpt-5.6-sol", "--codex-reasoning", "high"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Mode != "codex" {
		t.Fatalf("Mode = %q want codex", o.Mode)
	}
	if o.CodexModel != "gpt-5.6-sol" {
		t.Fatalf("CodexModel = %q want gpt-5.6-sol", o.CodexModel)
	}
	if o.CodexReasoning != "high" {
		t.Fatalf("CodexReasoning = %q want high", o.CodexReasoning)
	}
	if !o.ProcessBackend() {
		t.Fatal("codex must share the process-backed worker lifecycle")
	}
	o.Mode = "headless"
	if !o.ProcessBackend() {
		t.Fatal("headless must remain a process-backed worker lifecycle")
	}
	o.Mode = "tmux"
	if o.ProcessBackend() {
		t.Fatal("tmux must not report process-backed lifecycle")
	}
	if _, err := ParseArgs([]string{"--codex-model", "gpt-5.6-sol", "--claude"}); err == nil {
		t.Fatal("a Codex model must not be silently ignored by another backend")
	}
	if _, err := ParseArgs([]string{"--codex-reasoning", "high", "--tmux"}); err == nil {
		t.Fatal("Codex reasoning must not be silently ignored by another backend")
	}
	if _, err := ParseArgs([]string{"--codex-model", "--claude"}); err == nil {
		t.Fatal("a missing Codex model value must not consume the next flag")
	}
	if _, err := ParseArgs([]string{"--codex-reasoning", ""}); err == nil {
		t.Fatal("an empty Codex reasoning value must fail")
	}
}

func TestParseClaudeAlias(t *testing.T) {
	o, err := ParseArgs([]string{"--repo", "/r", "--codex", "--claude"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Mode != "headless" {
		t.Fatalf("Mode = %q want headless", o.Mode)
	}
}

func TestWriteRuntime(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".nightshift"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := DefaultOptions()
	o.Repo, o.Mode, o.CodexModel, o.CodexReasoning = repo, "codex", "gpt-5.6-sol", "high"
	if err := o.WriteRuntime(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(o.RuntimeFile())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["backend"] != "codex" || got["model"] != "gpt-5.6-sol" || got["reasoning"] != "high" {
		t.Fatalf("runtime metadata = %v", got)
	}
	o.CodexModel, o.CodexReasoning = "", ""
	if err := o.WriteRuntime(); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(o.RuntimeFile())
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "inherited" || got["reasoning"] != "inherited" {
		t.Fatalf("inherited runtime metadata = %v", got)
	}
}

func TestInferredStartMode(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "thread-1")
	t.Setenv("CODEX_WORKING_DIR", "")
	if got := inferredStartMode(); got != "codex" {
		t.Fatalf("Codex session mode = %q", got)
	}
	t.Setenv("CODEX_THREAD_ID", "")
	if got := inferredStartMode(); got != "headless" {
		t.Fatalf("plain shell mode = %q", got)
	}
	t.Setenv("CODEX_WORKING_DIR", "/repo")
	if got := inferredStartMode(); got != "codex" {
		t.Fatalf("Codex working-dir mode = %q", got)
	}
}

func TestDerivedPaths(t *testing.T) {
	o := DefaultOptions()
	o.Repo = "/x/repo"
	if o.StageWT() != "/x/repo-stage" ||
		o.Venv() != "/x/repo/.nightshift/venv" ||
		o.RetryDir() != "/x/repo/.nightshift/retries" ||
		o.LandedFile() != "/x/repo/.nightshift/landed.tsv" ||
		o.WorkerWT(2) != "/x/repo-w2" {
		t.Errorf("derived paths wrong: %s %s %s %s %s",
			o.StageWT(), o.Venv(), o.RetryDir(), o.LandedFile(), o.WorkerWT(2))
	}
}

func TestRepoConfigAndRunRegistration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := t.TempDir()
	t.Setenv("DEVBRAIN_DATA", data)
	t.Setenv("DEVBRAIN_PROJECT", "test__repo")

	repo := t.TempDir()
	if err := SaveRepo(repo); err != nil {
		t.Fatal(err)
	}
	if got := ResolveRepo(""); got != repo {
		t.Errorf("ResolveRepo remembered = %q want %q", got, repo)
	}
	other := t.TempDir()
	if got := ResolveRepo(other); got != other {
		t.Errorf("explicit repo wins: got %q want %q", got, other)
	}
	if got := ResolveRepo("/does/not/exist"); got != repo {
		t.Errorf("non-dir explicit falls back to remembered: got %q", got)
	}

	// No project dir yet → registration is a silent no-op.
	if DataProjectDir(repo) != "" {
		t.Error("DataProjectDir should be empty before the project dir exists")
	}
	pd := filepath.Join(data, "projects", "test__repo")
	os.MkdirAll(pd, 0o755)
	if err := RegisterRun(repo, 8799); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(pd, "nightshift-run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		Port int    `json:"port"`
		Repo string `json:"repo"`
	}
	if json.Unmarshal(b, &run) != nil || run.Port != 8799 || run.Repo != repo {
		t.Errorf("run marker wrong: %s", b)
	}
	UnregisterRun(repo)
	if _, err := os.Stat(filepath.Join(pd, "nightshift-run.json")); err == nil {
		t.Error("UnregisterRun should remove the marker")
	}
}
