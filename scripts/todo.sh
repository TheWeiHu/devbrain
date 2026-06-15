#!/usr/bin/env bash
# devbrain — TODO queue (file-per-task, git-native, no central DB).
#
# A priority-ranked work queue that any agent can pull from. One markdown file
# per TODO with YAML frontmatter — the same conflict-free sharding the prompt log
# uses: two agents touching *different* TODOs never collide, so the queue syncs
# across machines by plain `git pull` (the flusher pushes the data repo). After
# cullback/ticket: the file IS the ticket; git is the database; no service, no lock
# server. devbrain adds one thing tk leaves to convention — an explicit *claim*
# (status open -> taken), made atomic locally with a mkdir guard so parallel
# Conductor worktrees on one machine can't grab the same task.
#
# Layout:
#   $DEVBRAIN_DATA/projects/<project>/todo/<id>.md
#
# Identity (project) is read FROM the working repo's git remote, exactly like the
# capture hook — so `todo` run from any worktree of a repo targets one queue.
#
# Usage:
#   todo add "<title>" [-p N] [-t tag] [-d depid] [-b "body"]   create (prints id)
#   todo list [--all] [--json]                                  open+ready, priority desc
#   todo ready [--json]            open todos whose deps are all done
#   todo blocked [--json]          open todos with unfinished deps
#   todo next [--json]             the single top ready+open todo (the loop pulls this)
#   todo show <id>                 print a todo's file
#   todo claim <id> [--by WHO]     atomically open -> taken (fails if not open)
#   todo done <id>                 mark done (closes the task)
#   todo release <id>              taken -> open (un-claim)
#   todo reopen <id>               done -> open
#   todo rm <id>                   delete a todo file
#
# Exit codes: 0 ok · 1 usage/not-found · 2 claim conflict (already taken / busy).
set -euo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

# ---- identity (mirror hooks/capture.sh) ------------------------------------
cwd="$PWD"
remote="$(git -C "$cwd" remote get-url origin 2>/dev/null || true)"
if [ -n "$remote" ]; then project="$(basename "${remote%.git}")"; else project="$(basename "$cwd")"; fi
sanitize() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'; }
project="$(sanitize "$project")"; [ -n "$project" ] || project="unknown"

# Allow an explicit override so the queue can be inspected for another project.
[ -n "${DEVBRAIN_PROJECT:-}" ] && project="$(sanitize "$DEVBRAIN_PROJECT")"

TODODIR="$DATA/projects/$project/todo"

# Who am I, for claims: <worktree>.<short-session>@<host>. Session id is exported
# by the loop/skill when available; otherwise fall back to PID.
claimant() {
  local top wt host sess
  top="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null || true)"
  wt="$(sanitize "$(basename "${top:-$cwd}")")"
  host="$(sanitize "$(hostname -s 2>/dev/null || echo host)")"
  sess="${DEVBRAIN_SESSION:-$$}"; sess="$(sanitize "$sess")"; sess="${sess:0:8}"
  printf '%s.%s@%s' "$wt" "$sess" "$host"
}

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

die() { echo "todo: $*" >&2; exit 1; }

# ---- frontmatter helpers ---------------------------------------------------
# Read a scalar field from a todo file's frontmatter (first match wins).
get_field() { # <file> <key>
  awk -v k="$2" '
    /^---[[:space:]]*$/ { n++; if (n==2) exit; next }
    n==1 {
      if ($0 ~ "^"k":") { sub("^"k":[[:space:]]*", ""); print; exit }
    }
  ' "$1"
}

# Set/replace a scalar field inside the frontmatter (must already exist).
set_field() { # <file> <key> <value>
  local f="$1" k="$2" v="$3" tmp
  tmp="$(mktemp)"
  awk -v k="$k" -v v="$v" '
    /^---[[:space:]]*$/ { n++; print; next }
    n==1 && $0 ~ "^"k":" && !done { print k": "v; done=1; next }
    { print }
  ' "$f" > "$tmp" && mv "$tmp" "$f"
}

title_of() { # <file>  -> first markdown H1 after frontmatter
  awk '
    /^---[[:space:]]*$/ { n++; next }
    n>=2 && /^# / { sub(/^# /, ""); print; exit }
  ' "$1"
}

# Is every dependency id of <file> in status done? (empty deps => ready)
deps_satisfied() { # <file>
  local deps d st
  deps="$(get_field "$1" deps)"
  deps="${deps#[}"; deps="${deps%]}"            # strip [ ]
  deps="$(printf '%s' "$deps" | tr ',' ' ')"
  [ -z "${deps// }" ] && return 0
  for d in $deps; do
    d="$(sanitize "$d")"
    [ -z "$d" ] && continue
    [ -e "$TODODIR/$d.md" ] || return 1         # missing dep = not satisfied
    st="$(get_field "$TODODIR/$d.md" status)"
    [ "$st" = "done" ] || return 1
  done
  return 0
}

# ---- listing ---------------------------------------------------------------
# Emit TSV rows: priority\tcreated\tid\tstatus\treadyflag\ttitle  for all todos.
rows() {
  [ -d "$TODODIR" ] || return 0
  local f id
  for f in "$TODODIR"/*.md; do
    [ -e "$f" ] || continue
    id="$(basename "$f" .md)"
    local pr cr st ready title
    pr="$(get_field "$f" priority)";  pr="${pr:-0}"
    cr="$(get_field "$f" created)";   cr="${cr:-0000}"
    st="$(get_field "$f" status)";    st="${st:-open}"
    title="$(title_of "$f")"
    if deps_satisfied "$f"; then ready=1; else ready=0; fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$pr" "$cr" "$id" "$st" "$ready" "$title"
  done
}

# Sort rows: priority desc, then created asc (FIFO within a priority).
sorted_rows() { rows | sort -t$'\t' -k1,1nr -k2,2; }

print_table() { # reads filtered TSV on stdin
  local any=0
  while IFS=$'\t' read -r pr cr id st ready title; do
    any=1
    local mark=""
    [ "$ready" = 0 ] && [ "$st" = open ] && mark=" (blocked)"
    printf '  [%3s] %-22s %-6s %s%s\n' "$pr" "$id" "$st" "$title" "$mark"
  done
  [ "$any" = 0 ] && echo "  (none)"
  return 0
}

print_json() { # reads filtered TSV on stdin
  local first=1
  printf '['
  while IFS=$'\t' read -r pr cr id st ready title; do
    [ $first = 1 ] || printf ','
    first=0
    if command -v jq >/dev/null 2>&1; then
      jq -cn --arg id "$id" --arg st "$st" --arg t "$title" --arg cr "$cr" \
            --argjson pr "${pr:-0}" --argjson ready "${ready:-0}" \
            '{id:$id,status:$st,priority:$pr,ready:($ready==1),created:$cr,title:$t}'
    else
      printf '{"id":"%s","status":"%s","priority":%s,"ready":%s,"title":"%s"}' \
        "$id" "$st" "$pr" "$([ "$ready" = 1 ] && echo true || echo false)" "$title"
    fi
  done
  printf ']\n'
}

# ---- commands --------------------------------------------------------------
cmd="${1:-help}"; shift || true

case "$cmd" in
  add)
    title=""; prio=0; body=""; tags=(); deps=()
    while [ $# -gt 0 ]; do
      case "$1" in
        -p|--priority) prio="$2"; shift 2;;
        -t|--tag)      tags+=("$(sanitize "$2")"); shift 2;;
        -d|--dep)      deps+=("$(sanitize "$2")"); shift 2;;
        -b|--body)     body="$2"; shift 2;;
        -*)            die "unknown flag: $1";;
        *)             [ -z "$title" ] && title="$1" || title="$title $1"; shift;;
      esac
    done
    [ -n "$title" ] || die "add needs a title"
    mkdir -p "$TODODIR"
    # id = <4-digit seq>-<slug-of-title>; bump seq on collision (race-safe create).
    slug="$(printf '%s' "$title" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]-' | sed 's/--*/-/g; s/^-//; s/-$//')"
    slug="${slug:0:40}"; [ -n "$slug" ] || slug="task"
    seq=0
    for f in "$TODODIR"/[0-9][0-9][0-9][0-9]-*.md; do
      [ -e "$f" ] || continue
      n="$(basename "$f" | cut -c1-4)"; n=$((10#$n))
      [ "$n" -gt "$seq" ] && seq="$n"
    done
    while :; do
      seq=$((seq+1))
      id="$(printf '%04d-%s' "$seq" "$slug")"
      file="$TODODIR/$id.md"
      if ( set -o noclobber; : > "$file" ) 2>/dev/null; then break; fi
    done
    tag_csv="$(IFS=,; echo "${tags[*]:-}")"
    dep_csv="$(IFS=,; echo "${deps[*]:-}")"
    {
      printf -- '---\n'
      printf 'id: %s\n' "$id"
      printf 'status: open\n'
      printf 'priority: %s\n' "$prio"
      printf 'created: %s\n' "$(now)"
      printf 'claimed_by:\n'
      printf 'claimed_at:\n'
      printf 'deps: [%s]\n' "$dep_csv"
      printf 'tags: [%s]\n' "$tag_csv"
      printf -- '---\n\n'
      printf '# %s\n' "$title"
      [ -n "$body" ] && printf '\n%s\n' "$body"
    } > "$file"
    echo "$id"
    ;;

  list)
    json=0; all=0
    for a in "$@"; do case "$a" in --json) json=1;; --all) all=1;; esac; done
    if [ "$all" = 1 ]; then
      out="$(sorted_rows)"
    else
      out="$(sorted_rows | awk -F'\t' '$4=="open"')"
    fi
    if [ "$json" = 1 ]; then printf '%s\n' "$out" | grep -v '^$' | print_json
    else echo "queue: $project"; printf '%s\n' "$out" | grep -v '^$' | print_table; fi
    ;;

  ready)
    json=0; for a in "$@"; do [ "$a" = --json ] && json=1; done
    out="$(sorted_rows | awk -F'\t' '$4=="open" && $5==1')"
    if [ "$json" = 1 ]; then printf '%s\n' "$out" | grep -v '^$' | print_json
    else echo "ready: $project"; printf '%s\n' "$out" | grep -v '^$' | print_table; fi
    ;;

  blocked)
    json=0; for a in "$@"; do [ "$a" = --json ] && json=1; done
    out="$(sorted_rows | awk -F'\t' '$4=="open" && $5==0')"
    if [ "$json" = 1 ]; then printf '%s\n' "$out" | grep -v '^$' | print_json
    else echo "blocked: $project"; printf '%s\n' "$out" | grep -v '^$' | print_table; fi
    ;;

  next)
    json=0; for a in "$@"; do [ "$a" = --json ] && json=1; done
    row="$(sorted_rows | awk -F'\t' '$4=="open" && $5==1 {print; exit}')"
    if [ -z "$row" ]; then
      [ "$json" = 1 ] && echo "null" || echo "(queue empty — no ready open todos)"
      exit 0
    fi
    if [ "$json" = 1 ]; then printf '%s\n' "$row" | print_json | sed 's/^\[//; s/\]$//'
    else printf '%s\n' "$row" | print_table; fi
    ;;

  show)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "show needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    cat "$f"
    ;;

  claim)
    id=""; who=""
    while [ $# -gt 0 ]; do case "$1" in --by) who="$2"; shift 2;; *) id="$(sanitize "$1")"; shift;; esac; done
    [ -n "$id" ] || die "claim needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    [ -n "$who" ] || who="$(claimant)"
    # mkdir is atomic create-or-fail — serializes the read-modify-write so two
    # local agents can't both flip the same file. The durable claim is the
    # committed `status: taken`; across machines git push ordering arbitrates.
    lock="$TODODIR/.lock-$id"
    if ! mkdir "$lock" 2>/dev/null; then echo "todo: $id is busy (another claim in flight)" >&2; exit 2; fi
    trap 'rmdir "$lock" 2>/dev/null || true' EXIT
    st="$(get_field "$f" status)"
    if [ "$st" != "open" ]; then
      echo "todo: $id is not open (status: $st, claimed_by: $(get_field "$f" claimed_by))" >&2
      exit 2
    fi
    set_field "$f" status taken
    set_field "$f" claimed_by "$who"
    set_field "$f" claimed_at "$(now)"
    echo "claimed $id by $who"
    ;;

  done|close)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "done needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" status done
    set_field "$f" claimed_at "$(now)"
    echo "done $id"
    ;;

  release|unclaim)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "release needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" status open
    set_field "$f" claimed_by ""
    set_field "$f" claimed_at ""
    echo "released $id"
    ;;

  reopen)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "reopen needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" status open
    echo "reopened $id"
    ;;

  rm|delete)
    id="$(sanitize "${1:-}")"; [ -n "$id" ] || die "rm needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    rm -f "$f"
    echo "removed $id"
    ;;

  dir)  echo "$TODODIR" ;;

  help|-h|--help|*)
    sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
    ;;
esac
