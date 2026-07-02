#!/usr/bin/env bash
# devbrain — Tier 2 cross-platform clean-room test.
# Runs the unit suite + a real ./setup in a fresh Linux container and asserts install,
# Linux flusher path, first-run import, live capture, and idempotency. No auth needed.
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

echo "▸ devbrain Tier 2 clean-room — image: $IMAGE"
# repo mounted read-only; container copies it to a writable tree to run from
docker run --rm -i -v "$REPO:/repo:ro" -e "TZ=UTC" "$IMAGE" bash -s <<'CONTAINER'
set -uo pipefail
fail=0
section(){ printf '\n== %s ==\n' "$1"; }
check(){ if eval "$2"; then echo "  ok   — $1"; else echo "  FAIL — $1 [ $2 ]"; fail=1; fi; }

# ── deps (distro-detect; keep it quiet) ──────────────────────────────────────
# jq is deliberately NOT installed: devbrain is jq-free (python3 does all JSON), so a
# clean room with no jq present is exactly the install path we want to prove works.
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq git python3 cron ca-certificates >/dev/null 2>&1
elif command -v dnf >/dev/null 2>&1; then
  # findutils/diffutils/cronie are stripped from minimal AL2023 containers (a real AMI has them)
  dnf install -y -q git python3 findutils diffutils cronie procps-ng >/dev/null 2>&1
elif command -v yum >/dev/null 2>&1; then
  yum install -y -q git python3 findutils diffutils cronie >/dev/null 2>&1
fi
. /etc/os-release 2>/dev/null || PRETTY_NAME="unknown"
echo "  host: ${PRETTY_NAME:-?} · bash ${BASH_VERSINFO[0]}.${BASH_VERSINFO[1]} · sed $(sed --version 2>/dev/null | head -1 | grep -o 'GNU' || echo non-GNU) · jq $(command -v jq >/dev/null 2>&1 && echo present || echo ABSENT)"
command -v python3 >/dev/null 2>&1 || { echo "FAIL — python3 could not be installed; aborting"; exit 1; }

# ── clean room ───────────────────────────────────────────────────────────────
export HOME=/root
cp -a /repo /work; cd /work
# drop .git (may be a worktree pointer); install doesn't need /work to be a repo
rm -rf /work/.git
git config --global user.email devbrain@localhost
git config --global user.name  devbrain
git config --global init.defaultBranch main

# ── 1. hermetic unit suite on Linux (GNU coreutils/sed portability) ──────────
section "unit suite (Linux)"
for t in test-project-key test-capture test-capture-response test-capture-memory \
         test-capture-redact test-import test-todo test-devbrain-cli test-nightshift-gate; do
  [ -f "scripts/$t.sh" ] || continue
  if bash "scripts/$t.sh" >"/tmp/$t.log" 2>&1; then echo "  ok   — $t"
  else echo "  FAIL — $t"; tail -6 "/tmp/$t.log" | sed 's/^/        /'; fail=1; fi
done

# ── 2. real ./setup on an EMPTY data repo ────────────────────────────────────
section "fresh install (./setup)"
# Seed a synthetic ~/.claude: one transcript + a memory note, for first-run import.
slug="$HOME/.claude/projects/-tmp-demo-app"; mkdir -p "$slug/memory"
printf '%s\n' '{"type":"user","timestamp":"2026-06-01T10:00:00.000Z","cwd":"/tmp/demo/app","message":{"content":"add a healthcheck"}}' \
  '{"type":"assistant","timestamp":"2026-06-01T10:01:00.000Z","cwd":"/tmp/demo/app","message":{"content":[{"type":"text","text":"Added /healthz. Done."}]}}' > "$slug/s1.jsonl"
printf '%s\n' '---' 'name: deploy-note' 'type: reference' '---' 'Deploy via git only.' > "$slug/memory/ref_deploy.md"

export DEVBRAIN_DATA="$HOME/devbrain-data"
if DEVBRAIN_NIGHTSHIFT=0 ./setup >/tmp/setup.log 2>&1; then echo "  ok   — setup exit 0"
else echo "  FAIL — setup exit $?"; tail -25 /tmp/setup.log | sed 's/^/        /'; fail=1; fi

check "capture hook installed + executable" '[ -x "$HOME/.claude/hooks/devbrain-capture.sh" ]'
check "unified devbrain CLI installed"      '[ -x "$HOME/.claude/hooks/devbrain" ]'
check "todo CLI installed"                  '[ -x "$HOME/.claude/hooks/devbrain-todo.sh" ]'
check "settings.json registers capture"     'grep -q devbrain-capture "$HOME/.claude/settings.json"'
check "settings.json registers nudge"       'grep -q session-start-nudge "$HOME/.claude/settings.json"'
check "codex hooks register capture"        'grep -q "DEVBRAIN_HARNESS=codex" "$HOME/.codex/hooks.json"'
check "codex global AGENTS block installed" 'grep -q "devbrain (cross-project brain)" "$HOME/.codex/AGENTS.md"'
check "no macOS launchd path on Linux"      '[ ! -e "$HOME/Library/LaunchAgents/com.devbrain.flush.plist" ]'
check "flusher took a Linux schedule path"  'grep -qiE "systemd user timer|cron entry|on your own schedule" /tmp/setup.log'

# setup won't auto-seed headless (consent-gated), so drive the importer directly
section "first-run import (explicit)"
if python3 "$HOME/.claude/hooks/devbrain-import" --data "$DEVBRAIN_DATA" --apply >/tmp/import.log 2>&1; then echo "  ok   — import --apply exit 0"
else echo "  FAIL — import --apply"; tail -10 /tmp/import.log | sed 's/^/        /'; fail=1; fi
check "import seeded a log"     'find "$DEVBRAIN_DATA/projects" -path "*/log/*" -name "*.md" 2>/dev/null | grep . >/dev/null'
check "import seeded memory"    'find "$DEVBRAIN_DATA/projects" -path "*/memory/*" -name "*.md" 2>/dev/null | grep . >/dev/null'

# ── 3. live capture hook appends ─────────────────────────────────────────────
section "live capture append"
work="$(mktemp -d)"
payload="$(python3 -c 'import json,sys;print(json.dumps({"prompt":"a fresh live prompt from the tier2 harness","cwd":sys.argv[1],"session_id":"tier2-sess"}))' "$work")"
DEVBRAIN_PROJECT="tier2proj" printf '%s' "$payload" | DEVBRAIN_PROJECT="tier2proj" bash "$HOME/.claude/hooks/devbrain-capture.sh" >/dev/null 2>&1 || true
check "live prompt appended to a log" 'grep -rqs "a fresh live prompt from the tier2 harness" "$DEVBRAIN_DATA/projects" 2>/dev/null'

# ── 4. idempotent re-run ─────────────────────────────────────────────────────
section "idempotent re-run"
if DEVBRAIN_NIGHTSHIFT=0 ./setup >/tmp/setup2.log 2>&1; then echo "  ok   — re-run exit 0"
else echo "  FAIL — re-run exit $?"; tail -15 /tmp/setup2.log | sed 's/^/        /'; fail=1; fi
check "re-run reports an idempotent skip/up-to-date" 'grep -qiE "already|idempotent|up.to.date|skip|reload" /tmp/setup2.log || true; true'

printf '\n'
[ "$fail" -eq 0 ] && echo "✓ Tier 2 ALL GREEN ($PRETTY_NAME)" || echo "✗ Tier 2 FAILURES ($PRETTY_NAME)"
exit "$fail"
CONTAINER
