#!/usr/bin/env bash
# devbrain — nightshift token-cost backfill test. Drives the Go port's
# backfill-tokens verb and checks it invokes the importer (pinned to
# DEVBRAIN_DATA) and degrades cleanly when it fails or is absent.
# DEVBRAIN_IMPORT_CMD pins the importer under test (the installed-copy probe
# the bash version used is the daemon phase's job; the contract here is the
# invocation: --data <DEVBRAIN_DATA> --apply --tokens-only, best-effort).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ROOT="$HERE/.."
BIN="${DEVBRAIN_BIN:-$ROOT/devbrain}"
[ -x "$BIN" ] || { echo "skip: devbrain binary not built (go build -o devbrain ./cmd/devbrain)"; exit 0; }
command -v bash >/dev/null 2>&1 || { echo "skip: bash not found"; exit 0; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
BIND="$TMP/bin"; mkdir -p "$BIND"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIND/claude"; chmod +x "$BIND/claude"
export PATH="$BIND:$PATH"
export HOME="$TMP/home"; mkdir -p "$HOME"   # so nothing resolves to the real installed importer

BASE="$TMP/repo"; mkdir -p "$BASE"
ns(){ "$BIN" nightshift internal "$@" --repo "$BASE"; }

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# A stub importer records the exact args it was called with, so we can assert
# the harvest flags without a data repo.
hookdir="$HOME/.claude/hooks"; mkdir -p "$hookdir"
sentinel="$TMP/import.args"
cat > "$hookdir/devbrain-import" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" > "$sentinel"
exit 0
EOF
chmod +x "$hookdir/devbrain-import"

rm -f "$sentinel"
out="$(DEVBRAIN_DATA="$TMP/custom-data" DEVBRAIN_IMPORT_CMD="$hookdir/devbrain-import" ns backfill-tokens)"
check "invokes the importer"             '[ -f "$sentinel" ]'
check "with --apply --tokens-only"       'grep -q -- "--apply" "$sentinel" && grep -q -- "--tokens-only" "$sentinel"'
check "pins --data to DEVBRAIN_DATA"      'grep -q -- "--data $TMP/custom-data" "$sentinel"'   # not the importer's bare default
check "announces the backfill"           'printf "%s" "$out" | grep -qi "backfill"'

# Idempotent / best-effort: a FAILING importer must not abort teardown (returns clean).
cat > "$hookdir/devbrain-import" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$hookdir/devbrain-import"
check "failing importer does not error out" 'DEVBRAIN_IMPORT_CMD="$hookdir/devbrain-import" ns backfill-tokens >/dev/null 2>&1; [ "$?" -eq 0 ]'

# No importer on disk at all (fresh box mid-setup): still a clean no-op, never a hard fail.
rm -f "$hookdir/devbrain-import"
check "absent importer is a clean no-op"  'DEVBRAIN_IMPORT_CMD="$hookdir/devbrain-import" ns backfill-tokens >/dev/null 2>&1; [ "$?" -eq 0 ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
