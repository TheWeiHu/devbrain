#!/usr/bin/env bash
# devbrain — Tier 2 cross-platform clean-room test.
# Cross-compiles the Go binary for Linux on the host, mounts it into a fresh
# container, and asserts the full machine lifecycle there: version, install
# (stub claude, no import), piped capture → redacted log, todo roundtrip,
# clean uninstall.
#
#   scripts/test-cross-platform-docker.sh                  # ubuntu:22.04 (default)
#   IMAGE=amazonlinux:2023 scripts/test-cross-platform-docker.sh
set -euo pipefail
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${IMAGE:-ubuntu:22.04}"
# Bail as a SKIP (exit 0), not a FAIL: test-all.sh's SKIP_RE recognizes both these
# messages, but it classifies exit-code-first, so a non-zero exit here would mask as a
# suite FAILURE on any machine without a running Docker daemon (e.g. macOS — devbrain's
# primary platform — with Docker Desktop closed). CI runs Docker, so it still executes.
command -v docker >/dev/null 2>&1 || { echo "docker required (not found)"; exit 0; }
docker info >/dev/null 2>&1 || { echo "docker daemon not running — start Docker and retry"; exit 0; }
command -v go >/dev/null 2>&1 || { echo "skip: go toolchain not installed"; exit 0; }

# Build a Linux binary for the docker engine's native arch.
ARCH="$(docker version --format '{{.Server.Arch}}' 2>/dev/null || true)"
if [ -z "$ARCH" ]; then
  case "$(uname -m)" in
    x86_64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "skip: unsupported host arch $(uname -m)"; exit 0 ;;
  esac
fi
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
echo "▸ devbrain Tier 2 clean-room — image: $IMAGE (linux/$ARCH)"
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 go build -C "$REPO" -o "$TMP/devbrain-linux" ./cmd/devbrain

docker run --rm -i -v "$TMP/devbrain-linux:/usr/local/bin/devbrain:ro" -e "TZ=UTC" "$IMAGE" bash -s <<'CONTAINER'
set -uo pipefail
fail=0
section(){ printf '\n== %s ==\n' "$1"; }
check(){ if eval "$2"; then echo "  ok   — $1"; else echo "  FAIL — $1 [ $2 ]"; fail=1; fi; }

# ── deps: git only (the binary's sole runtime requirement) ───────────────────
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

# ── clean room: throwaway HOME + a stub claude on PATH ───────────────────────
export HOME=/root
git config --global user.email devbrain@localhost
git config --global user.name  devbrain
git config --global init.defaultBranch main
mkdir -p "$HOME/stubbin"
printf '#!/bin/sh\nexit 0\n' > "$HOME/stubbin/claude" && chmod +x "$HOME/stubbin/claude"
export PATH="$HOME/stubbin:$PATH"
export DEVBRAIN_DATA="$HOME/devbrain-data"

section "devbrain version"
check "binary runs on this image"  '[ -n "$(devbrain version)" ]'

section "devbrain install --yes (stub claude, no import)"
if DEVBRAIN_NO_IMPORT=1 devbrain install --yes >/tmp/install.log 2>&1; then echo "  ok   — install exit 0"
else echo "  FAIL — install exit $?"; tail -25 /tmp/install.log | sed 's/^/        /'; fail=1; fi
check "settings.json registers capture"  'grep -q "hook capture" "$HOME/.claude/settings.json"'
check "settings.json registers response" 'grep -q "hook response" "$HOME/.claude/settings.json"'
check "config records data dir"          'grep -q "devbrain-data" "$HOME/.config/devbrain/config.json"'
check "data repo initialized"            'git -C "$DEVBRAIN_DATA" rev-parse HEAD >/dev/null 2>&1'
check "skills extracted"                 '[ -f "$HOME/.claude/skills/continue/SKILL.md" ]'
check "no macOS launchd path on Linux"   '[ ! -e "$HOME/Library/LaunchAgents/com.devbrain.flush.plist" ]'
check "flusher took a Linux schedule path" 'grep -qiE "systemd user timer|cron entry|on your own schedule" /tmp/install.log'

section "piped capture event -> redacted log"
work="$(mktemp -d)"
printf '%s' '{"prompt":"a fresh live prompt with key sk-abcdefghijklmnopqrstuvwx end","cwd":"'"$work"'","session_id":"tier2-sess"}' \
  | DEVBRAIN_PROJECT=tier2proj devbrain hook capture >/dev/null 2>&1 || true
log="$(find "$DEVBRAIN_DATA/projects" -name '*.tier2-sess.md' 2>/dev/null | head -1)"
check "live prompt appended to a log" '[ -n "$log" ] && grep -q "a fresh live prompt" "$log"'
check "secret redacted"               'grep -q "REDACTED" "$log" && ! grep -q "sk-abcdefghijklmnopqrstuvwx" "$log"'

section "todo roundtrip"
id="$(DEVBRAIN_PROJECT=tier2__proj devbrain todo add "container task" -p 9)"
check "todo add"   '[ -n "$id" ]'
check "todo next"  '[ "$(DEVBRAIN_PROJECT=tier2__proj devbrain todo next)" = "$id" ]'
check "todo claim" 'DEVBRAIN_PROJECT=tier2__proj devbrain todo claim "$id" >/dev/null'
check "todo done"  'DEVBRAIN_PROJECT=tier2__proj devbrain todo done "$id" >/dev/null && DEVBRAIN_PROJECT=tier2__proj devbrain todo show "$id" | grep -q "^done_at: ....-..-..T..:..:..Z"'

section "uninstall clean"
if devbrain uninstall >/tmp/uninstall.log 2>&1; then echo "  ok   — uninstall exit 0"
else echo "  FAIL — uninstall exit $?"; tail -15 /tmp/uninstall.log | sed 's/^/        /'; fail=1; fi
check "hooks deregistered" '! grep -q "devbrain" "$HOME/.claude/settings.json" 2>/dev/null'
check "skills removed"     '[ ! -e "$HOME/.claude/skills/continue" ]'
check "data repo intact"   '[ -n "$log" ] && [ -f "$log" ]'

printf '\n'
[ "$fail" -eq 0 ] && echo "✓ Tier 2 ALL GREEN ($PRETTY_NAME)" || echo "✗ Tier 2 FAILURES ($PRETTY_NAME)"
exit "$fail"
CONTAINER
