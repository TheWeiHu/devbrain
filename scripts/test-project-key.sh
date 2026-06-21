#!/usr/bin/env bash
# devbrain — project-key.sh tests. Pure-offline; builds throwaway git repos with
# fake remotes (git remote add never touches the network).
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "$HERE/../hooks/project-key.sh"

REPOS="$(mktemp -d)"; trap 'rm -rf "$REPOS"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }
# key <name> [<remote-url>] -> resolved key for a fresh repo with that remote.
key(){ local d="$REPOS/$1"; mkdir -p "$d"; git -C "$d" init -q >/dev/null 2>&1; \
       [ -n "${2:-}" ] && git -C "$d" remote add origin "$2"; devbrain_project_key "$d"; }

check "scp    -> owner__repo"  '[ "$(key a git@github.com:orgA/api.git)" = "orga__api" ]'
check "https  -> owner__repo"  '[ "$(key b https://github.com/orgA/api.git)" = "orga__api" ]'
check "ssh:// -> owner__repo"  '[ "$(key c ssh://git@github.com/orgA/api)" = "orga__api" ]'
check "bare   -> owner__repo"  '[ "$(key d github.com/orgA/api)" = "orga__api" ]'
check "nested group last two"  '[ "$(key e https://gitlab.com/group/sub/proj.git)" = "sub__proj" ]'
check "same basename, different owners -> distinct keys" \
  '[ "$(key f git@github.com:orgA/api.git)" != "$(key g git@github.com:orgB/api.git)" ]'
check "no remote -> miscellaneous"        '[ "$(key norem)" = "miscellaneous" ]'
check "remote without owner -> miscellaneous" '[ "$(key noowner myrepo)" = "miscellaneous" ]'
# Local-filesystem origins are not hosted identities: a worktree whose origin is the
# workspace path must NOT mint a <repo>__<workspace> folder.
check "absolute path -> miscellaneous"    '[ "$(key localwt /Users/x/code/devbrain/managua-v1)" = "miscellaneous" ]'
check "file:// path -> miscellaneous"     '[ "$(key fileurl file:///srv/repos/devbrain/managua-v1)" = "miscellaneous" ]'
check "relative path -> miscellaneous"    '[ "$(key relpath ../devbrain/managua-v1)" = "miscellaneous" ]'
check "DEVBRAIN_PROJECT override"  'DEVBRAIN_PROJECT=Forced_X devbrain_project_key "$REPOS" | grep -qx forced_x'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
