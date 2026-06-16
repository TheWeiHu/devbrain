#!/usr/bin/env bash
# devbrain — TODO queue. One markdown file per task (conflict-free sync like the
# log); priority-ranked; `claim` marks a task taken so a parallel run skips it.
# Tasks are created by /distill and worked by /continue — this CLI is the substrate.
#
#   $DEVBRAIN_DATA/projects/<project>/todo/<id>.md
#
#   todo add "<title>" [-p N] [-b "body"]   create (prints id); priority 0-100, default 0
#   todo list                               open tasks, highest priority first
#   todo next                               id of the top open task (empty if none)
#   todo show <id>                          print a task file
#   todo claim <id>                         mark open -> taken (exit 2 if not open)
#   todo pr <id> <url>                       record the PR url; stays taken (in-review)
#   todo done <id>                          close it (only after the PR has MERGED)
#   todo release <id>                       taken -> open (un-claim)
#
# Identity (which project's queue) = the working repo's git remote, like capture.
set -euo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
cwd="$PWD"
remote="$(git -C "$cwd" remote get-url origin 2>/dev/null || true)"
if [ -n "$remote" ]; then project="$(basename "${remote%.git}")"; else project="$(basename "$cwd")"; fi
sanitize() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'; }
project="$(sanitize "$project")"; [ -n "$project" ] || project="unknown"
[ -n "${DEVBRAIN_PROJECT:-}" ] && project="$(sanitize "$DEVBRAIN_PROJECT")"
TODODIR="$DATA/projects/$project/todo"

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
die() { echo "todo: $*" >&2; exit 1; }
get_field() { awk -v k="$2" '/^---[[:space:]]*$/{n++; if(n==2)exit; next}
  n==1 && $0 ~ "^"k":" { sub("^"k":[[:space:]]*",""); print; exit }' "$1"; }
set_field() { local f="$1" k="$2" v="$3" tmp; tmp="$(mktemp)"
  awk -v k="$k" -v v="$v" '/^---[[:space:]]*$/{n++; print; next}
    n==1 && $0 ~ "^"k":" && !d { print k": "v; d=1; next } { print }' "$f" > "$tmp" && mv "$tmp" "$f"; }
title_of() { awk '/^---[[:space:]]*$/{n++; next} n>=2 && /^# /{sub(/^# /,""); print; exit}' "$1"; }

# "priority<TAB>created<TAB>id<TAB>title" for open tasks, sorted priority desc / FIFO.
open_rows() {
  [ -d "$TODODIR" ] || return 0
  local f st
  for f in "$TODODIR"/*.md; do
    [ -e "$f" ] || continue
    st="$(get_field "$f" status)"; [ "$st" = "open" ] || continue
    printf '%s\t%s\t%s\t%s\n' "$(get_field "$f" priority)" "$(get_field "$f" created)" \
      "$(basename "$f" .md)" "$(title_of "$f")"
  done | sort -t$'\t' -k1,1nr -k2,2
}

cmd="${1:-help}"; shift || true
case "$cmd" in
  add)
    title=""; prio=0; body=""
    while [ $# -gt 0 ]; do case "$1" in
      -p|--priority) prio="$2"; shift 2;;
      -b|--body)     body="$2"; shift 2;;
      -*) die "unknown flag: $1";;
      *)  [ -z "$title" ] && title="$1" || title="$title $1"; shift;;
    esac; done
    [ -n "$title" ] || die "add needs a title"
    mkdir -p "$TODODIR"
    slug="$(printf '%s' "$title" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]-' | sed 's/--*/-/g; s/^-//; s/-$//')"
    slug="${slug:0:40}"; [ -n "$slug" ] || slug="task"
    seq=0
    for f in "$TODODIR"/[0-9][0-9][0-9][0-9]-*.md; do
      [ -e "$f" ] || continue; n="$(basename "$f" | cut -c1-4)"; n=$((10#$n)); [ "$n" -gt "$seq" ] && seq="$n"
    done
    while :; do
      seq=$((seq+1)); id="$(printf '%04d-%s' "$seq" "$slug")"; file="$TODODIR/$id.md"
      ( set -o noclobber; : > "$file" ) 2>/dev/null && break
    done
    { printf -- '---\nid: %s\nstatus: open\npriority: %s\ncreated: %s\nclaimed_by:\n---\n\n# %s\n' \
        "$id" "$prio" "$(now)" "$title"
      [ -n "$body" ] && printf '\n%s\n' "$body"; } > "$file"
    echo "$id"
    ;;
  list)
    echo "queue: $project"; rows="$(open_rows)"
    [ -z "$rows" ] && { echo "  (empty)"; exit 0; }
    printf '%s\n' "$rows" | while IFS=$'\t' read -r pr cr id title; do printf '  [%3s] %-32s %s\n' "$pr" "$id" "$title"; done
    ;;
  next)  open_rows | head -1 | cut -f3 ;;
  show)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "show needs an id"
    [ -e "$TODODIR/$id.md" ] || die "no such todo: $id"; cat "$TODODIR/$id.md"
    ;;
  claim)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "claim needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    st="$(get_field "$f" status)"
    [ "$st" = "open" ] || { echo "todo: $id is $st" >&2; exit 2; }
    set_field "$f" status taken
    set_field "$f" claimed_by "$(whoami)@$(hostname -s 2>/dev/null || echo host)"
    echo "claimed $id"
    ;;
  pr)
    id="$(sanitize "${1:-}")"; url="${2:-}"
    [ -n "$id" ] || die "pr needs an id"; [ -n "$url" ] || die "pr needs a url"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    # Insert-or-update a `pr:` line inside the frontmatter (set_field only updates
    # existing keys). Status is deliberately left as-is (taken) — a PR is in-review,
    # not done; `done` is reserved for after the PR merges.
    tmp="$(mktemp)"
    awk -v v="$url" '/^---[[:space:]]*$/{n++; if(n==2 && !d){print "pr: " v; d=1} print; next}
      n==1 && /^pr:/ {print "pr: " v; d=1; next} {print}' "$f" > "$tmp" && mv "$tmp" "$f"
    echo "pr $id -> $url"
    ;;
  done|close)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "done needs an id"
    [ -e "$TODODIR/$id.md" ] || die "no such todo: $id"
    set_field "$TODODIR/$id.md" status done; echo "done $id"
    ;;
  release|unclaim)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "release needs an id"
    [ -e "$TODODIR/$id.md" ] || die "no such todo: $id"
    set_field "$TODODIR/$id.md" status open; set_field "$TODODIR/$id.md" claimed_by ""; echo "released $id"
    ;;
  *) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//' ;;
esac
