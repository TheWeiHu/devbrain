#!/usr/bin/env bash
# devbrain — brain.sh router tests. Exercises the OFFLINE fallback (gbrain forced off
# PATH) against a throwaway DEVBRAIN_DATA, plus the gbrain passthrough when available.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; BRAIN="$HERE/../hooks/brain.sh"
export DEVBRAIN_DATA="$(mktemp -d)"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# Seed two projects' brain pages on disk (the source of truth gbrain indexes FROM).
mkdir -p "$DEVBRAIN_DATA/projects/owner__alpha/brain" "$DEVBRAIN_DATA/projects/owner__beta/brain"
printf '# Install\nHow to install the widget and configure the daemon.\n' > "$DEVBRAIN_DATA/projects/owner__alpha/brain/install.md"
printf '# Concurrency\nThe daemon uses a lockfile to avoid races.\n'       > "$DEVBRAIN_DATA/projects/owner__alpha/brain/concurrency.md"
printf '# Install\nBeta install notes — totally different widget.\n'       > "$DEVBRAIN_DATA/projects/owner__beta/brain/install.md"

# Force gbrain OFF PATH so we hit the offline fallback regardless of the host.
NOGB="$(printf '%s' "$PATH" | tr ':' '\n' | grep -v '\.bun' | paste -sd: -)"
b(){ PATH="$NOGB" bash "$BRAIN" "$@"; }
command -v gbrain >/dev/null 2>&1 && PATH="$NOGB" command -v gbrain >/dev/null 2>&1 \
  && { echo "skip: could not remove gbrain from PATH for offline test"; exit 0; }

# ── offline search ──────────────────────────────────────────────────────────
check "search finds matching page"   'out="$(b search daemon)"; grep -q "owner__alpha/concurrency" <<<"$out"'
check "search ranks by term coverage" 'out="$(b search "daemon lockfile races")"; head -1 <<<"$out" | grep -q "owner__alpha/concurrency"'
check "search output is gbrain-shaped" 'out="$(b search install)"; head -1 <<<"$out" | grep -qE "^\[[0-9]+\.[0-9]+\] owner__(alpha|beta)/install -- "'
check "search no match -> No results" 'out="$(b search zzzznotapage)"; grep -q "No results." <<<"$out"'
# >20 matching pages must NOT trip a false "No results." (head closes the pipe, sort
# SIGPIPEs, pipefail would otherwise tack "No results." onto real hits).
mkdir -p "$DEVBRAIN_DATA/projects/owner__many/brain"
for i in $(seq 1 30); do printf '# P%s\nthe daemon widget appears here too\n' "$i" > "$DEVBRAIN_DATA/projects/owner__many/brain/page$i.md"; done
check ">20 hits -> no false No results" 'out="$(b search daemon)"; ! grep -q "No results." <<<"$out"'
check ">20 hits -> capped at 20 lines"  'out="$(b search widget)"; [ "$(grep -c "^\[" <<<"$out")" = 20 ]'
check "search spans projects"         'out="$(b search install)"; grep -q "owner__alpha/install" <<<"$out" && grep -q "owner__beta/install" <<<"$out"'

# ── offline get ─────────────────────────────────────────────────────────────
check "get exact slug reads page"     'out="$(b get owner__alpha/concurrency)"; grep -q "lockfile" <<<"$out"'
check "get missing -> page_not_found"  'out="$(b get owner__alpha/nope)"; grep -q "page_not_found" <<<"$out"'
check "fuzzy unique basename resolves" 'out="$(b get concurrency --fuzzy)"; grep -q "lockfile" <<<"$out"'   # only alpha has it
check "fuzzy ambiguous -> Did you mean" 'out="$(b get install --fuzzy)"; grep -q "Did you mean" <<<"$out" && grep -q "owner__beta/install" <<<"$out"'

# ── list / index-op no-ops ──────────────────────────────────────────────────
check "list emits slugs"              'out="$(b list)"; grep -q "owner__alpha/install" <<<"$out"'
check "put is a clean no-op offline"   'b put owner__alpha/install </dev/null; [ $? -eq 0 ]'

# ── passthrough: only when real gbrain is installed ─────────────────────────
if command -v gbrain >/dev/null 2>&1; then
  # With gbrain present the router must exec it (not the fallback). `list` against an
  # empty real brain differs from the fallback's disk listing — just assert it runs.
  # Retry: the real brain is one PGLite DB (single-process), so a concurrent gbrain
  # call — e.g. a nightshift worker mid-turn — can transiently lock it. Retry a few
  # times so that contention doesn't fail this passthrough smoke check.
  check "passthrough: gbrain handles the call" 'ok=1; for _ in 1 2 3 4 5; do bash "$BRAIN" list >/dev/null 2>&1 && { ok=0; break; }; sleep 0.4; done; [ "$ok" = 0 ]'
else
  echo "  skip — gbrain not installed, passthrough path not exercised"
fi

echo "== brain: $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
