#!/usr/bin/env bash
# devbrain — published-tarball clean-room install test.
#
# Every OTHER install test runs scripts/install.sh from the repo CHECKOUT, which
# contains every file — so a runtime reference to a file the npm package does NOT
# ship (e.g. anything dropped by a `!scripts/...` rule in package.json `files`)
# passes the suite yet breaks a real `npx getdevbrain install`. This test closes
# that gap: it builds the ACTUAL tarball with `npm pack`, extracts it, and installs
# FROM THE EXTRACTED PACKAGE into a throwaway $HOME — exactly what npx unpacks and
# runs. A missing runtime file makes install.sh abort under `set -euo pipefail`,
# failing here instead of in a user's terminal.
#
# Hermetic: `--without flusher,git-gate` keeps it off the OS user's crontab/systemd
# and out of any parent git repo; DEVBRAIN_NO_PATH/NO_IMPORT keep it from editing
# rc files or scanning real history; everything writes under a temp HOME. No network
# (npm pack is local). Skips cleanly if npm or python3 is absent.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
command -v npm     >/dev/null 2>&1 || { echo "skip: npm not available";     exit 0; }
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not available"; exit 0; }
command -v node    >/dev/null 2>&1 || { echo "skip: node not available";    exit 0; }
PYDIR="$(dirname "$(command -v python3)")"
NPMDIR="$(dirname "$(command -v npm)")"   # node+npm stay on PATH inside env -i

pass=0; fail=0
check() { if eval "$2"; then echo "  ok   — $1"; pass=$((pass+1)); else echo "  FAIL — $1"; fail=$((fail+1)); fi; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# 1. Build the real npm tarball (no publish, no network, no lifecycle scripts).
if ! npm pack "$REPO" --pack-destination "$TMP" >"$TMP/pack.log" 2>&1; then
  echo "  FAIL — npm pack"; sed 's/^/        | /' "$TMP/pack.log"
  echo "== 0 passed, 1 failed =="; exit 1
fi
TGZ="$(ls "$TMP"/*.tgz 2>/dev/null | head -1)"
check "npm pack produced a tarball" '[ -n "$TGZ" ] && [ -f "$TGZ" ]'

# 2. The tarball must ship every file install.sh copies at runtime — and must NOT
#    ship the deliberately-excluded ones (the `!scripts/...` rules in `files`).
LIST="$(tar tzf "$TGZ" 2>/dev/null | sed 's#^package/##')"
contains() { printf '%s\n' "$LIST" | grep -qxF "$1"; }
for f in setup VERSION \
         hooks/devbrain_lib.py hooks/project-key.sh hooks/capture.sh \
         hooks/capture-response.sh hooks/capture-memory.sh hooks/capture-gbrain.sh \
         hooks/session-start-nudge.sh hooks/brain.sh hooks/turn-marker.sh \
         scripts/install.sh scripts/uninstall.sh scripts/flush.sh \
         scripts/link-preferences.sh scripts/rebuild-brain.sh scripts/todo.sh \
         scripts/import.py scripts/queue.py scripts/dashboard.html scripts/devbrain \
         scripts/nightshift scripts/nightshift-orchestrate.sh scripts/nightshift-status.py \
         scripts/model_pricing.py scripts/com.devbrain.flush.plist \
         prompts/nightshift-drain.txt prompts/nightshift-plan.txt \
         skills/continue/SKILL.md skills/distill/SKILL.md bin/devbrain.js; do
  check "ships $f" "contains $f"
done
check "excludes scripts/test-*"     '! printf "%s\n" "$LIST" | grep -q "^scripts/test-"'
check "excludes scripts/release.sh" '! contains scripts/release.sh'

# 3. Extract and install FROM THE PACKAGE into a throwaway HOME (the npx path).
mkdir -p "$TMP/extracted"
tar xzf "$TGZ" -C "$TMP/extracted"          # -> $TMP/extracted/package/
ROOT="$TMP/extracted/package"
HOMEDIR="$TMP/home"; mkdir -p "$HOMEDIR/data/projects"; git init -q "$HOMEDIR/data"

# env -i proves the package is self-contained: only node/npm/python + core bins on
# PATH, no inherited env, no network. Full default install minus the two components
# with machine-wide side effects, so it still exercises capture/response/nudge/skills
# /nightshift file copies — i.e. nearly every shipped runtime file.
env -i HOME="$HOMEDIR" PATH="$NPMDIR:$PYDIR:/usr/bin:/bin" SHELL=/bin/bash \
    DEVBRAIN_DATA="$HOMEDIR/data" DEVBRAIN_NO_PATH=1 DEVBRAIN_NO_IMPORT=1 \
    bash "$ROOT/scripts/install.sh" --without flusher,git-gate </dev/null \
    >"$TMP/install.log" 2>&1
rc=$?
check "install from packed tarball exits 0" "[ $rc -eq 0 ]"
[ "$rc" -eq 0 ] || sed 's/^/        | /' "$TMP/install.log"

CL="$HOMEDIR/.claude"; BIN="$CL/hooks"
check "capture hook installed + executable" '[ -x "$BIN/devbrain-capture.sh" ]'
check "unified devbrain CLI installed"      '[ -x "$BIN/devbrain" ]'
check "version pinned for installed CLI"    '[ -f "$BIN/devbrain.version" ]'
check "settings.json registers capture"     'grep -q devbrain-capture "$CL/settings.json"'
check "skills installed (/continue)"        '[ -f "$CL/skills/continue/SKILL.md" ]'
check "nightshift toolset installed"        '[ -x "$CL/nightshift/nightshift" ]'
check "nightshift worker prompts shipped"   'ls "$CL/nightshift/prompts/"*.txt >/dev/null 2>&1'

# 4. The npm front door itself runs straight from the package (pre-install help).
check "bin/devbrain.js help runs from pkg"  'node "$ROOT/bin/devbrain.js" help >/dev/null 2>&1'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
