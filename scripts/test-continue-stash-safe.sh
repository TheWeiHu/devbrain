#!/usr/bin/env bash
# devbrain — guard: no skill body may run `git stash -u`. The `-u` sweeps untracked
# files into git's stash, which lives in the SHARED common dir (one `refs/stash`
# across all worktrees) and which /continue never pops — so a nightshift worker's
# operational untracked files (.nightshift/, a worktree-local .claude/settings.json)
# get buried and lost. Comment lines that merely *mention* `-u` (to warn
# against it) are fine; only an actual command counts.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; SKILLS="$HERE/../skills"
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

# Flag an executable `git … stash … -u` (the real form is `git -C "$cwd" stash -u`,
# so options sit between `git` and `stash`). Strip comment lines (the `:NN:` prefix
# then optional space then `#`) so a warning that merely *mentions* `-u` is fine.
offenders="$(grep -rn 'stash' "$SKILLS" 2>/dev/null \
  | grep -vE ':[[:space:]]*#' \
  | grep -E 'git .*stash[[:space:]]+(-[A-Za-z]*u|--include-untracked)' || true)"

check "no skill runs 'git stash -u'" '[ -z "$offenders" ]'
[ -n "$offenders" ] && printf '%s\n' "$offenders" | sed 's/^/        | /'

# The /continue skill must still park tracked WIP before branching (the safe form).
check "/continue still parks tracked WIP" 'grep -qE "git -C .* stash( |$)" "$SKILLS/continue/SKILL.md"'

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
