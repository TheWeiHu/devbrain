#!/usr/bin/env bash
# devbrain — install.sh must ENABLE Codex's `hooks` feature, not just register hooks.json.
# Codex 0.138+ gates hook execution behind [features].hooks (OFF by default); without
# `codex features enable hooks` the registered hooks never fire and Codex never prompts to
# trust them. Sandboxed install against a throwaway HOME + a MOCK codex; no services.
set -u

REPO="$(cd "$(dirname "$0")/.." && pwd)"
command -v python3 >/dev/null 2>&1 || { echo "skip: python3 not available"; exit 0; }
PYDIR="$(dirname "$(command -v python3)")"

pass=0; fail=0
check() { if eval "$2"; then echo "  ok   — $1"; pass=$((pass+1)); else echo "  FAIL — $1"; fail=$((fail+1)); fi; }

mock_codex() {  # $1=bindir : a codex that records its args and succeeds
  cat > "$1/codex" <<EOF
#!/usr/bin/env bash
echo "\$*" >> "$1/codex-calls.log"
exit 0
EOF
  chmod +x "$1/codex"
}

# --- Case 1: codex on PATH -> installer runs 'codex features enable hooks' ---
SB="$(mktemp -d)"; mkdir -p "$SB/data/projects" "$SB/bin" "$SB/.codex"; git init -q "$SB/data"
mock_codex "$SB/bin"
env -i HOME="$SB" PATH="$SB/bin:/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh \
  DEVBRAIN_DATA="$SB/data" CODEX_HOME="$SB/.codex" \
  bash "$REPO/scripts/install.sh" --only capture,codex </dev/null >"$SB/out.txt" 2>&1
check "installer invokes 'codex features enable hooks'" 'grep -q "features enable hooks" "$SB/bin/codex-calls.log"'
check "prints the enabled-feature confirmation"         'grep -q "enabled Codex .hooks. feature" "$SB/out.txt"'
check "codex hooks.json still registered"               '[ -f "$SB/.codex/hooks.json" ]'
check "final summary claims enabled (enable succeeded)"  'grep -q "hooks feature enabled" "$SB/out.txt"'

# --- Case 2: codex NOT on PATH -> graceful NOTE, install still succeeds ---
SB2="$(mktemp -d)"; mkdir -p "$SB2/data/projects" "$SB2/.codex"; git init -q "$SB2/data"
env -i HOME="$SB2" PATH="/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh \
  DEVBRAIN_DATA="$SB2/data" CODEX_HOME="$SB2/.codex" \
  bash "$REPO/scripts/install.sh" --only capture,codex </dev/null >"$SB2/out.txt" 2>&1
check "codex-absent prints the manual NOTE"  'grep -q "codex not on PATH" "$SB2/out.txt"'
check "install still succeeds (hooks.json)"  '[ -f "$SB2/.codex/hooks.json" ]'
# Codex flagged this: the final summary must NOT claim "enabled" when the enable didn't run.
check "codex-absent summary does NOT claim enabled" '! grep -q "hooks feature enabled" "$SB2/out.txt"'
check "codex-absent summary tells user to enable"   'grep -q "enable hooks yourself" "$SB2/out.txt"'

# --- Case 3: an existing config.toml is backed up before the enable ---
SB3="$(mktemp -d)"; mkdir -p "$SB3/data/projects" "$SB3/bin" "$SB3/.codex"; git init -q "$SB3/data"
printf '[features]\nother = true\n' > "$SB3/.codex/config.toml"
mock_codex "$SB3/bin"
env -i HOME="$SB3" PATH="$SB3/bin:/usr/bin:/bin:$PYDIR" SHELL=/bin/zsh \
  DEVBRAIN_DATA="$SB3/data" CODEX_HOME="$SB3/.codex" \
  bash "$REPO/scripts/install.sh" --only capture,codex </dev/null >/dev/null 2>&1
check "existing config.toml backed up before edit" 'ls "$SB3/.codex/config.toml.bak."* >/dev/null 2>&1'

rm -rf "$SB" "$SB2" "$SB3"
echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
