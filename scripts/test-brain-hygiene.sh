#!/usr/bin/env bash
# devbrain — brain-hygiene.sh smoke tests. Runs against a throwaway DEVBRAIN_DATA.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; HYG="$HERE/brain-hygiene.sh"
export DEVBRAIN_DATA="$(mktemp -d)"
trap 'rm -rf "$DEVBRAIN_DATA"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

proj="testproj"
BD="$DEVBRAIN_DATA/projects/$proj/brain"; mkdir -p "$BD"

# good page: links to an existing page, no markers
printf '# Alpha\n\nClean page linking [[%s/beta]].\n' "$proj" > "$BD/alpha.md"
# beta exists (so the alpha link resolves) and carries a TODO marker
printf '# Beta\n\nTODO: revisit this later.\n' > "$BD/beta.md"
# page with a dead wikilink + supersession language
printf '# Gamma\n\nThis is no longer accurate and links [[%s/ghost]].\n' "$proj" > "$BD/gamma.md"

run(){ bash "$HYG" --project "$proj" --days 30 2>&1; }
out="$(run)"

check "scans the brain dir"        'grep -q "brain hygiene: testproj" <<<"$out"'
check "counts 3 pages"             'grep -q "pages: 3" <<<"$out"'
check "flags TODO marker (beta)"   'grep -qi "MARKERS" <<<"$out" && grep -q "beta:.*TODO" <<<"$out"'
check "flags supersession (gamma)" 'grep -q "gamma:.*no longer" <<<"$out"'
check "flags dead wikilink (ghost)" 'grep -q "ghost" <<<"$out"'
check "alpha->beta link not dead"  '! grep -q "alpha -> .*beta" <<<"$out"'

# nonexistent project: clean exit, no crash
check "missing project exits 0"    'bash "$HYG" --project nope_$$ >/dev/null 2>&1'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
