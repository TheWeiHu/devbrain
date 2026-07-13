package main

// Go-native port of scripts/test-cross-platform-docker.sh: Tier 2 cross-platform
// clean-room test. Cross-compiles the Go binary for Linux, mounts it into a fresh
// Docker container, and asserts the full machine lifecycle there.
//
// Skips when docker is absent or the daemon is not running — mirrors the script's
// own skip semantics (exit 0, not a failure).

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/TheWeiHu/devbrain/internal/clitest"
)

func TestCrossPlatformDocker(t *testing.T) {
	clitest.SkipIfExcluded(t, "docker") // the nightshift fast merge gate drops this; CI runs it
	dockerSkipIfUnavailable(t)

	image := os.Getenv("IMAGE")
	if image == "" {
		image = "ubuntu:22.04"
	}

	// Determine the docker server arch to cross-compile for.
	arch := dockerServerArch(t)

	// Build a linux binary for the container's arch.
	root := dockerRepoRoot(t)
	tmp := t.TempDir()
	linuxBin := filepath.Join(tmp, "devbrain-linux")

	cmd := exec.Command("go", "build", "-o", linuxBin, "./cmd/devbrain")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-compile linux/%s binary: %v\n%s", arch, err, out)
	}

	t.Logf("devbrain Tier 2 clean-room — image: %s (linux/%s)", image, arch)

	// The container script run inline (passed via -c so we don't need a temp file).
	containerScript := `
set -uo pipefail
fail=0
section(){ printf '\n== %s ==\n' "$1"; }
check(){ if eval "$2"; then echo "  ok   — $1"; else echo "  FAIL — $1 [ $2 ]"; fail=1; fi; }

# deps: git only
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq git ca-certificates cron >/dev/null 2>&1
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y -q git findutils diffutils cronie procps-ng >/dev/null 2>&1
elif command -v yum >/dev/null 2>&1; then
  yum install -y -q git findutils diffutils cronie >/dev/null 2>&1
fi
. /etc/os-release 2>/dev/null || PRETTY_NAME="unknown"
echo "  host: ${PRETTY_NAME:-?} · bash ${BASH_VERSINFO[0]}.${BASH_VERSINFO[1]} · python3 $(command -v python3 >/dev/null 2>&1 && echo present || echo ABSENT) · jq $(command -v jq >/dev/null 2>&1 && echo present || echo ABSENT)"

# clean room: throwaway HOME + a stub claude on PATH
export HOME=/root
git config --global user.email devbrain@localhost
git config --global user.name  devbrain
git config --global init.defaultBranch main
mkdir -p "$HOME/stubbin"
printf '#!/bin/sh\nexit 0\n' > "$HOME/stubbin/claude" && chmod +x "$HOME/stubbin/claude"
export PATH="$HOME/stubbin:$PATH"
export DEVBRAIN_DATA="$HOME/devbrain-data"

section "devbrain version"
check "binary runs on this image" '[ -n "$(devbrain version)" ]'

section "devbrain install --yes (stub claude, no import)"
if DEVBRAIN_NO_IMPORT=1 devbrain install --yes >/tmp/install.log 2>&1; then echo "  ok   — install exit 0"
else echo "  FAIL — install exit $?"; tail -25 /tmp/install.log | sed 's/^/        /'; fail=1; fi
check "settings.json registers gbrain trace"  'grep -q "hook gbrain" "$HOME/.claude/settings.json"'
check "settings.json registers session nudge" 'grep -q "hook session-start" "$HOME/.claude/settings.json"'
check "no retired capture hooks"              '! grep -qE "hook (capture|response|memory)" "$HOME/.claude/settings.json"'
check "config records data dir"          'grep -q "devbrain-data" "$HOME/.config/devbrain/config.json"'
check "data repo initialized"            'git -C "$DEVBRAIN_DATA" rev-parse HEAD >/dev/null 2>&1'
check "skills extracted"                 '[ -f "$HOME/.claude/skills/continue/SKILL.md" ]'
check "no macOS launchd path on Linux"   '[ ! -e "$HOME/Library/LaunchAgents/com.devbrain.flush.plist" ]'
check "flusher took a Linux schedule path" 'grep -qiE "systemd user timer|cron entry|on your own schedule" /tmp/install.log'

section "planted codex rollout -> swept, redacted log"
work="$(mktemp -d)"
git -C "$work" init -q && git -C "$work" remote add origin https://github.com/tier2/proj.git
roll="$HOME/.codex/sessions/2026/07/14"
mkdir -p "$roll"
printf '%s\n%s\n' \
  '{"timestamp":"2026-07-14T10:00:00Z","type":"session_meta","payload":{"id":"019f0000-0000-0000-0000-00000t2sess0","cwd":"'"$work"'"}}' \
  '{"type":"event_msg","timestamp":"2026-07-14T10:00:01Z","payload":{"type":"user_message","message":"a fresh swept prompt with key sk-abcdefghijklmnopqrstuvwx end"}}' \
  > "$roll/rollout-tier2.jsonl"
devbrain sweep --force >/dev/null 2>&1 || true
log="$(find "$DEVBRAIN_DATA/projects/tier2__proj" -name '*.md' -path '*log*' 2>/dev/null | head -1)"
check "swept prompt landed in a log" '[ -n "$log" ] && grep -q "a fresh swept prompt" "$log"'
check "secret redacted"              'grep -q "REDACTED" "$log" && ! grep -q "sk-abcdefghijklmnopqrstuvwx" "$log"'

section "todo roundtrip"
id="$(DEVBRAIN_PROJECT=tier2__proj devbrain todo add "container task" -p 9)"
check "todo add"   '[ -n "$id" ]'
check "todo next"  '[ "$(DEVBRAIN_PROJECT=tier2__proj devbrain todo next)" = "$id" ]'
check "todo claim" 'DEVBRAIN_PROJECT=tier2__proj devbrain todo claim "$id" >/dev/null'
check "todo review" 'DEVBRAIN_PROJECT=tier2__proj devbrain todo review "$id" "https://example.com/pr/1" >/dev/null'
check "todo done"  'DEVBRAIN_PROJECT=tier2__proj devbrain todo done "$id" >/dev/null && DEVBRAIN_PROJECT=tier2__proj devbrain todo show "$id" | grep -q "^done_at: ....-..-..T..:..:..Z"'

section "uninstall clean"
if devbrain uninstall >/tmp/uninstall.log 2>&1; then echo "  ok   — uninstall exit 0"
else echo "  FAIL — uninstall exit $?"; tail -15 /tmp/uninstall.log | sed 's/^/        /'; fail=1; fi
check "hooks deregistered" '! grep -q "devbrain" "$HOME/.claude/settings.json" 2>/dev/null'
check "skills removed"     '[ ! -e "$HOME/.claude/skills/continue" ]'
check "data repo intact"   '[ -n "$log" ] && [ -f "$log" ]'

printf '\n'
[ "$fail" -eq 0 ] && echo "ok Tier 2 ALL GREEN ($PRETTY_NAME)" || echo "FAIL Tier 2 FAILURES ($PRETTY_NAME)"
exit "$fail"
`

	dockerArgs := []string{
		"run", "--rm", "-i",
		"-v", linuxBin + ":/usr/local/bin/devbrain:ro",
		"-e", "TZ=UTC",
		image,
		"bash", "-c", containerScript,
	}

	dockerCmd := exec.Command("docker", dockerArgs...)
	dockerCmd.Stdout = os.Stdout
	dockerCmd.Stderr = os.Stderr
	if err := dockerCmd.Run(); err != nil {
		t.Fatalf("container assertions failed: %v", err)
	}
}

// dockerSkipIfUnavailable skips the test if docker is absent or the daemon is not
// running — UNLESS DEVBRAIN_TEST_REQUIRE names "docker", in which case a would-be
// skip is upgraded to a failure so a runner that's meant to have docker can't go
// green while silently skipping the clean-room install test.
func dockerSkipIfUnavailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		clitest.SkipUnlessRequired(t, "docker", "docker required (not found)")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		clitest.SkipUnlessRequired(t, "docker", "docker daemon not running — start Docker and retry\n%s", out)
	}
	if _, err := exec.LookPath("go"); err != nil {
		clitest.SkipUnlessRequired(t, "docker", "go toolchain not installed")
	}
}

// dockerServerArch returns the architecture string used by the docker server,
// falling back to the host arch when docker info is not fully available.
func dockerServerArch(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("docker", "version", "--format", "{{.Server.Arch}}").Output()
	if err == nil {
		if a := strings.TrimSpace(string(out)); a != "" {
			return a
		}
	}
	// Fallback to host arch.
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		t.Skipf("skip: unsupported host arch %s", runtime.GOARCH)
		return ""
	}
}

// dockerRepoRoot walks up from the test binary's source location to find go.mod.
func dockerRepoRoot(t *testing.T) string {
	t.Helper()
	// Use the clitest harness to get the repo root (walks up to go.mod).
	// We replicate the walk here to avoid import cycles (package main test file).
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from cwd")
		}
		dir = parent
	}
}
