#!/usr/bin/env bash
# devbrain — nightshift token-cost backfill test. Sources the orchestrator in
# NIGHTSHIFT_LIB mode and checks backfill_token_cost() invokes the importer (pinned to
# DEVBRAIN_DATA) and degrades cleanly when it fails or is absent.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; ORCH="$HERE/nightshift-orchestrate.sh"
command -v bash >/dev/null 2>&1 || { echo "skip: bash not found"; exit 0; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
BIN="$TMP/bin"; mkdir -p "$BIN"; printf '#!/usr/bin/env bash\nexit 0\n' > "$BIN/claude"; chmod +x "$BIN/claude"
export PATH="$BIN:$PATH"
export HOME="$TMP/home"; mkdir -p "$HOME"   # so the installed-importer probe resolves under our control

BASE="$TMP/repo"; mkdir -p "$BASE"
NIGHTSHIFT_LIB=1 . "$ORCH" --repo "$BASE" >/dev/null 2>&1   # the guard returns before boot

pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# A stub importer at the INSTALLED path ($HOME/.claude/hooks/devbrain-import) records the
# exact args it was called with, so we can assert the harvest flags without a data repo.
hookdir="$HOME/.claude/hooks"; mkdir -p "$hookdir"
sentinel="$TMP/import.args"
cat > "$hookdir/devbrain-import" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" > "$sentinel"
exit 0
EOF
chmod +x "$hookdir/devbrain-import"

rm -f "$sentinel"
out="$(DEVBRAIN_DATA="$TMP/custom-data" backfill_token_cost)"
check "invokes the importer"             '[ -f "$sentinel" ]'
check "with --apply --tokens-only"       'grep -q -- "--apply" "$sentinel" && grep -q -- "--tokens-only" "$sentinel"'
check "pins --data to DEVBRAIN_DATA"      'grep -q -- "--data $TMP/custom-data" "$sentinel"'   # not import.py's bare default
check "announces the backfill"           'printf "%s" "$out" | grep -qi "backfill"'

# Idempotent / best-effort: a FAILING importer must not abort teardown (returns clean).
cat > "$hookdir/devbrain-import" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$hookdir/devbrain-import"
check "failing importer does not error out" 'backfill_token_cost >/dev/null 2>&1; [ "$?" -eq 0 ]'

# No importer on disk at all (fresh box mid-setup): still a clean no-op, never a hard fail.
rm -f "$hookdir/devbrain-import"
check "absent importer is a clean no-op"  'SELF_DIR="$TMP/nowhere" backfill_token_cost >/dev/null 2>&1; [ "$?" -eq 0 ]'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
