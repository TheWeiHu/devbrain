#!/usr/bin/env bash
# devbrain — brain hygiene pass (read-only).
#
# Brain pages keep growing and nothing marks which have gone stale. This scans a
# project's brain/*.md pages and reports four hygiene signals, so a human (or a
# follow-up /distill) can decide what to revisit:
#
#   1. STALE      — pages whose last git commit is older than N days (default 30)
#   2. MARKERS    — explicit "needs attention" words left in a page
#                   (TODO/FIXME/XXX/redacted/"out of date"/stale/flag)
#   3. SUPERSEDED — supersession language (superseded/deprecated/obsolete/
#                   "no longer"/"used to") that often flags a now-wrong fact
#   4. DEADLINKS  — [[wikilinks]] whose target page file does not exist
#
# Report only — never edits a page, never blocks. The brain is a projection; this
# just surfaces what to reconcile. Mirrors todo.sh/capture identity resolution.
#
#   brain-hygiene.sh [--days N] [--project <key>]
#
set -euo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
cwd="$PWD"
days=30
proj_override=""
while [ $# -gt 0 ]; do case "$1" in
  --days)    days="$2"; shift 2;;
  --project) proj_override="$2"; shift 2;;
  -h|--help) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0;;
  *) echo "brain-hygiene: unknown arg: $1" >&2; exit 1;;
esac; done

# Same offline resolver as todo.sh / capture so we scan the right project folder.
_pk="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_pk/devbrain-project-key.sh" "$_pk/../hooks/project-key.sh" "$HOME/.claude/hooks/devbrain-project-key.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
if [ -n "$proj_override" ]; then
  project="$proj_override"
else
  project="$(devbrain_project_key "$cwd" "$DATA" 2>/dev/null || true)"; [ -n "$project" ] || project="unknown"
fi
BRAINDIR="$DATA/projects/$project/brain"

[ -d "$BRAINDIR" ] || { echo "brain-hygiene: no brain dir for '$project' ($BRAINDIR)"; exit 0; }

now_epoch="$(date +%s)"
cutoff=$(( days * 86400 ))

# last-commit epoch for a file (0 if untracked / no history) — git survives the
# mtime resets that `git pull` causes, so it's the honest staleness signal.
last_commit_epoch() {
  local rel="$1" e
  e="$(git -C "$DATA" log -1 --format=%ct -- "$rel" 2>/dev/null)"
  printf '%s' "${e:-0}"
}

stale=(); markers=(); superseded=(); deadlinks=()

# Collect the set of existing page basenames for dead-link checking.
declare -a pages=()
for f in "$BRAINDIR"/*.md; do [ -e "$f" ] && pages+=("$(basename "$f" .md)"); done
page_exists() { local t="$1" p; for p in "${pages[@]}"; do [ "$p" = "$t" ] && return 0; done; return 1; }

for f in "$BRAINDIR"/*.md; do
  [ -e "$f" ] || continue
  base="$(basename "$f" .md)"
  rel="projects/$project/brain/$base.md"

  # 1. staleness (git last-commit; fall back to file mtime if untracked)
  ce="$(last_commit_epoch "$rel")"
  if [ "$ce" = "0" ]; then
    ce="$(stat -f %m "$f" 2>/dev/null || stat -c %Y "$f" 2>/dev/null || echo "$now_epoch")"
  fi
  age=$(( now_epoch - ce ))
  if [ "$age" -gt "$cutoff" ]; then
    stale+=("$base ($(( age / 86400 ))d)")
  fi

  # 2. attention markers
  if grep -nEi '(\bTODO\b|\bFIXME\b|\bXXX\b|redact|out[ -]of[ -]date|\bstale\b|\bflag\b)' "$f" >/dev/null 2>&1; then
    while IFS= read -r line; do markers+=("$base:$line"); done < <(grep -nEi '(\bTODO\b|\bFIXME\b|\bXXX\b|redact|out[ -]of[ -]date|\bstale\b|\bflag\b)' "$f" | head -3)
  fi

  # 3. supersession language — candidate now-wrong facts
  if grep -nEi '(supersed|deprecat|obsolete|no longer|used to|was removed|replaced by)' "$f" >/dev/null 2>&1; then
    while IFS= read -r line; do superseded+=("$base:$line"); done < <(grep -nEi '(supersed|deprecat|obsolete|no longer|used to|was removed|replaced by)' "$f" | head -3)
  fi

  # 4. dead wikilinks — [[a/b]] or [[b]] → target page basename = part after last '/'
  while IFS= read -r tgt; do
    [ -n "$tgt" ] || continue
    topic="${tgt##*/}"
    page_exists "$topic" || deadlinks+=("$base -> [[$tgt]]")
  done < <(grep -oE '\[\[[^]]+\]\]' "$f" | sed -E 's/^\[\[//; s/\]\]$//' | sort -u)
done

# ---- report ----
section() { # name, array...
  local name="$1"; shift
  printf '\n## %s (%d)\n' "$name" "$#"
  [ "$#" -eq 0 ] && { printf '  (none)\n'; return; }
  local x; for x in "$@"; do printf '  - %s\n' "$x"; done
}

echo "brain hygiene: $project  ($BRAINDIR)"
echo "pages: ${#pages[@]}  ·  stale threshold: ${days}d"
section "STALE (no commit in ${days}d)" "${stale[@]+"${stale[@]}"}"
section "MARKERS (attention words)"     "${markers[@]+"${markers[@]}"}"
section "SUPERSEDED (maybe now-wrong)"  "${superseded[@]+"${superseded[@]}"}"
section "DEADLINKS (missing target)"    "${deadlinks[@]+"${deadlinks[@]}"}"

total=$(( ${#stale[@]} + ${#markers[@]} + ${#superseded[@]} + ${#deadlinks[@]} ))
printf '\n%d hygiene signal(s). Review the pages above; reconcile or re-distill.\n' "$total"
