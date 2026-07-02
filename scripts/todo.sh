#!/usr/bin/env bash
# devbrain — TODO queue. One markdown file per task (conflict-free sync like the
# log); priority-ranked; `claim` marks a task taken so a parallel run skips it.
# Tasks are created by /distill and worked by /continue — this CLI is the substrate.
#
#   $DEVBRAIN_DATA/projects/<project>/todo/<id>.md
#
#   todo add "<title>" [-p N] [-b "body"]   create (prints id); priority 0-100, default 0
#   todo list [status]                      tasks by status (default open; 'all'=any), priority first
#   todo next                               id of the top open task (empty if none)
#   todo show <id>                          print a task file
#   todo edit <id> [-t "title"] [-b "body"] rewrite the title heading and/or the body
#   todo prio <id> <N>                      reprioritize an existing task (priority 0-100)
#   todo claim <id>                         mark open -> taken (exit 2 if not open)
#   todo review <id> [pr]                   mark -> review (PR open, awaiting merge); records pr
#   todo hold <id> [reason]                 mark -> held (needs a human: blocked/parked); records reason
#   todo approve <id>                        greenlight: set approved:true + reopen (worker may download/install/network)
#   todo done <id>                          close it (only after the PR merges); stamps done_at
#   todo self-heal [status...]              close open/taken tasks whose recorded PR has merged (zombie sweep)
#   todo release <id>                       taken/review/held -> open (un-claim / un-hold)
#   todo reopen <id> [reason]               FORCE done -> open (work verified absent; regenerate)
#   todo context <id>                       attach a synthesized "## Context" body section (reads stdin)
#
# Lifecycle: open -> taken -> review -> done, plus `held` (a terminal "needs you"
# bucket — blocked-unattended or failed-to-merge, with a `reason`). Anything not
# `open` is hidden from `next`/`list`, so a held task stops being handed out until
# a human `release`s it. `/continue` sets `review` on PR open, `done` on merge.
#
# Identity (which project's queue) = the working repo's git remote, like capture.
set -euo pipefail

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"
cwd="$PWD"
# Resolve identity via the shared offline resolver (project-key.sh) so the queue
# lives under the SAME projects/<owner>__<repo> folder capture and the skills use.
# Installed alongside as devbrain-project-key.sh; repo copy is ../hooks/project-key.sh.
_dir="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_dir/devbrain-hook-common.sh" "$_dir/../hooks/hook-common.sh" "$HOME/.claude/hooks/devbrain-hook-common.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
command -v devbrain_source_project_key >/dev/null 2>&1 || exit 1
devbrain_source_project_key || exit 1
project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"
TODODIR="$DATA/projects/$project/todo"

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
die() { echo "todo: $*" >&2; exit 1; }
epoch_of() {
  date -j -u -f '%Y-%m-%dT%H:%M:%SZ' "$1" +%s 2>/dev/null || date -u -d "$1" +%s 2>/dev/null || echo 0
}
# Merge-state of a PR (full URL or number) → MERGED|OPEN|CLOSED|"". Overridable via
# DEVBRAIN_PR_STATE_CMD (a command taking the pr ref) so `self-heal` is testable
# offline, without gh or the network.
pr_state() {
  if [ -n "${DEVBRAIN_PR_STATE_CMD:-}" ]; then "$DEVBRAIN_PR_STATE_CMD" "$1"
  else gh pr view "$1" --json state -q .state 2>/dev/null; fi
}
get_field() { awk -v k="$2" '/^---[[:space:]]*$/{n++; if(n==2)exit; next}
  n==1 && $0 ~ "^"k":" { sub("^"k":[[:space:]]*",""); print; exit }' "$1"; }
# Update a frontmatter field in place; if the field is absent, insert it just
# before the closing `---` (so it works on tasks created before the field existed,
# e.g. `pr:` on a pre-review-status task).
set_field() { local f="$1" k="$2" v="$3" tmp; tmp="$(mktemp)"
  awk -v k="$k" -v v="$v" '
    /^---[[:space:]]*$/{ n++; if(n==2 && !d){ print k": "v; d=1 } print; next }
    n==1 && $0 ~ "^"k":" && !d { print k": "v; d=1; next }
    { print }' "$f" > "$tmp" && mv "$tmp" "$f"; }
title_of() { awk '/^---[[:space:]]*$/{n++; next} n>=2 && /^# /{sub(/^# /,""); print; exit}' "$1"; }
# A title → a 40-char kebab slug (lowercased, alnum+dash only).
slugify() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]-' | sed 's/--*/-/g; s/^-//; s/-$//' | cut -c1-40; }
# Allocate the next NNNN-slug id, atomically create its empty file (noclobber loop
# beats a parallel writer to the slot), and echo the id.
alloc_file() {
  local slug="$1" seq=0 n id file f
  mkdir -p "$TODODIR"
  for f in "$TODODIR"/[0-9][0-9][0-9][0-9]-*.md; do
    [ -e "$f" ] || continue; n="$(basename "$f" | cut -c1-4)"; n=$((10#$n)); [ "$n" -gt "$seq" ] && seq="$n"
  done
  while :; do
    seq=$((seq+1)); id="$(printf '%04d-%s' "$seq" "$slug")"; file="$TODODIR/$id.md"
    ( set -o noclobber; : > "$file" ) 2>/dev/null && break
  done
  printf '%s' "$id"
}

# DEVBRAIN_TODO_ONLY scopes the queue to a fixed subset (nightshift fixed-set mode):
# a comma/space list of task ids — full slug (0081-foo-bar) OR bare 4-digit number
# (0081). When set, rows() (so next/list/open-count too) only sees tasks in the set;
# unset/empty = no filter. The nightshift orchestrator exports this so its workers'
# `next` only ever claims the chosen tasks.
only_match() {  # $1 task id → 0 if in DEVBRAIN_TODO_ONLY (or filter unset)
  [ -n "${DEVBRAIN_TODO_ONLY:-}" ] || return 0
  local id="$1" num="${1%%-*}" tok
  for tok in ${DEVBRAIN_TODO_ONLY//,/ }; do
    if [ "$tok" = "$id" ] || [ "$tok" = "$num" ]; then return 0; fi
  done
  return 1
}

DERIVE_READY=0; DERIVE_ON=0; DERIVE_DONE_IDS=""; DERIVE_BRANCH_IDS=""
derive_init() {
  [ "$DERIVE_READY" = 0 ] || return 0
  DERIVE_READY=1
  [ "${DEVBRAIN_TODO_DERIVE_GIT:-0}" = 1 ] || return 0
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 0
  # DEVBRAIN_TODO_FETCH_TTL=N: skip the network fetch if one landed within N seconds.
  # Every derive call pays a real `git fetch` (~5-10s against github) — a polling
  # consumer like the nightshift status emitter turns that into a 40s+ cycle. 0 (the
  # default) keeps today's always-fetch behavior; refs are at most N seconds stale.
  local ttl="${DEVBRAIN_TODO_FETCH_TTL:-0}" fh
  fh="$(git rev-parse --git-dir 2>/dev/null)/FETCH_HEAD"
  if [ "$ttl" -gt 0 ] 2>/dev/null && [ -f "$fh" ] \
     && [ $(( $(date +%s) - $(date -r "$fh" +%s 2>/dev/null || stat -c %Y "$fh" 2>/dev/null || echo 0) )) -lt "$ttl" ]; then
    :
  else
    git fetch -q origin 'refs/heads/nightshift:refs/remotes/origin/nightshift' 'refs/heads/todo/*:refs/remotes/origin/todo/*' 2>/dev/null || true
  fi
  git rev-parse --verify -q refs/remotes/origin/nightshift >/dev/null 2>&1 || return 0
  DERIVE_ON=1
  DERIVE_DONE_IDS="$(git log --format=%s refs/remotes/origin/nightshift 2>/dev/null \
    | sed -nE 's/^nightshift: merge todo\/([0-9]{4}-[a-z0-9-]+) into nightshift$/\1/p')"
  DERIVE_BRANCH_IDS="$(git for-each-ref --format='%(refname)' refs/remotes/origin/todo 2>/dev/null \
    | sed -nE 's#^refs/remotes/origin/todo/([0-9]{4}-[a-z0-9-]+)$#\1#p')"
}
# Pure-builtin exact-line match: this runs twice per task in every derive-mode `list`,
# and the printf|grep form costs 2 forks per call — ~10s of spawn overhead on a
# 100-task queue on macOS. Newline-fencing gives the same whole-line semantics.
line_has() { local nl='
'; case "$nl$1$nl" in *"$nl$2$nl"*) return 0;; esac; return 1; }
NOW_EPOCH=""   # cached once per process for lease math; a run never spans minutes
lease_alive() {
  local f="$1" ca age ttl="${DEVBRAIN_TODO_CLAIM_TTL:-5400}"
  ca="$(get_field "$f" claimed_at)"
  [ -n "$ca" ] || return 1
  [ -n "$NOW_EPOCH" ] || NOW_EPOCH="$(date +%s)"
  age=$(( NOW_EPOCH - $(epoch_of "$ca") ))
  [ "$age" -ge 0 ] && [ "$age" -lt "$ttl" ]
}
effective_status() {  # $1 task file ; $2 id
  local f="$1" id="$2" st; st="$(get_field "$f" status)"
  [ "$st" = held ] && { echo held; return; }
  derive_init
  [ "$DERIVE_ON" = 1 ] || { echo "${st:-open}"; return; }
  line_has "$DERIVE_DONE_IDS" "$id" && { echo done; return; }
  line_has "$DERIVE_BRANCH_IDS" "$id" && { echo review; return; }
  lease_alive "$f" && { echo taken; return; }
  echo open
}

# "priority<TAB>created<TAB>id<TAB>status<TAB>title" for tasks matching <filter>
# (default "open"; "all" = any status), sorted priority desc / FIFO on ties.
rows() {
  [ -d "$TODODIR" ] || return 0
  # Prime derive ONCE here, in the function's own shell. effective_status runs inside a
  # $(...) subshell below, so its derive_init call can never persist DERIVE_READY — left
  # unprimed, derive re-ran its git commands (fetch included) once PER TASK, turning a
  # 100-task derive `list` into ~600 git spawns / 14s (and N network fetches pre-TTL).
  derive_init
  local want="${1:-open}" f st
  for f in "$TODODIR"/*.md; do
    [ -e "$f" ] || continue
    only_match "$(basename "$f" .md)" || continue
    st="$(effective_status "$f" "$(basename "$f" .md)")"
    { [ "$want" = "all" ] || [ "$st" = "$want" ]; } || continue
    printf '%s\t%s\t%s\t%s\t%s\n' "$(get_field "$f" priority)" "$(get_field "$f" created)" \
      "$(basename "$f" .md)" "$st" "$(title_of "$f")"
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
    slug="$(slugify "$title")"; [ -n "$slug" ] || slug="task"
    id="$(alloc_file "$slug")"; file="$TODODIR/$id.md"
    { printf -- '---\nid: %s\nstatus: open\npriority: %s\ncreated: %s\nclaimed_by:\nclaimed_at:\npr:\n---\n\n# %s\n' \
        "$id" "$prio" "$(now)" "$title"
      [ -n "$body" ] && printf '\n%s\n' "$body"; } > "$file"
    echo "$id"
    ;;
  list)
    want="${1:-open}"
    case "$want" in open|taken|review|held|done|all) ;; *) die "list: bad status: $want (open|taken|review|held|done|all)";; esac
    hdr="queue: $project"; [ "$want" != "open" ] && hdr="$hdr ($want)"; echo "$hdr"
    out="$(rows "$want")"
    [ -z "$out" ] && { echo "  (empty)"; exit 0; }
    printf '%s\n' "$out" | while IFS=$'\t' read -r pr cr id st title; do
      if [ "$want" = "open" ]; then printf '  [%3s] %-32s %s\n' "$pr" "$id" "$title"
      else printf '  [%3s] %-7s %-32s %s\n' "$pr" "$st" "$id" "$title"; fi
    done
    ;;
  next)  rows open | head -1 | cut -f3 ;;
  show)
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "show needs an id"
    [ -e "$TODODIR/$id.md" ] || die "no such todo: $id"; cat "$TODODIR/$id.md"
    ;;
  edit)
    # Rewrite the `# ` title and/or the body; frontmatter is left untouched.
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "edit needs an id"; shift || true
    nt=""; nb=""; st=0; sb=0
    while [ $# -gt 0 ]; do case "$1" in
      -t|--title) nt="$2"; st=1; shift 2;;
      -b|--body)  nb="$2"; sb=1; shift 2;;
      *) die "edit: bad flag: $1";;
    esac; done
    [ "$st" = 1 ] || [ "$sb" = 1 ] || die "edit needs -t and/or -b"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    [ "$st" = 1 ] || nt="$(title_of "$f")"                          # keep current title/body
    [ "$sb" = 1 ] || nb="$(awk 'p&&NF{f=1} f{print} /^# /{p=1}' "$f")"   # ... for whichever flag was omitted
    { awk '{print} /^---[[:space:]]*$/{if(++n==2)exit}' "$f"        # frontmatter, verbatim
      printf '\n# %s\n' "$nt"; [ -n "$nb" ] && printf '\n%s\n' "$nb"
    } > "$f.tmp" && mv "$f.tmp" "$f"
    echo "edited $id"
    ;;
  prio|reprioritize)
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "prio needs an id"; shift || true
    p="${1:-}"; case "$p" in ''|*[!0-9]*) die "prio needs a number 0-100";; esac
    [ "$p" -le 100 ] || die "prio out of range: $p (must be 0-100)"   # upper bound, not just digits
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" priority "$p"; echo "prio $id -> $p"
    ;;
  claim)
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "claim needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    st="$(effective_status "$f" "$id")"
    [ "$st" = "open" ] || { echo "todo: $id is $st" >&2; exit 2; }
    set_field "$f" status taken
    set_field "$f" claimed_by "$(whoami)@$(hostname -s 2>/dev/null || echo host)"
    set_field "$f" claimed_at "$(now)"   # lease timestamp → a dead worker's stale claim can be reclaimed (nightshift F5)
    echo "claimed $id"
    ;;
  review)
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "review needs an id"; shift || true
    pr="${1:-}"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" status review
    [ -n "$pr" ] && set_field "$f" pr "$pr"
    echo "review $id${pr:+ (pr $pr)}"
    ;;
  hold)
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "hold needs an id"; shift || true
    reason="$*"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" status held
    [ -n "$reason" ] && set_field "$f" reason "$reason"
    echo "held $id${reason:+ ($reason)}"
    ;;
  approve)
    # Human greenlight: a worker may do the downloads/installs/network this task
    # needs (overrides the unattended self-hold policy). Re-opens it for pickup.
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "approve needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" approved true
    set_field "$f" status open
    set_field "$f" claimed_by ""
    # Reopening for fresh work: clear the old merged-PR record + done stamp so the
    # self-heal sweep doesn't see (open + merged pr) and re-close this as a zombie.
    set_field "$f" pr ""; set_field "$f" done_at ""
    echo "approved $id — unattended execution authorized; back to open"
    ;;
  note)
    # record a one-line failure/feedback note the next worker sees via `show` (status unchanged)
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "note needs an id"; shift || true
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" last_failure "$*"; echo "noted $id"
    ;;
  context)
    # Attach a synthesized "## Context" section to the task body (multi-line, from
    # stdin) — /continue writes here after querying gbrain so the next worker and the
    # user see the gathered context. Replaces any prior block so re-runs don't pile up.
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "context needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    [ -t 0 ] && die "context reads the body on stdin (pipe or heredoc it)"   # don't hang on a tty
    ctx="$(cat)"; [ -n "$ctx" ] || die "context needs the body on stdin"
    # task minus any prior block; $(...) trims trailing blanks so re-runs don't accrete them
    body="$(awk '/^## Context \(synthesized /{exit} {print}' "$f")"
    printf '%s\n\n## Context (synthesized %s)\n\n%s\n' "$body" "$(now)" "$ctx" > "$f"
    echo "context $id"
    ;;
  done|close)
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "done needs an id"
    [ -e "$TODODIR/$id.md" ] || die "no such todo: $id"
    # stamp completion time → cycle time (created -> done) is measurable
    set_field "$TODODIR/$id.md" status done
    set_field "$TODODIR/$id.md" done_at "$(now)"; echo "done $id"
    ;;
  self-heal|selfheal|heal)
    # Defense-in-depth for the merge→done path: scan open/taken tasks that carry a
    # pr:, ask whether each PR merged, and close the merged ones. Catches zombies left
    # by a manually-merged PR or any path that bypassed `todo done`. /distill step 3c
    # does this for `review` tasks (with confirmation); this auto-heals the open/taken
    # backlog, where an already-merged PR is an unambiguous zombie. Override the merge
    # lookup with DEVBRAIN_PR_STATE_CMD; statuses to scan default to "open taken".
    [ -n "${DEVBRAIN_PR_STATE_CMD:-}" ] || command -v gh >/dev/null 2>&1 || die "self-heal needs gh (GitHub CLI)"
    statuses="${*:-open taken}"; healed=0
    for st in $statuses; do
      for id in $(rows "$st" | cut -f3); do
        f="$TODODIR/$id.md"; [ -e "$f" ] || continue
        pr="$(get_field "$f" pr)"; [ -n "$pr" ] || continue
        [ "$(pr_state "$pr")" = "MERGED" ] || continue
        set_field "$f" status done; set_field "$f" done_at "$(now)"
        echo "self-heal: closed $id (pr merged: $pr)"; healed=$((healed+1))
      done
    done
    echo "self-heal: $healed task(s) closed"
    ;;
  release|unclaim)
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "release needs an id"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    # `done` is terminal: never reopen a completed task. Guards the nightshift race
    # where a watchdog requeue fires after the merge-success path already closed the
    # task — that otherwise zombies it (status=open + done_at + a merged PR) and the
    # queue keeps handing the finished work back out.
    [ "$(get_field "$f" status)" = "done" ] && { echo "todo: $id already done — not releasing" >&2; exit 0; }
    set_field "$f" status open; set_field "$f" claimed_by ""; set_field "$f" claimed_at ""
    # Clear any old merged-PR record + done stamp on reopen so the self-heal sweep can't
    # re-close this intentionally-reopened task as a zombie (open + merged pr). Also clear the
    # hold `reason`: a released task is no longer held, so a lingering note (e.g. nightshift's
    # fixed-set parking note) is stale and shouldn't keep showing on the card.
    set_field "$f" pr ""; set_field "$f" done_at ""; set_field "$f" reason ""; echo "released $id"
    ;;
  reopen)
    # Counterpart to `release`'s done-is-terminal guard: force a `done` task back to `open` when
    # its work is VERIFIED absent. Unlike `approve` it sets no `approved:true` flag — it only
    # un-closes the task; the optional reason is stamped as last_failure for the next worker.
    id="$(devbrain_sanitize "${1:-}")"; [ -n "$id" ] || die "reopen needs an id"; shift || true
    reason="$*"
    f="$TODODIR/$id.md"; [ -e "$f" ] || die "no such todo: $id"
    set_field "$f" status open; set_field "$f" claimed_by ""; set_field "$f" claimed_at ""
    set_field "$f" pr ""; set_field "$f" done_at ""; set_field "$f" reason ""
    [ -n "$reason" ] && set_field "$f" last_failure "$reason"
    echo "reopened $id${reason:+ ($reason)}"
    ;;
  help|-h|--help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//' >&2; die "unknown command: $cmd";;
esac
