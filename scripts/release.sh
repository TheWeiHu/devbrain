#!/usr/bin/env bash
# devbrain — release cutter (MAINTAINER tool for the devbrain PROJECT itself, run
# from a repo checkout — deliberately NOT installed as a `devbrain` subcommand).
# Bumps VERSION, rolls the CHANGELOG [Unreleased] notes into a dated section,
# commits, and creates the annotated vX.Y.Z tag.
#
#   ./scripts/release.sh patch|minor|major   bump from the current VERSION
#   ./scripts/release.sh X.Y.Z                set an explicit version
#   ./scripts/release.sh <ver> --push         push commit + tag AND publish a GitHub Release
#   ./scripts/release.sh <ver> --push --no-release   push, but skip the GitHub Release
#   ./scripts/release.sh <ver> --dry-run      show what would change; touch nothing
#
# Run on a clean `main` checkout. Pre-1.0 rule: minor = new capability, patch =
# fixes/docs. The tag points at the release commit; --push also runs `gh release
# create` from the new CHANGELOG section (skips if gh is absent/unauthenticated).
set -euo pipefail

usage() { sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; }

# Print the body of the [<ver>] section of CHANGELOG.md (between its heading and the
# next "## "). Used as the GitHub Release notes.
changelog_section() {
  awk -v ver="$1" 'BEGIN{ gsub(/\./,"\\.",ver); pat="^## \\[" ver "\\]" }
    $0 ~ pat {f=1; next} f && /^## / {exit} f {print}' CHANGELOG.md
}

# Publish a GitHub Release for the new tag, best-effort: needs `gh`, auth, and a
# GitHub remote. Skips gracefully (like every other optional-tool path) so a
# release on a box without gh still succeeds — the tag is the real artifact.
publish_release() {
  command -v gh >/dev/null 2>&1 || { echo "release: 'gh' not found — GitHub Release skipped (create later: gh release create v$new)"; return 0; }
  if changelog_section "$new" | gh release create "v$new" --title "devbrain v$new" --notes-file - --verify-tag >/dev/null 2>&1; then
    echo "release: published GitHub Release v$new"
  else
    echo "release: GitHub Release skipped (gh not authenticated / no GitHub remote) — create later:  gh release create v$new --notes-file -"
  fi
}

PUSH=0; DRY=0; NO_REL=0; BUMP=""
while [ $# -gt 0 ]; do case "$1" in
  --push)        PUSH=1; shift;;
  --no-release)  NO_REL=1; shift;;
  --dry-run|-n)  DRY=1; shift;;
  -h|--help)     usage; exit 0;;
  major|minor|patch)        BUMP="$1"; shift;;
  [0-9]*.[0-9]*.[0-9]*)     BUMP="$1"; shift;;
  *) echo "release: bad arg '$1'" >&2; usage; exit 1;;
esac; done
[ -n "$BUMP" ] || { echo "release: need major|minor|patch or X.Y.Z" >&2; usage; exit 1; }

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "release: not in a git repo" >&2; exit 1; }
cd "$ROOT"
[ -f VERSION ] && [ -f CHANGELOG.md ] || {
  echo "release: VERSION + CHANGELOG.md not found at repo root ($ROOT) — run inside the devbrain repo" >&2; exit 1; }

cur="$(tr -d '[:space:]' < VERSION)"
case "$cur" in [0-9]*.[0-9]*.[0-9]*) ;; *) echo "release: VERSION '$cur' is not semver" >&2; exit 1;; esac
IFS=. read -r MA MI PA <<EOF
$cur
EOF
case "$BUMP" in
  major) new="$((MA+1)).0.0";;
  minor) new="$MA.$((MI+1)).0";;
  patch) new="$MA.$MI.$((PA+1))";;
  *)     new="$BUMP";;
esac
case "$new" in [0-9]*.[0-9]*.[0-9]*) ;; *) echo "release: computed version '$new' is not semver" >&2; exit 1;; esac
[ "$new" != "$cur" ] || { echo "release: new version equals current ($cur)" >&2; exit 1; }

# Guards: clean tree (so the release commit is just VERSION+CHANGELOG) + tag is free.
if [ "$DRY" = 0 ] && [ -n "$(git status --porcelain)" ]; then
  echo "release: working tree not clean — commit or stash first" >&2; exit 1; fi
if git rev-parse -q --verify "refs/tags/v$new" >/dev/null 2>&1; then
  echo "release: tag v$new already exists" >&2; exit 1; fi

today="$(date +%F)"
echo "release: $cur -> $new   (tag v$new · $today)$([ "$DRY" = 1 ] && echo '   [dry-run]')"

# 1. Roll the CHANGELOG: move the [Unreleased] body into a new [new] — today section,
#    leaving [Unreleased] reset to the placeholder.
tmp="$(mktemp)"
awk -v new="$new" -v today="$today" '
  function flush(   s,e,i) {                       # emit captured body, trimmed of edge blanks
    s=1; while (s<=n && buf[s] ~ /^[[:space:]]*$/) s++
    e=n; while (e>=s && buf[e] ~ /^[[:space:]]*$/) e--
    for (i=s;i<=e;i++) print buf[i]
    return (e>=s)                                  # 1 if any content emitted
  }
  /^## \[Unreleased\]/ && cap==0 {                 # reset Unreleased, open the new dated section
    print "## [Unreleased]"; print ""; print "_Nothing yet._"; print "";
    print "## [" new "] — " today; print "";
    cap=1; n=0; next
  }
  cap==1 && /^## / { if (flush()) print ""; cap=0; print; next }   # next section -> close out
  cap==1 {
    if ($0 ~ /^_Nothing yet\._[[:space:]]*$/) next               # drop the placeholder
    buf[++n]=$0; next
  }
  { print }
  END { if (cap==1) flush() }                      # Unreleased was the final section
' CHANGELOG.md > "$tmp"

# 2. Update the link-ref footer: point [Unreleased] at v<new>...HEAD and add a [new] tag ref.
tmp2="$(mktemp)"
awk -v new="$new" '
  /^\[Unreleased\]: / {
    line=$0; sub(/compare\/v[0-9.]+\.\.\.HEAD/, "compare/v" new "...HEAD", line); print line
    base=$0; sub(/^\[Unreleased\]: /, "", base); sub(/\/compare\/.*/, "", base)
    print "[" new "]: " base "/releases/tag/v" new
    next
  }
  { print }
' "$tmp" > "$tmp2"
rm -f "$tmp"

if [ "$DRY" = 1 ]; then
  echo "--- VERSION ---"; echo "$new"
  [ -f package.json ] && echo "--- package.json version ---"; [ -f package.json ] && echo "$new"
  echo "--- CHANGELOG.md (diff) ---"; diff -u CHANGELOG.md "$tmp2" || true
  rm -f "$tmp2"
  echo "release: dry-run — nothing written."
  exit 0
fi

cat "$tmp2" > CHANGELOG.md; rm -f "$tmp2"
printf '%s\n' "$new" > VERSION

# Keep the npm package version in lockstep with VERSION (devbrain ships on npm as
# `getdevbrain`). Guarded by [ -f package.json ] so the maintainer test — a repo with
# only VERSION + CHANGELOG — still passes. Only the top-level "version" line is touched.
if [ -f package.json ]; then
  tmpj="$(mktemp)"
  awk -v v="$new" '!d && /"version":[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"/ {
        sub(/"version":[[:space:]]*"[0-9]+\.[0-9]+\.[0-9]+"/, "\"version\": \"" v "\""); d=1 } { print }' \
      package.json > "$tmpj" && mv "$tmpj" package.json
fi

git add VERSION CHANGELOG.md
[ -f package.json ] && git add package.json
git commit -q -m "Release v$new"
git tag -a "v$new" -m "devbrain v$new"
echo "release: committed + tagged v$new"

if [ "$PUSH" = 1 ]; then
  br="$(git rev-parse --abbrev-ref HEAD)"
  git push origin "$br"
  git push origin "v$new"
  echo "release: pushed $br + v$new"
  [ "$NO_REL" = 1 ] && echo "release: --no-release → GitHub Release skipped" || publish_release
else
  echo "release: not pushed. To publish:  git push origin HEAD && git push origin v$new"
fi
