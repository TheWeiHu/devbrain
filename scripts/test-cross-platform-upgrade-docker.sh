#!/usr/bin/env bash
# devbrain — Tier 2 clean-room UPGRADE test (existing install → new version).
#
# The fresh-install path is covered by scripts/test-cross-platform-docker.sh; this
# proves the OTHER half install.sh is responsible for: re-running ./setup over an
# ALREADY-installed older version updates the runtime in place and reflects the new
# version, without leaving stale (renamed/removed) artifacts behind. install.sh is
# the highest-churn surface, and its upgrade-cleanup `rm -f` lines had no test.
#
# It installs an "old" version (VERSION stamped to a sentinel), seeds the stale
# files a genuinely older install would have left on disk, then upgrades in place
# (VERSION restored to HEAD) and asserts: version bumped, hooks/CLI re-installed,
# the stale artifacts cleaned up, and settings still wired. No auth needed.
#
#   scripts/test-cross-platform-upgrade-docker.sh             # ubuntu:22.04 (default)
#   IMAGE=amazonlinux:2023 scripts/test-cross-platform-upgrade-docker.sh
#
# Named "*-docker.sh" so the fast nightshift merge gate (DEVBRAIN_TEST_SKIP='docker|
# dogfood') skips it by name like its sibling; CI runs the full set.
set -euo pipefail
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${IMAGE:-ubuntu:22.04}"
command -v docker >/dev/null 2>&1 || { echo "docker required (not found)"; exit 1; }
docker info >/dev/null 2>&1 || { echo "docker daemon not running — start Docker and retry"; exit 1; }

echo "▸ devbrain Tier 2 upgrade clean-room — image: $IMAGE"
# repo mounted read-only; container copies it to a writable tree to run from
docker run --rm -i -v "$REPO:/repo:ro" -e "TZ=UTC" "$IMAGE" bash -s <<'CONTAINER'
set -uo pipefail
fail=0
section(){ printf '\n== %s ==\n' "$1"; }
check(){ if eval "$2"; then echo "  ok   — $1"; else echo "  FAIL — $1 [ $2 ]"; fail=1; fi; }

# ── deps (distro-detect; keep it quiet) ──────────────────────────────────────
# jq deliberately NOT installed: devbrain is jq-free, so a jq-less room is the path.
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq git python3 cron ca-certificates >/dev/null 2>&1
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y -q git python3 findutils diffutils cronie procps-ng >/dev/null 2>&1
elif command -v yum >/dev/null 2>&1; then
  yum install -y -q git python3 findutils diffutils cronie >/dev/null 2>&1
fi
. /etc/os-release 2>/dev/null || PRETTY_NAME="unknown"
echo "  host: ${PRETTY_NAME:-?} · bash ${BASH_VERSINFO[0]}.${BASH_VERSINFO[1]}"
command -v python3 >/dev/null 2>&1 || { echo "FAIL — python3 could not be installed; aborting"; exit 1; }

# ── clean room ───────────────────────────────────────────────────────────────
export HOME=/root
cp -a /repo /work; cd /work
rm -rf /work/.git                       # install doesn't need /work to be a repo
git config --global user.email devbrain@localhost
git config --global user.name  devbrain
git config --global init.defaultBranch main

BIN="$HOME/.claude/hooks"
DEVBRAIN="$BIN/devbrain"
export DEVBRAIN_DATA="$HOME/devbrain-data"
NEWVER="$(cat /work/VERSION)"
OLDVER="0.0.0-pre-upgrade"
[ -n "$NEWVER" ] && [ "$NEWVER" != "$OLDVER" ] || { echo "FAIL — VERSION unreadable or equals sentinel"; exit 1; }

# ── 1. install the "old" version ─────────────────────────────────────────────
section "old install (VERSION=$OLDVER)"
printf '%s\n' "$OLDVER" > /work/VERSION
if DEVBRAIN_NIGHTSHIFT=0 ./setup >/tmp/old.log 2>&1; then echo "  ok   — old setup exit 0"
else echo "  FAIL — old setup exit $?"; tail -25 /tmp/old.log | sed 's/^/        /'; fail=1; fi
check "old devbrain CLI installed"  '[ -x "$DEVBRAIN" ]'
check "version file reports OLD"    '[ "$(bash "$DEVBRAIN" version)" = "$OLDVER" ]'

# Seed the stale artifacts a genuinely older install left behind — the exact files
# install.sh's upgrade path `rm -f`s (the pre-rename dashboard + the stray release
# copy). The upgrade must remove them.
echo "stale pre-rename dashboard" > "$BIN/devbrain-queue-dashboard.html"
echo "stray release script"       > "$BIN/devbrain-release.sh"
check "stale artifacts seeded"      '[ -e "$BIN/devbrain-queue-dashboard.html" ] && [ -e "$BIN/devbrain-release.sh" ]'

# ── 2. upgrade in place to the new (HEAD) version ────────────────────────────
section "upgrade in place (VERSION=$NEWVER)"
printf '%s\n' "$NEWVER" > /work/VERSION
if DEVBRAIN_NIGHTSHIFT=0 ./setup >/tmp/new.log 2>&1; then echo "  ok   — upgrade setup exit 0"
else echo "  FAIL — upgrade setup exit $?"; tail -25 /tmp/new.log | sed 's/^/        /'; fail=1; fi

# ── 3. the upgrade is reflected everywhere ───────────────────────────────────
section "new version reflected"
check "version file reports NEW"         '[ "$(bash "$DEVBRAIN" version)" = "$NEWVER" ]'
check "installed VERSION file bumped"     'grep -qx "$NEWVER" "$BIN/devbrain.version"'
check "capture hook re-installed + exec"  '[ -x "$BIN/devbrain-capture.sh" ]'
check "todo CLI present + exec"           '[ -x "$BIN/devbrain-todo.sh" ]'
check "unified devbrain CLI present"      '[ -x "$DEVBRAIN" ]'
check "current dashboard present"         '[ -e "$BIN/devbrain-dashboard.html" ]'
check "settings still registers capture"  'grep -q devbrain-capture "$HOME/.claude/settings.json"'

section "stale artifacts cleaned up"
check "pre-rename dashboard removed"  '[ ! -e "$BIN/devbrain-queue-dashboard.html" ]'
check "stray release copy removed"    '[ ! -e "$BIN/devbrain-release.sh" ]'

printf '\n'
[ "$fail" -eq 0 ] && echo "✓ Tier 2 upgrade ALL GREEN ($PRETTY_NAME)" || echo "✗ Tier 2 upgrade FAILURES ($PRETTY_NAME)"
exit "$fail"
CONTAINER
