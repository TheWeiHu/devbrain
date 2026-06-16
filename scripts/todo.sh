#!/usr/bin/env bash
# devbrain — TODO queue (file-per-task, git-native, no central DB).
#
# A priority-ranked work queue. One markdown file per task — the same conflict-free
# sharding the prompt log uses: two agents touching *different* tasks never collide,
# so the queue syncs by plain `git pull` (the flusher pushes the data repo). After
# cullback/ticket: the file IS the ticket, git IS the database, no service. devbrain
# adds one thing — an explicit *claim* (open -> taken), atomic via a mkdir guard, so
# parallel agents can't grab the same task.
#
# Tasks are not written here by hand much: `/distill` extracts them from the log,
# and `/continue` pulls the top one and works it. This CLI is the thin substrate.
#
#   $DEVBRAIN_DATA/projects/<project>/todo/<id>.md
#
# Usage:
#   todo add "<title>" [-p N] [-b "body"]   create (prints id); priority 0-100, default 0
#   todo list                               open tasks, highest priority first
#   todo next                               print the id of the top open task (empty if none)
#   todo show <id>                          print a task file
#   todo claim <id> [--by WHO]              atomically open -> taken (exit 2 if not open)
#   todo done <id>                          close it
#   todo release <id>                       taken -> open (un-claim)
#
# Identity (which project's queue) is the working repo's git remote, like capture.
# Exit: 0 ok · 1 usage/not-found · 2 claim conflict.
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

# Who claims: <worktree>.<short-session>@<host>.
claimant() {
  local top wt host sess
  top="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null || true)"
  wt="$(sanitize "$(basename "${top:-$cwd}")")"
  host="$(sanitize "$(hostname -s 2>/dev/null || echo host)")"
  sess="$(sanitize "${DEVBRAIN_SESSION:-$$}")"; sess="${sess:0:8}"
  printf '%s.%s@%s' "$wt" "$sess" "$host"
}

get_field() { # <file> <key>  — read a scalar from frontmatter
  awk -v k="$2" '/^---[[:space:]]*$/{n++; if(n==2)exit; next}
    n==1 && $0 ~ "^"k":" { sub("^"k":[[:space:]]*",""); print; exit }' "$1"
}
set_field() { # <file> <key> <value>  — replace a scalar in frontmatter
  local f="$1" k="$2" v="$3" tmp; tmp="$(mktemp)"
  awk -v k="$k" -v v="$v" '/^---[[:space:]]*$/{n++; print; next}
    n==1 && $0 ~ "^"k":" && !d { print k": "v; d=1; next } { print }' "$f" > "$tmp" && mv "$tmp" "$f"
}
title_of() { awk '/^---[[:space:]]*$/{n++; next} n>=2 && /^# /{sub(/^# /,""); print; exit}' "$1"; }

# Emit "priority<TAB>created<TAB>id<TAB>status<TAB>title" for open tasks, sorted
# priority desc then created asc (FIFO within a priority).
open_rows() {
  [ -d "$TODODIR" ] || return 0
  local f id pr cr st
  for f in "$TODODIR"/*.md; do
    [ -e "$f" ] || continue
    st="$(get_field "$f" status)"; [ "$st" = "open" ] || continue
    id="$(basename "$f" .md)"
    pr="$(get_field "$f" priority)"; pr="${pr:-0}"
    cr="$(get_field "$f" created)";  cr="${cr:-0000}"
    printf '%s\t%s\t%s\t%s\t%s\n' "$pr" "$cr" "$id" "$st" "$(title_of "$f")"
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
      if ( set -o noclobber; : > "$file" ) 2>/dev/null; then break; fi
    done
    {
      printf -- '---\nid: %s\nstatus: open\npriority: %s\ncreated: %s\nclaimed_by:\nclaimed_at:\n---\n\n# %s\n' \
        "$id" "$prio" "$(now)" "$title"
      [ -n "$body" ] && printf '\n%s\n' "$body"
    } > "$file"
    echo "$id"
    ;;

  list)
    echo "queue: $project"
    rows="$(open_rows)"
    if [ -z "$rows" ]; then echo "  (empty)"; else
      printf '%s\n' "$rows" | while IFS=$'\t' read -r pr cr id st title; do
        printf '  [%3s] %-32s %s\n' "$pr" "$id" "$title"
      done
    fi
    ;;

  next)  # print just the top task id (empty if none) — built for `id=$(todo next)`
    open_rows | head -1 | cut -f3
    ;;

  show)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "show needs an id"
    [ -e "$TODODIR/$id.md" ] || die "no such todo: $id"; cat "$TODODIR/$id.md"
    ;;

  claim)
    id=""; who=""
    while [ $# -gt 0 ]; do case "$1" in --by) who="$2"; shift 2;; *) id="$(sanitize "$1")"; shift;; esac; done
    [ -n "$id" ] || die "claim needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    [ -n "$who" ] || who="$(claimant)"
    # mkdir = atomic create-or-fail; serializes the read-modify-write so two local
    # agents can't both flip the same file. Durable claim is the committed `taken`.
    lock="$TODODIR/.lock-$id"
    mkdir "$lock" 2>/dev/null || { echo "todo: $id is busy (claim in flight)" >&2; exit 2; }
    trap 'rmdir "$lock" 2>/dev/null || true' EXIT
    st="$(get_field "$f" status)"
    [ "$st" = "open" ] || { echo "todo: $id is $st (claimed_by: $(get_field "$f" claimed_by))" >&2; exit 2; }
    set_field "$f" status taken; set_field "$f" claimed_by "$who"; set_field "$f" claimed_at "$(now)"
    echo "claimed $id by $who"
    ;;

  done|close)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "done needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" status done; set_field "$f" claimed_at "$(now)"; echo "done $id"
    ;;

  release|unclaim)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "release needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" status open; set_field "$f" claimed_by ""; set_field "$f" claimed_at ""; echo "released $id"
    ;;

  dir)  echo "$TODODIR" ;;
  help|-h|--help|*) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//' ;;
esac
