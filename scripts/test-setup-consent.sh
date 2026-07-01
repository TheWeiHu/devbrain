#!/usr/bin/env bash
# devbrain — setup consent/dry-run tests. Exercises the public installer entrypoint
# in a throwaway HOME; no real global installs or services.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
command -v git >/dev/null 2>&1 || { echo "skip: git not available"; exit 0; }
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not available"; exit 0; }

pass=0; fail=0
check() { if eval "$2"; then echo "  ok   — $1"; pass=$((pass+1)); else echo "  FAIL — $1"; fail=$((fail+1)); fi; }

BASE_PATH="/usr/bin:/bin"

# 1. setup --dry-run previews but creates nothing.
SB="$(mktemp -d)"
OUT="$(env -i HOME="$SB" PATH="$BASE_PATH" SHELL=/bin/zsh DEVBRAIN_DATA="$SB/data" \
  bash "$REPO/setup" --dry-run --only capture --no-open 2>&1)"
check "setup dry-run announces no-write mode" 'printf "%s" "$OUT" | grep -q "Dry run — no files will be written"'
check "setup dry-run delegates scoped install preview" 'printf "%s" "$OUT" | grep -q "components  : capture "'
check "setup dry-run does not create data repo" '[ ! -e "$SB/data" ]'
check "setup dry-run does not create Claude/Codex homes" '[ ! -e "$SB/.claude" ] && [ ! -e "$SB/.codex" ]'

# 2. Non-interactive --yes does NOT authorize a missing global gbrain install.
SB2="$(mktemp -d)"
FAKE="$SB2/fakebin"; mkdir -p "$FAKE"
cat > "$FAKE/bun" <<'SH'
#!/usr/bin/env bash
echo "bun was called: $*" >> "$HOME/bun-called"
exit 42
SH
chmod +x "$FAKE/bun"
OUT2="$(env -i HOME="$SB2" PATH="$FAKE:$BASE_PATH" SHELL=/bin/zsh DEVBRAIN_DATA="$SB2/data" \
  bash "$REPO/setup" --yes --no-open --only capture 2>&1)"
check "--yes without --install-deps skips missing gbrain" 'printf "%s" "$OUT2" | grep -q "skipping gbrain"'
check "--yes without --install-deps never calls bun" '[ ! -e "$SB2/bun-called" ]'
check "real setup still installs requested component" '[ -x "$SB2/.claude/hooks/devbrain-capture.sh" ]'

# 3. --install-deps is visible in dry-run and uses the pinned package identity.
SB3="$(mktemp -d)"
OUT3="$(env -i HOME="$SB3" PATH="$BASE_PATH" SHELL=/bin/zsh DEVBRAIN_DATA="$SB3/data" \
  bash "$REPO/setup" --dry-run --install-deps --only capture --no-open 2>&1)"
check "--install-deps dry-run shows pinned gbrain package" 'printf "%s" "$OUT3" | grep -q "bun add -g gbrain@1.3.1"'
check "--install-deps dry-run still writes nothing" '[ ! -e "$SB3/data" ] && [ ! -e "$SB3/.claude" ]'

rm -rf "$SB" "$SB2" "$SB3"
echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
