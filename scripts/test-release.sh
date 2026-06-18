#!/usr/bin/env bash
# devbrain — release.sh tests. Runs in a throwaway git repo with a VERSION +
# CHANGELOG fixture so it never touches the real repo or creates real tags.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; REL="$HERE/release.sh"
T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
pass=0; fail=0
check(){ if eval "$2"; then pass=$((pass+1)); echo "  ok   — $1"; else fail=$((fail+1)); echo "  FAIL — $1 [ $2 ]"; fi; }

git -C "$T" init -q
git -C "$T" config user.email t@t; git -C "$T" config user.name t
printf '0.1.0\n' > "$T/VERSION"
cat > "$T/CHANGELOG.md" <<'EOF'
# Changelog

## [Unreleased]

### Added
- a shiny new thing

## [0.1.0] — 2026-06-18

### Added
- the baseline

[Unreleased]: https://github.com/TheWeiHu/devbrain/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.1.0
EOF
git -C "$T" add -A; git -C "$T" commit -qm init
r(){ ( cd "$T" && bash "$REL" "$@" ); }

# dry-run changes nothing
r minor -n >/dev/null 2>&1
check "dry-run leaves VERSION"      '[ "$(cat "$T/VERSION")" = "0.1.0" ]'
check "dry-run makes no tag"        '[ -z "$(git -C "$T" tag -l)" ]'

# minor bump 0.1.0 -> 0.2.0
out="$(r minor 2>&1)"
check "minor -> 0.2.0 in VERSION"   '[ "$(cat "$T/VERSION")" = "0.2.0" ]'
check "tag v0.2.0 created"          'git -C "$T" rev-parse -q --verify refs/tags/v0.2.0 >/dev/null'
check "release commit made"         '[ "$(git -C "$T" log -1 --pretty=%s)" = "Release v0.2.0" ]'
check "CHANGELOG has [0.2.0]"        'grep -q "^## \[0.2.0\] — " "$T/CHANGELOG.md"'
check "moved note into 0.2.0"        "awk '/## \[0.2.0\]/{f=1} f&&/shiny new thing/{print;exit}' \"\$T/CHANGELOG.md\" | grep -q shiny"
check "Unreleased reset to empty"    'awk "/## \[Unreleased\]/{f=1;next} f&&/## \[/{exit} f&&/shiny/{bad=1} END{exit bad}" "$T/CHANGELOG.md"'
check "Unreleased ref -> v0.2.0"     'grep -q "compare/v0.2.0\.\.\.HEAD" "$T/CHANGELOG.md"'
check "added [0.2.0] tag ref"        'grep -q "^\[0.2.0\]: .*releases/tag/v0.2.0" "$T/CHANGELOG.md"'
check "did not push (no remote ok)"  'grep -q "not pushed" <<<"$out"'

# guards
check "re-release same tag fails"    'r 0.2.0 >/dev/null 2>&1; [ "$?" -ne 0 ]'
printf 'dirty\n' > "$T/dirty.txt"
check "dirty tree blocks release"    'r patch >/dev/null 2>&1; [ "$?" -ne 0 ]'
rm -f "$T/dirty.txt"
check "bad version string rejected"  'r 1.2 >/dev/null 2>&1; [ "$?" -ne 0 ]'
check "no arg rejected"              'r >/dev/null 2>&1; [ "$?" -ne 0 ]'

# explicit version + patch math
r 1.0.0 >/dev/null 2>&1
check "explicit 1.0.0 set"           '[ "$(cat "$T/VERSION")" = "1.0.0" ]'
r patch >/dev/null 2>&1
check "patch -> 1.0.1"               '[ "$(cat "$T/VERSION")" = "1.0.1" ]'
r major >/dev/null 2>&1
check "major -> 2.0.0"               '[ "$(cat "$T/VERSION")" = "2.0.0" ]'

# --push publishes a GitHub Release — stub `gh` (capture argv + stdin notes) and a
# bare remote so the real push + the `gh release create` path both exercise.
G="$(mktemp -d)"; BARE="$G/remote.git"; WORK="$G/work"; GBIN="$G/bin"; mkdir -p "$GBIN"
git init -q --bare "$BARE"
git init -q "$WORK"; git -C "$WORK" config user.email t@t; git -C "$WORK" config user.name t
git -C "$WORK" remote add origin "$BARE"
printf '0.5.0\n' > "$WORK/VERSION"
printf '# Changelog\n\n## [Unreleased]\n\n- new shiny\n\n## [0.5.0] — 2026-01-01\n\n- base\n\n[Unreleased]: https://github.com/TheWeiHu/devbrain/compare/v0.5.0...HEAD\n[0.5.0]: https://github.com/TheWeiHu/devbrain/releases/tag/v0.5.0\n' > "$WORK/CHANGELOG.md"
git -C "$WORK" add -A; git -C "$WORK" commit -qm init; git -C "$WORK" branch -M main; git -C "$WORK" push -q -u origin main
printf '#!/usr/bin/env bash\necho "$@" > "%s/gh-args.txt"\ncat > "%s/gh-notes.txt"\nexit 0\n' "$G" "$G" > "$GBIN/gh"; chmod +x "$GBIN/gh"
( cd "$WORK" && PATH="$GBIN:$PATH" bash "$REL" minor --push ) >/dev/null 2>&1
check "--push tags the remote"       'git -C "$BARE" tag -l | grep -q v0.6.0'
check "--push invokes gh release"    'grep -q "release create v0.6.0" "$G/gh-args.txt"'
check "--push pipes changelog notes" 'grep -q "new shiny" "$G/gh-notes.txt"'
rm -f "$G/gh-args.txt"
( cd "$WORK" && PATH="$GBIN:$PATH" bash "$REL" patch --push --no-release ) >/dev/null 2>&1
check "--no-release skips gh"        '[ ! -f "$G/gh-args.txt" ]'
rm -rf "$G"

echo "== $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
