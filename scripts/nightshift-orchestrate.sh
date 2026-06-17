#!/usr/bin/env bash
# devbrain/nightshift — multi-worker ORCHESTRATOR.
#
# Runs N interactive `claude` workers in parallel, each in its OWN git worktree
# (devbrain's "one worktree ↔ one branch ↔ one issue" rule — required so parallel
# workers don't collide; the queue's `claim` keeps them off the same task). The
# orchestrator:
#   • assigns /continue to each idle worker (turn-complete = its marker increments)
#   • on a hang (pane frozen > HANG with no marker) kills the worker, RELEASES its
#     claimed task, and respawns a fresh one
#   • when the queue empties, sends a worker a PLANNING turn that reads
#     .nightshift/followups.md + the objective and adds new TODOs (no code that turn)
#   • respawns any worker whose session dies
#   • runs FOREVER by default — never stops on a drained queue (it replans to
#     refill). Bound it with --max-turns / --max-wall, or stop via ostop / Ctrl-C.
#
# Watch any worker:   tmux attach -t ns-w0   (or w1/w2)
# Watch wall:         scripts/nightshift-wall.sh
#
# Usage:  nightshift-orchestrate.sh --repo BASE_CLONE [options]
#   --workers N      parallel workers           (default 3)
#   --hang SECS      frozen-pane hang threshold  (default 600)
#   --low N          replenish when open<N       (default 2)
#   --max-turns N    total turns across workers  (default 30)
#   --max-wall SECS  hard wall-clock stop        (default 28800 = 8h)
#   --poll SECS      poll interval               (default 15)
#   --base-branch B  branch staging is cut from  (default main)
#   --keep-staging   accumulate onto existing staging instead of resetting it
#   --test-cmd CMD   green-gate command (default: auto pytest in a venv)
#   --no-gate        merge without running tests (staging is disposable anyway)
#   --strict-gate    treat an inconclusive gate (no tests/tooling) as FAIL
#   --retries N      merge re-attempts before parking a task for the human (default 2)
#
# COMPOUNDING: workers branch off origin/staging (not main); on turn-complete the
# orchestrator merges the worker branch into staging IF the green-gate passes
# (serialized — the single orchestrator loop is the merge lock), marks the task
# `done`, and pushes. Conflicts / red tests requeue the task. You review and merge
# `git diff main...staging` → main yourself.

set -uo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
TODO="$HOME/.claude/hooks/devbrain-todo.sh"; [ -x "$TODO" ] || TODO="$SELF_DIR/todo.sh"

BASE=""; N=3; HANG=600; LOW=2; MAXTURNS=0; MAXWALL=0; POLL=15; REPLAN=300; FOREVER=1
BASE_BRANCH=main; KEEP_STAGING=0; TEST_CMD=""; NO_GATE=0; STRICT=0; RETRIES=2
# Defaults run FOREVER: 0 caps = unlimited. Workers are respawned if they die or go
# idle with no work; when the queue empties, a planning turn refills it (--replan).
# Stop with `ostop` / Ctrl-C, or set --max-turns / --max-wall to bound a run.
while [ $# -gt 0 ]; do case "$1" in
  --repo)        BASE="$2"; shift 2;;
  --workers)     N="$2"; shift 2;;
  --hang)        HANG="$2"; shift 2;;
  --low)         LOW="$2"; shift 2;;
  --max-turns)   MAXTURNS="$2"; FOREVER=0; shift 2;;
  --max-wall)    MAXWALL="$2"; FOREVER=0; shift 2;;
  --replan)      REPLAN="$2"; shift 2;;
  --poll)        POLL="$2"; shift 2;;
  --base-branch) BASE_BRANCH="$2"; shift 2;;
  --keep-staging) KEEP_STAGING=1; shift;;
  --test-cmd)    TEST_CMD="$2"; shift 2;;
  --no-gate)     NO_GATE=1; shift;;
  --strict-gate) STRICT=1; shift;;
  --retries)     RETRIES="$2"; shift 2;;
  *) echo "orch: unknown arg $1" >&2; exit 1;;
esac; done

STAGE_WT="$BASE-stage"; VENV="$BASE/.nightshift/venv"; RETRYDIR="$BASE/.nightshift/retries"

command -v tmux   >/dev/null 2>&1 || { echo "orch: tmux not found" >&2; exit 1; }
command -v claude >/dev/null 2>&1 || { echo "orch: claude not found" >&2; exit 1; }
[ -n "$BASE" ] || { echo "orch: --repo is required" >&2; exit 1; }
BASE="$(cd "$BASE" && pwd)" || { echo "orch: --repo not a dir" >&2; exit 1; }

NIGHTSHIFT_RULES="NIGHTSHIFT (unattended) MODE: you are running unattended in an automated loop; there is no human to answer questions this turn. Never ask the user anything and never use AskUserQuestion. Base every task on origin/staging, NOT main: when the /continue protocol says to branch off origin/main, branch off origin/staging instead and open your PR against the staging branch. When you would ask follow-up questions, instead append them to .nightshift/followups.md and queue them as TODOs via the devbrain-todo CLI. Be conservative about adding TODOs — only queue a follow-up that is essential to the objective and not already in the queue; the goal is to DRAIN the queue toward the objective, not grow it. Build a minimal MVP for the current task only, then end your turn."

PLAN_RULES="PLANNING TURN: the task queue is low. Do NOT write code or open a PR this turn. Read .nightshift/followups.md (if present) and the project objective (run: gbrain search for this project, and read its objective.md under the devbrain-data brain). Then add 3 to 6 concrete, minimal next TODOs that advance the objective via the devbrain-todo CLI (devbrain-todo add \"title\" -p PRIORITY -b \"why/acceptance\"), deduped against existing open tasks. Then end your turn."

# ---- shared helpers ----------------------------------------------------------
pane()  { tmux capture-pane -t "$1" -p 2>/dev/null; }
is_idle() {  # $1 session — footer present AND not mid-turn
  local p; p="$(pane "$1")" || return 1
  printf '%s' "$p" | grep -q "bypass permissions\|to cycle\|? for shortcuts" || return 1
  printf '%s' "$p" | grep -q "esc to interrupt" && return 1
  return 0
}
open_count() { ( cd "$BASE" && "$TODO" list 2>/dev/null ) | grep -cE '^[[:space:]]*\['; }
hashpane() { pane "$1" | cksum | awk '{print $1}'; }

handle_prompts() {  # $1 session — auto-clear trust + menus so nothing blocks
  local s="$1" p; p="$(pane "$s")"
  if printf '%s' "$p" | grep -qiE "trust this folder|trust the (files|authors)|Is this a project you"; then
    tmux send-keys -t "$s" "1"; tmux send-keys -t "$s" Enter; return 0
  fi
  if printf '%s' "$p" | grep -qE "Enter to select|Tab/Arrow keys to navigate"; then
    { echo "## menu @ $(date -u +%FT%TZ) [$s]"; printf '%s\n\n' "$p"; } >> "$BASE/.nightshift/followups.md" 2>/dev/null
    tmux send-keys -t "$s" Enter   # take the agent's recommended (highlighted) option
    return 0
  fi
  return 1
}
is_stuck_error() { printf '%s' "$(pane "$1")" | grep -qiE "API Error|Overloaded|\b529\b|usage limit|resets at"; }

send_prompt() { tmux send-keys -t "$1" -l "$2"; tmux send-keys -t "$1" Enter; }

spawn_worker() {  # $1 index
  local i="$1" wt sess marker
  wt="$BASE-w$i"; sess="ns-w$i"; marker="$wt/.nightshift/w$i.turns"
  git -C "$BASE" worktree prune 2>/dev/null
  git -C "$BASE" fetch -q origin 2>/dev/null
  [ -d "$wt" ] || git -C "$BASE" worktree add -f --detach "$wt" origin/staging >/dev/null 2>&1
  mkdir -p "$wt/.nightshift"
  tmux kill-session -t "$sess" 2>/dev/null
  tmux new-session -d -s "$sess" -c "$wt" -x 200 -y 50
  send_prompt "$sess" "export NIGHTSHIFT_MARKER='$marker'"
  local launch="claude --dangerously-skip-permissions --disallowedTools AskUserQuestion mcp__conductor__AskUserQuestion --append-system-prompt '$NIGHTSHIFT_RULES'"
  send_prompt "$sess" "$launch"
  WT[$i]="$wt"; SESS[$i]="$sess"; MARKER[$i]="$marker"
  BASE_CNT[$i]=0; LASTHASH[$i]=""; LASTCHG[$i]=$(date +%s); STATE[$i]="booting"; PROMPT_SENT[$i]=""
  echo "orch: spawned worker $i ($sess) in $wt"
}
mcount() { [ -f "${MARKER[$1]}" ] && wc -l < "${MARKER[$1]}" | tr -d ' ' || echo 0; }

release_branch_task() {  # $1 index — free the task this worker's worktree had claimed
  local b; b="$(git -C "${WT[$1]}" branch --show-current 2>/dev/null)"
  case "$b" in todo/*) ( cd "$BASE" && "$TODO" release "${b#todo/}" 2>/dev/null ) && echo "orch: released ${b#todo/}";; esac
}

# Ensure the turn-marker Stop hook is installed globally (guarded by NIGHTSHIFT_MARKER,
# so it only fires for workers). Global — NOT per-worktree — because /continue's
# `git stash -u` would stash a worktree-local .claude/settings.json mid-turn.
ensure_marker_hook() {
  local hook="$HOME/.claude/hooks/devbrain-turn-marker.sh" src="$SELF_DIR/../hooks/turn-marker.sh"
  mkdir -p "$HOME/.claude/hooks"
  [ -f "$src" ] && { cp "$src" "$hook"; chmod +x "$hook"; }
  [ -f "$hook" ] || { echo "orch: WARN turn-marker.sh not found — markers will not fire"; return; }
  command -v jq >/dev/null 2>&1 || { echo "orch: WARN jq missing — register Stop hook manually: $hook"; return; }
  local set="$HOME/.claude/settings.json" tmp; [ -f "$set" ] || echo '{}' > "$set"
  if ! grep -q "devbrain-turn-marker" "$set" 2>/dev/null; then
    tmp="$(mktemp)"
    jq --arg c "$hook" '.hooks.Stop = ((.hooks.Stop // []) + [{"hooks":[{"type":"command","command":$c}]}])' "$set" > "$tmp" && mv "$tmp" "$set" \
      && echo "orch: registered turn-marker Stop hook globally"
  fi
}

# ---- staging + green-gate + serialized automerge -----------------------------
setup_staging() {
  git -C "$BASE" fetch -q origin
  if [ "$KEEP_STAGING" = 1 ] && git -C "$BASE" ls-remote --exit-code --heads origin staging >/dev/null 2>&1; then
    echo "orch: keeping existing origin/staging"
  else
    git -C "$BASE" branch -f staging "origin/$BASE_BRANCH"
    git -C "$BASE" push -f -q origin staging
    echo "orch: staging reset to origin/$BASE_BRANCH"
  fi
  git -C "$BASE" worktree prune 2>/dev/null
  [ -d "$STAGE_WT" ] || git -C "$BASE" worktree add -f "$STAGE_WT" staging >/dev/null 2>&1
  git -C "$STAGE_WT" checkout -q staging 2>/dev/null; git -C "$STAGE_WT" reset -q --hard origin/staging
  mkdir -p "$RETRYDIR"
  # Exclude the state dir in ALL worktrees (shared info/exclude) so /continue's
  # `git add -A` never commits markers/logs into a task's PR.
  local excl="$BASE/.git/info/exclude"
  [ -f "$excl" ] && ! grep -qxF '.nightshift/' "$excl" 2>/dev/null && echo '.nightshift/' >> "$excl"
  if [ "$NO_GATE" != 1 ] && [ -z "$TEST_CMD" ]; then
    # Upgrade pip/setuptools/wheel FIRST — the venv default pip can be too old to do
    # PEP 660 editable installs from a pyproject-only project, which silently breaks
    # `pip install -e .` and leaves the package + its deps uninstalled (rc=2 gate).
    python3 -m venv "$VENV" >/dev/null 2>&1 \
      && "$VENV/bin/pip" install -q --upgrade pip setuptools wheel >/dev/null 2>&1 \
      && "$VENV/bin/pip" install -q pytest >/dev/null 2>&1 \
      && echo "orch: green-gate venv ready (pytest)" || echo "orch: WARN gate venv unavailable — gate may be inconclusive"
  fi
}

run_gate() {  # $1 dir → 0 pass · 1 fail · 2 inconclusive
  local dir="$1" out rc
  if [ -n "$TEST_CMD" ]; then
    out="$( cd "$dir" && timeout 600 bash -c "$TEST_CMD" 2>&1 )"; rc=$?
    [ "$rc" -eq 0 ] && { echo "  gate PASS: $TEST_CMD"; return 0; }
    echo "  gate FAIL ($TEST_CMD): $(printf '%s' "$out" | tail -2 | tr '\n' ' ' | cut -c1-160)"; return 1
  fi
  [ -x "$VENV/bin/python" ] || { echo "  gate inconclusive (no venv)"; return 2; }
  # Install the package + its declared deps (dev extras if present) so pytest can
  # actually import it. If this fails the suite won't collect → rc=2 → FAIL below,
  # which is correct: a task that can't be installed/imported must not merge.
  ( cd "$dir" && { "$VENV/bin/pip" install -q -e ".[dev]" >/dev/null 2>&1 || "$VENV/bin/pip" install -q -e . >/dev/null 2>&1; } ) || true
  out="$( cd "$dir" && timeout 600 "$VENV/bin/python" -m pytest -q 2>&1 )"; rc=$?
  case "$rc" in
    0) echo "  gate PASS (pytest)"; return 0;;
    5) echo "  gate inconclusive (no tests collected)"; return 2;;
    1) echo "  gate FAIL (pytest): $(printf '%s' "$out" | tail -2 | tr '\n' ' ' | cut -c1-160)"; return 1;;
    2) echo "  gate FAIL (collection/import error): $(printf '%s' "$out" | tail -2 | tr '\n' ' ' | cut -c1-160)"; return 1;;
    *) echo "  gate inconclusive (pytest rc=$rc)"; return 2;;
  esac
}

notify() {  # $1 title-suffix · $2 message — native macOS toast (best-effort)
  command -v osascript >/dev/null 2>&1 && \
    osascript -e "display notification \"$2\" with title \"nightshift\" subtitle \"$1\"" 2>/dev/null || true
}
requeue() {  # $1 id — release back to open, or PARK for the human after $RETRIES
  local id="$1" f="$RETRYDIR/$id" n; n=$(cat "$f" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$f"
  if [ "$n" -le "$RETRIES" ]; then ( cd "$BASE" && "$TODO" release "$id" 2>/dev/null ); echo "  requeued $id (attempt $n/$RETRIES)"
  else
    grep -qxF "$id" "$BASE/.nightshift/parked" 2>/dev/null || echo "$id" >> "$BASE/.nightshift/parked"
    echo "  ⚠ $id failed $n× — PARKED in review (needs you)"
    notify "needs your review" "$id couldn't merge after $RETRIES tries"
  fi
}

# Serialized by construction: only the single orchestrator loop calls this.
merge_to_staging() {  # $1 branch (todo/<id>) ; $2 task id
  local br="$1" id="$2" verdict
  git -C "$BASE" ls-remote --exit-code --heads origin "$br" >/dev/null 2>&1 || { echo "orch:   $br not pushed (worker turn produced no pushed branch) — requeue"; requeue "$id"; return 1; }
  git -C "$BASE" fetch -q origin
  git -C "$STAGE_WT" checkout -q staging 2>/dev/null; git -C "$STAGE_WT" reset -q --hard origin/staging
  if ! git -C "$STAGE_WT" merge --no-ff -q -m "nightshift: merge $br into staging" "origin/$br" >/dev/null 2>&1; then
    git -C "$STAGE_WT" merge --abort 2>/dev/null
    echo "orch: ✗ $br CONFLICTS with staging"; requeue "$id"; return 1
  fi
  if [ "$NO_GATE" = 1 ]; then verdict=0; else run_gate "$STAGE_WT"; verdict=$?; fi
  if [ "$verdict" -eq 0 ] || { [ "$verdict" -eq 2 ] && [ "$STRICT" != 1 ]; }; then
    if git -C "$STAGE_WT" push -q origin staging; then
      ( cd "$BASE" && "$TODO" done "$id" 2>/dev/null ); echo "orch: ✓ merged $br → staging; task $id done"
    else
      git -C "$STAGE_WT" reset -q --hard origin/staging
      echo "orch: ✗ push of staging failed for $br — requeue"; requeue "$id"
    fi
  else
    git -C "$STAGE_WT" reset -q --hard origin/staging
    echo "orch: ✗ $br failed gate — not merged"; requeue "$id"
  fi
}

# ---- boot --------------------------------------------------------------------
mkdir -p "$BASE/.nightshift"
exec > >(tee -a "$BASE/.nightshift/orchestrator.log") 2>&1   # stable log for the wall pane
echo "orch: starting $N workers on $BASE | hang=${HANG}s low=$LOW max-turns=$MAXTURNS gate=$([ "$NO_GATE" = 1 ] && echo off || echo on)"
ensure_marker_hook   # markers must fire for turn-detection / hang / automerge to work
setup_staging        # staging must exist before workers branch off it
declare -a WT SESS MARKER BASE_CNT LASTHASH LASTCHG STATE PROMPT_SENT
for i in $(seq 0 $((N-1))); do spawn_worker "$i"; done
echo "orch: workers booting; watch any with: tmux attach -t ns-w0"

START=$(date +%s); TURNS_DONE=0; PLANNED_LAST=0
[ "$FOREVER" = 1 ] && echo "orch: running FOREVER — respawns dead/idle workers, replans every ${REPLAN}s; stop with ostop/Ctrl-C"

# ---- the orchestration loop --------------------------------------------------
while :; do
  now=$(date +%s)
  [ "$MAXWALL"  -gt 0 ] && [ $((now - START)) -ge "$MAXWALL" ] && { echo "orch: wall-clock cap hit"; break; }
  [ "$MAXTURNS" -gt 0 ] && [ "$TURNS_DONE" -ge "$MAXTURNS" ]   && { echo "orch: max-turns cap hit"; break; }

  oc="$(open_count)"
  for i in $(seq 0 $((N-1))); do
    s="${SESS[$i]}"
    # respawn a worker whose session died (crash / closed)
    if ! tmux has-session -t "$s" 2>/dev/null; then
      echo "orch: worker $i session gone — respawning"; spawn_worker "$i"; s="${SESS[$i]}"; continue
    fi
    handle_prompts "$s" >/dev/null && { LASTCHG[$i]=$now; continue; }   # cleared a blocker

    cur="$(mcount "$i")"
    if [ "$cur" -gt "${BASE_CNT[$i]}" ]; then           # turn finished
      TURNS_DONE=$((TURNS_DONE + 1)); BASE_CNT[$i]="$cur"; STATE[$i]="idle"
      echo "orch: worker $i finished a turn (total turns: $TURNS_DONE)"
      # gate + merge the work this turn produced (skip planning turns — no todo/ branch)
      br="$(git -C "${WT[$i]}" branch --show-current 2>/dev/null)"
      case "$br" in todo/*) merge_to_staging "$br" "${br#todo/}";; esac
    fi

    if is_idle "$s"; then
      if [ "${STATE[$i]}" = "assigned" ]; then
        # idle but marker didn't advance (e.g. ended on an API error) — retry same prompt
        if is_stuck_error "$s"; then echo "orch: worker $i hit API/limit — resending"; fi
        send_prompt "$s" "${PROMPT_SENT[$i]}"; LASTCHG[$i]=$now; continue
      fi
      # needs an assignment
      if [ "$oc" -gt 0 ]; then
        send_prompt "$s" "/continue"; PROMPT_SENT[$i]="/continue"
        STATE[$i]="assigned"; BASE_CNT[$i]="$cur"; LASTCHG[$i]=$now
        echo "orch: worker $i assigned /continue (open=$oc)"
      elif [ $((now - PLANNED_LAST)) -gt "$REPLAN" ]; then
        # queue empty → generate more work so the fleet never starves (forever mode)
        echo "orch: queue empty — worker $i planning (replenish)"
        send_prompt "$s" "$PLAN_RULES"; PROMPT_SENT[$i]="$PLAN_RULES"
        STATE[$i]="assigned"; BASE_CNT[$i]="$cur"; LASTCHG[$i]=$now; PLANNED_LAST=$now
      else
        STATE[$i]="parked"   # no work + planned recently — re-plans after $REPLAN s
      fi
    else
      # busy: detect a hang via a frozen pane
      h="$(hashpane "$s")"
      if [ "$h" = "${LASTHASH[$i]}" ]; then
        if is_stuck_error "$s"; then LASTCHG[$i]=$now            # waiting out API/limit ≠ hang
        elif [ $((now - LASTCHG[$i])) -ge "$HANG" ]; then
          echo "orch: worker $i HUNG (${HANG}s frozen) — restarting"
          release_branch_task "$i"; tmux kill-session -t "$s" 2>/dev/null
          spawn_worker "$i"; continue
        fi
      else
        LASTHASH[$i]="$h"; LASTCHG[$i]=$now
      fi
    fi
  done
  # No drained-queue stop: in forever mode the loop keeps planning + respawning. It only
  # exits on the optional --max-turns/--max-wall caps (checked at the top).
  sleep "$POLL"
done

echo "orch: done. turns=$TURNS_DONE open=$(open_count) tasks left."
echo "orch: REVIEW WHAT LANDED →  git -C $STAGE_WT diff $BASE_BRANCH...staging   (then merge staging → $BASE_BRANCH)"
echo "orch: worker sessions left alive: ns-w0 .. ns-w$((N-1))"
