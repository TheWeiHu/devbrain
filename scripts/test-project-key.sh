#!/usr/bin/env bash
# devbrain — project-key.sh tests. Pure-offline; builds throwaway git repos with
# fake remotes (git remote add never touches the network) under a temp DEVBRAIN_DATA.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/../hooks/project-key.sh"

DATA="$(mktemp -d)"; REPOS="$(mktemp -d)"
trap 'rm -rf "$DATA" "$REPOS"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# owner<TAB>repo<TAB>host helper for the parse tests.
field(){ devbrain_parse_remote "$1" | cut -f"$2"; }
mkrepo(){ local d="$REPOS/$1"; mkdir -p "$d"; git -C "$d" init -q >/dev/null 2>&1; \
          [ -n "${2:-}" ] && git -C "$d" remote add origin "$2"; printf '%s' "$d"; }
idremote(){ sed -n 's/^remote: //p' "$DATA/projects/$1/.identity" 2>/dev/null; }

echo "== parse_remote =="
check "scp owner"    '[ "$(field git@github.com:orgA/api.git 1)" = "orgA" ]'
check "scp repo"     '[ "$(field git@github.com:orgA/api.git 2)" = "api" ]'
check "scp host"     '[ "$(field git@github.com:orgA/api.git 3)" = "github.com" ]'
check "https owner"  '[ "$(field https://github.com/orgA/api.git 1)" = "orgA" ]'
check "https repo"   '[ "$(field https://github.com/orgA/api.git 2)" = "api" ]'
check "ssh:// owner"  '[ "$(field ssh://git@github.com/orgA/api 1)" = "orgA" ]'
check "bare owner"   '[ "$(field github.com/orgA/api 1)" = "orgA" ]'
check "nested repo"  '[ "$(field https://gitlab.com/group/sub/proj.git 2)" = "proj" ]'

echo "== normalize_remote (scp vs https are the same repo) =="
check "scp == https" '[ "$(devbrain_normalize_remote git@github.com:orgA/api.git)" = "$(devbrain_normalize_remote https://github.com/orgA/api.git)" ]'
check "case-folded"  '[ "$(devbrain_normalize_remote https://GitHub.com/OrgA/Api.git)" = "github.com/orga/api" ]'

echo "== project_key resolution =="
r="$(mkrepo a git@github.com:orgA/api.git)"
check "new project -> <owner>__<repo>" '[ "$(devbrain_project_key "$r" "$DATA")" = "orga__api" ]'
check "writes .identity"               '[ "$(idremote orga__api)" = "git@github.com:orgA/api.git" ]'
check "second call reuses canon"       '[ "$(devbrain_project_key "$r" "$DATA")" = "orga__api" ]'

# Legacy basename folder with no .identity is adopted in place + backfilled.
mkdir -p "$DATA/projects/legacyfoo"
r2="$(mkrepo b https://github.com/someorg/legacyfoo.git)"
check "legacy folder adopted in place" '[ "$(devbrain_project_key "$r2" "$DATA")" = "legacyfoo" ]'
check "legacy .identity backfilled"    '[ "$(idremote legacyfoo)" = "https://github.com/someorg/legacyfoo.git" ]'

# Collision: an existing basename folder owned by a DIFFERENT remote.
mkdir -p "$DATA/projects/api"
printf 'remote: git@github.com:orgA/api.git\n' > "$DATA/projects/api/.identity"
r3="$(mkrepo c https://github.com/orgB/api.git)"
check "collision -> disambiguated key" '[ "$(devbrain_project_key "$r3" "$DATA")" = "orgb__api" ]'

# Same repo, different URL form -> reuse the legacy folder (no canonical folder
# exists for it, and the recorded remote matches across scp/https forms).
mkdir -p "$DATA/projects/widget"
printf 'remote: git@github.com:orgZ/widget.git\n' > "$DATA/projects/widget/.identity"
r4="$(mkrepo d https://github.com/orgZ/widget.git)"   # https form of the scp remote above
check "same repo other URL reuses"     '[ "$(devbrain_project_key "$r4" "$DATA")" = "widget" ]'

# No remote -> cwd basename.
r5="$(mkrepo norem)"
check "no remote -> basename"          '[ "$(devbrain_project_key "$r5" "$DATA")" = "norem" ]'

# Explicit override wins.
check "DEVBRAIN_PROJECT override"      'DEVBRAIN_PROJECT=Forced_X devbrain_project_key "$r" "$DATA" | grep -qx forced_x'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
