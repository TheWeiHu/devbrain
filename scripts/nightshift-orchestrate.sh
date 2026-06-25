#!/usr/bin/env bash
# devbrain/nightshift — multi-worker ORCHESTRATOR.
#
# Runs N `claude` workers in parallel, each in its OWN git worktree (devbrain's
# "one worktree ↔ one branch ↔ one issue" rule — required so parallel workers
# don't collide; the queue's `claim` keeps them off the same task). The
# orchestrator assigns /continue to idle workers, gates + merges each completed
# turn into `nightshift`, replans when the queue empties, and runs FOREVER (bound
# with --max-turns / --max-wall, or stop via ostop / Ctrl-C).
#
# ── TWO EXECUTION BACKENDS ───────────────────────────────────────────────────
# headless  (DEFAULT) — one `claude -p` per turn per worker. The process IS the
#           turn: its exit is the turn boundary, its exit code/stdout the result.
#           No tmux, no Stop-hook marker, no terminal-scraping — far simpler and
#           more robust. This is what you want.
#   --tmux  (FALLBACK) — drive a persistent interactive `claude` in a tmux pane
#           via send-keys, detecting turn-completion with a Stop-hook marker file
#           and scraping the pane for state. More moving parts; kept only as a hedge
#           against a future `claude -p` pricing change (the `nightshift` CLI prints
#           the full why at `start --tmux`, and it's in the no-arg help).
#
# Watch (either mode):  devbrain nightshift watch   (browser dashboard)
# Watch a tmux worker:  tmux attach -t ns-w0      (--tmux mode only)
#
# Usage:  nightshift-orchestrate.sh --repo BASE_CLONE [options]
#   --workers N      parallel workers           (default 3)
#   --headless       run each turn as a detached `claude -p`        (DEFAULT backend)
#   --tmux           use the interactive tmux backend instead of headless claude -p
#   --turn-timeout S max seconds for one headless turn (default 1800; SIGTERM after)
#   --hang SECS      frozen-pane hang threshold  (default 600; --tmux only)
#   --max-turns N    stop after N completed turns (default 0 = unlimited / run forever)
#   --max-wall SECS  stop after S seconds wall-clock (default 0 = unlimited / run forever)
#   --replan SECS    min gap between empty-queue planning turns, measured since the LAST
#                    plan — not since the queue emptied (default 300). One plan per window,
#                    fleet-wide; the first one always fires (counter starts at 0).
#   --poll SECS      poll interval               (default 15)
#   --base-branch B  branch nightshift is cut from  (default main)
#   --keep-nightshift   accumulate onto existing nightshift instead of resetting it
#   --test-cmd CMD   green-gate command (default: auto pytest in a venv)
#   --no-gate        merge without running tests (nightshift is disposable anyway)
#   --strict-gate    treat an inconclusive gate (no tests/tooling) as FAIL
#   --retries N      merge re-attempts before parking a task for the human (default 2)
#   --notify         macOS notifications on stall / usage-limit  (default off)
#   (--low N is accepted for back-compat but is a no-op; replenish is time-based via --replan)
#
# COMPOUNDING: workers branch off origin/nightshift (not main); on turn-complete the
# orchestrator merges the worker branch into nightshift IF the green-gate passes
# (serialized — the single orchestrator loop is the merge lock), marks the task
# `done`, and pushes. Conflicts / red tests requeue the task. You review and merge
# `git diff main...nightshift` → main yourself.

set -uo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
TODO="$HOME/.claude/hooks/devbrain-todo.sh"; [ -x "$TODO" ] || TODO="$SELF_DIR/todo.sh"

BASE=""; N=3; HANG=600; LOW=2; MAXTURNS=0; MAXWALL=0; POLL=15; REPLAN=300; FOREVER=1
BASE_BRANCH=main; KEEP_NIGHTSHIFT=0; TEST_CMD=""; NO_GATE=0; STRICT=0; RETRIES=2
MODE=headless; TURN_MAX=1800   # default backend = claude -p; per-turn wall cap (s)
GATE_PY=python3; GATE_IMPORT_ERROR=0   # interpreter chosen for the gate venv; set in setup_nightshift
CLAIM_TTL=5400         # a task claimed (→ taken) longer than this with no live worktree branch is reclaimed
STALL_K=8; RECON_EVERY=8   # stall after K turns with no new merge; reconcile every N polls
NOTIFY=0                   # macOS notifications OFF by default; --notify to enable
LIMIT_BACKOFF=300          # on a usage limit, poll/ping only every 5 min (not aggressively)
RESEND_GRACE=60            # don't re-send /continue within this many s of the last send (kills startup spam)
# Defaults run FOREVER: 0 caps = unlimited. Workers are respawned if they die or go
# idle with no work. When the queue is empty AND it's been >--replan seconds since the
# LAST planning turn, one worker spends a turn planning to refill it (cooldown is measured
# from the last plan, not from when the queue emptied).
# Stop with `ostop` / Ctrl-C, or set --max-turns / --max-wall to bound a run.
while [ $# -gt 0 ]; do case "$1" in
  --repo)        BASE="$2"; shift 2;;
  --workers)     N="$2"; shift 2;;
  --tmux)        MODE=tmux; shift;;
  --headless)    MODE=headless; shift;;
  --turn-timeout) TURN_MAX="$2"; shift 2;;
  --hang)        HANG="$2"; shift 2;;
  --low)         LOW="$2"; shift 2;;
  --max-turns)   MAXTURNS="$2"; FOREVER=0; shift 2;;
  --max-wall)    MAXWALL="$2"; FOREVER=0; shift 2;;
  --replan)      REPLAN="$2"; shift 2;;
  --poll)        POLL="$2"; shift 2;;
  --base-branch) BASE_BRANCH="$2"; shift 2;;
  --keep-nightshift) KEEP_NIGHTSHIFT=1; shift;;
  --test-cmd)    TEST_CMD="$2"; shift 2;;
  --no-gate)     NO_GATE=1; shift;;
  --strict-gate) STRICT=1; shift;;
  --retries)     RETRIES="$2"; shift 2;;
  --notify)      NOTIFY=1; shift;;
  *) echo "orch: unknown arg $1" >&2; exit 1;;
esac; done

STAGE_WT="$BASE-stage"; VENV="$BASE/.nightshift/venv"; RETRYDIR="$BASE/.nightshift/retries"
RULES_FILE="$BASE/.nightshift/drain-rules.txt"   # rules go in a file (read at launch) — NOT inline in the shell command, so quotes/newlines in the text can't break the launch

[ "$MODE" = tmux ] && { command -v tmux >/dev/null 2>&1 || { echo "orch: tmux not found (required for --tmux mode)" >&2; exit 1; }; }
command -v claude >/dev/null 2>&1 || { echo "orch: claude not found" >&2; exit 1; }
[ -n "$BASE" ] || { echo "orch: --repo is required" >&2; exit 1; }
BASE="$(cd "$BASE" && pwd)" || { echo "orch: --repo not a dir" >&2; exit 1; }

# Worker prompts are extracted into prompts/ (installed alongside this script, or
# ../prompts in the repo) — this orchestrator is logic, not 2KB of embedded prose.
PROMPTS="$SELF_DIR/prompts"; [ -d "$PROMPTS" ] || PROMPTS="$SELF_DIR/../prompts"
NIGHTSHIFT_RULES="$(cat "$PROMPTS/nightshift-drain.txt")"

PLAN_RULES="$(cat "$PROMPTS/nightshift-plan.txt")"

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
# Any worker showing a real USAGE LIMIT (not a transient 529) → back off to a 5-min cadence.
usage_limited() {
  local i
  for i in $(seq 0 $((N - 1))); do
    printf '%s' "$(pane "${SESS[$i]}")" | grep -qiE "usage limit|limit reached|resets? (at|in)|approaching .*limit|out of .*credit|quota" && return 0
  done
  return 1
}

send_prompt() {  # robust submit: clear stale menu/input, type, then Enter
  tmux send-keys -t "$1" Escape 2>/dev/null     # dismiss any open slash-command autocomplete
  tmux send-keys -t "$1" C-u    2>/dev/null     # clear the input line (no leftover "continue" to concat)
  tmux send-keys -t "$1" -l "$2"
  sleep 0.5                                      # let the slash menu populate so Enter runs the command
  tmux send-keys -t "$1" Enter
}

spawn_worker() {  # $1 index
  local i="$1" wt sess marker
  wt="$BASE-w$i"; sess="ns-w$i"; marker="$wt/.nightshift/w$i.turns"
  git -C "$BASE" worktree prune 2>/dev/null
  git -C "$BASE" fetch -q origin 2>/dev/null
  [ -d "$wt" ] || git -C "$BASE" worktree add -f --detach "$wt" origin/nightshift >/dev/null 2>&1
  mkdir -p "$wt/.nightshift"
  tmux kill-session -t "$sess" 2>/dev/null; sleep 1   # let the killed pane's processes go
  tmux new-session -d -s "$sess" -c "$wt" -x 200 -y 50
  local launch="claude --dangerously-skip-permissions --disallowedTools AskUserQuestion --append-system-prompt \"\$(cat '$RULES_FILE')\""
  # Wait for the (zsh) shell to finish starting before typing — sending keystrokes
  # before the prompt is ready mangles the launch (the respawn-into-garbage bug).
  sleep 2
  tmux send-keys -t "$sess" -l "export NIGHTSHIFT_MARKER='$marker'; $launch"; tmux send-keys -t "$sess" Enter
  # Confirm claude actually came up; if the shell was slow, Ctrl-C + relaunch once.
  local r ok=0
  for r in $(seq 1 15); do
    tmux capture-pane -t "$sess" -p 2>/dev/null | grep -q "bypass permissions" && { ok=1; break; }
    sleep 1
  done
  if [ "$ok" = 0 ]; then
    tmux send-keys -t "$sess" C-c 2>/dev/null; sleep 1
    tmux send-keys -t "$sess" -l "export NIGHTSHIFT_MARKER='$marker'; $launch"; tmux send-keys -t "$sess" Enter
    echo "orch: worker $i launch retried (shell was slow to ready)"
  fi
  WT[$i]="$wt"; SESS[$i]="$sess"; MARKER[$i]="$marker"
  BASE_CNT[$i]=0; LASTHASH[$i]=""; LASTCHG[$i]=$(date +%s); STATE[$i]="booting"; PROMPT_SENT[$i]=""
  echo "orch: spawned worker $i ($sess) in $wt"
}
mcount() { [ -f "${MARKER[$1]}" ] && wc -l < "${MARKER[$1]}" | tr -d ' ' || echo 0; }

# ---- headless backend (claude -p) — the DEFAULT ------------------------------
# One `claude -p` per turn per worker. No tmux, no Stop-hook marker, no pane
# scraping: the process is the turn (exit = turn boundary, exit code/log = result).
# Workers still each get their own worktree off origin/nightshift.
spawn_worker_headless() {  # $1 index — ensure the worktree exists; turns run on demand
  local i="$1" wt; wt="$BASE-w$i"
  git -C "$BASE" worktree prune 2>/dev/null
  git -C "$BASE" fetch -q origin 2>/dev/null
  [ -d "$wt" ] || git -C "$BASE" worktree add -f --detach "$wt" origin/nightshift >/dev/null 2>&1
  mkdir -p "$wt/.nightshift"
  WT[$i]="$wt"; WTLOG[$i]="$wt/.nightshift/turn.log"; WTPID[$i]=""
  STATE[$i]="idle"; LASTCHG[$i]=$(date +%s); PROMPT_SENT[$i]=""
  echo "orch: worker $i worktree ready ($wt) [headless]"
}
run_headless_turn() {  # $1 index ; $2 prompt — launch one claude -p turn in the background
  local i="$1" prompt="$2" wt="${WT[$i]}" log="${WTLOG[$i]}"
  # Start every turn from a CLEAN origin/nightshift. A reused worktree otherwise keeps the
  # prior turn's branch + any leftover/uncommitted files (e.g. after a mid-turn `nightshift
  # stop`), which leak stale work into the next claim and cause same-file collisions. Each
  # turn branches off nightshift fresh anyway, so this reset is safe. `clean -fd` (no -x)
  # preserves gitignored paths AND the venv/build dirs listed in .git/info/exclude (set up
  # at boot); it DOES discard other uncommitted work, which is intentional (turns are atomic).
  # Drop only THIS worktree's leftover todo/ branch from the prior turn — not all refs/heads/todo.
  # Refs are shared across worktrees, so a blanket sweep could delete another worker's branch
  # while it's transiently detached; scoping to the branch we're leaving keeps refs from piling
  # up (the merge deletes only the origin copy) without reaching into a sibling worker's state.
  local prev; prev="$(git -C "$wt" branch --show-current 2>/dev/null)"
  git -C "$wt" checkout -q --detach origin/nightshift 2>/dev/null   # off the prior todo/ branch so it can be pruned
  git -C "$wt" reset -q --hard origin/nightshift 2>/dev/null
  git -C "$wt" clean -qfd 2>/dev/null
  case "$prev" in todo/*) git -C "$wt" branch -qD "$prev" 2>/dev/null;; esac
  : > "$log"
  # The rules go in --append-system-prompt as a real argument (not typed into a
  # TUI), so quotes/newlines in them can't break anything — the whole reason the
  # headless backend is less hacky than --tmux. `timeout` bounds a runaway turn.
  ( cd "$wt" && exec timeout "$TURN_MAX" claude -p "$prompt" \
       --dangerously-skip-permissions \
       --disallowedTools AskUserQuestion \
       --append-system-prompt "$(cat "$RULES_FILE")" ) >>"$log" 2>&1 &
  WTPID[$i]=$!; PROMPT_SENT[$i]="$prompt"
  # Record the turn PID on disk so a SEPARATE `devbrain nightshift stop` (which has no view of
  # this process's WTPID array) can reap the detached child even after a hard orchestrator
  # kill. Removed when the turn is harvested in hl_step.
  echo "$!" > "$wt/.nightshift/turn.pid" 2>/dev/null
}
hl_step() {  # $1 index — one poll step for a headless worker
  local i="$1" rc br
  if [ -n "${WTPID[$i]}" ]; then
    if kill -0 "${WTPID[$i]}" 2>/dev/null; then STATE[$i]="working"; return; fi   # turn in progress
    wait "${WTPID[$i]}" 2>/dev/null; rc=$?; WTPID[$i]=""; STATE[$i]="idle"
    rm -f "${WT[$i]}/.nightshift/turn.pid" 2>/dev/null
    TURNS_DONE=$((TURNS_DONE + 1))
    echo "orch: worker $i finished a turn rc=$rc (total turns: $TURNS_DONE)"
    # exit code/stdout replace the pane-scrape: a usage limit shows in the log.
    grep -qiE "usage limit|limit reached|out of .*credit|quota|resets? (at|in)" "${WTLOG[$i]}" 2>/dev/null && LIMIT_HIT=1
    if [ "$rc" = 124 ]; then
      # The turn was killed mid-flight (wall cap). Don't try to merge a half-done branch —
      # RELEASE the task it claimed so it returns to `open` instead of stranding `taken`.
      echo "orch: worker $i turn TIMED OUT after ${TURN_MAX}s — discarding its branch + releasing its task"
      release_branch_task "$i"; NOMERGE=$((NOMERGE + 1)); return
    fi
    br="$(git -C "${WT[$i]}" branch --show-current 2>/dev/null)"
    case "$br" in
      todo/*) if merge_to_nightshift "$br" "${br#todo/}"; then NOMERGE=0; else NOMERGE=$((NOMERGE + 1)); fi;;
      *)      NOMERGE=$((NOMERGE + 1));;   # planning / no-branch turn → no merge
    esac
    return   # harvested this poll; assign the next turn on the following poll
  fi
  # idle → decide the next turn (SAME policy as the tmux backend)
  if [ "$STALLED" = 1 ] || [ "$NOMERGE" -ge "$STALL_K" ]; then STATE[$i]="parked"; return; fi
  [ "$BASE_RED" = 1 ] && [ "$BR_ASSIGNED" -ge 1 ] && { STATE[$i]="parked"; return; }   # red base → feed one fixer only
  if [ "$oc" -gt 0 ]; then
    run_headless_turn "$i" "/continue"; STATE[$i]="working"; BR_ASSIGNED=$((BR_ASSIGNED + 1))
    echo "orch: worker $i started /continue (open=$oc)"
  elif [ $((now - PLANNED_LAST)) -gt "$REPLAN" ]; then
    echo "orch: queue empty — worker $i planning (replenish)"
    run_headless_turn "$i" "$PLAN_RULES"; STATE[$i]="working"; PLANNED_LAST=$now
  else
    STATE[$i]="parked"
  fi
}

release_branch_task() {  # $1 index — restore as if this worker's turn never ran:
  # wipe the half-done branch FIRST (local + the pushed copy on origin), reset the worktree
  # to a pristine origin/nightshift, and ONLY THEN release the task back to `open`. Ordering
  # matters: if we reopened the task while origin/todo/<id> still held partial work, reconcile()
  # would pick that branch up and merge the timed-out work. So if the remote branch can't be
  # deleted (network/auth), we HOLD the task instead of reopening — reconcile skips held tasks,
  # so the partial work can never ship. Used on timeout / shutdown / hang-restart.
  local wt="${WT[$1]}" b id; b="$(git -C "$wt" branch --show-current 2>/dev/null)"
  case "$b" in todo/*) id="${b#todo/}";; *) return 0;; esac
  git -C "$wt" checkout -q --detach origin/nightshift 2>/dev/null   # leave the branch so it can be deleted
  git -C "$wt" reset -q --hard origin/nightshift 2>/dev/null
  git -C "$wt" clean -qfd 2>/dev/null
  git -C "$wt" branch -qD "$b" 2>/dev/null                          # local ref
  git -C "$BASE" push -q origin --delete "$b" 2>/dev/null           # pushed copy, if the turn got that far
  # Confirm origin/<b> is actually gone before reopening; ls-remote exits non-zero when absent.
  if git -C "$BASE" ls-remote --exit-code --heads origin "$b" >/dev/null 2>&1; then
    ( cd "$BASE" && "$TODO" hold "$id" "dead turn: could not delete origin/$b — partial work may remain; release after deleting the branch" 2>/dev/null )
    echo "orch: ⚠ origin/$b survived deletion — HELD $id so reconcile won't merge the partial branch"
    notify "needs your review" "$id: couldn't delete partial branch origin/$b"
  else
    ( cd "$BASE" && "$TODO" release "$id" 2>/dev/null ) && echo "orch: released $id"
    echo "orch: discarded partial branch $b (local+remote); worktree restored to origin/nightshift"
  fi
}

# Clean shutdown: the headless backend launches each turn as a detached `claude -p`;
# without this, stopping the orchestrator (Ctrl-C / cap hit / kill) leaves those children
# running and their tasks stranded `taken`. Reap every in-flight turn and release its task.
# Headless-only by design: tmux sessions are deliberately left alive for inspection (the
# original behavior; `devbrain nightshift stop` reaps them), and any stranded tmux claim is freed
# by the stale-claim lease on restart — so cleanup doesn't touch tmux.
CLEANED=0
cleanup() {
  trap - EXIT INT TERM; [ "$CLEANED" = 1 ] && return; CLEANED=1
  [ "$MODE" = headless ] || return 0
  echo "orch: shutting down — reaping in-flight turns + releasing their claimed tasks"
  local i p
  for i in $(seq 0 $((N - 1))); do
    p="${WTPID[$i]:-}"; { [ -n "$p" ] && kill -0 "$p" 2>/dev/null; } || continue
    pkill -P "$p" 2>/dev/null; kill "$p" 2>/dev/null   # timeout forwards TERM to claude; -P sweeps any straggler
    wait "$p" 2>/dev/null                              # let the turn's git fully exit before we touch its worktree
    release_branch_task "$i"                            # kill + reap FIRST, then wipe — no race on a still-writing turn
    rm -f "${WT[$i]}/.nightshift/turn.pid" 2>/dev/null
  done
}

# Ensure the turn-marker Stop hook is installed globally (guarded by NIGHTSHIFT_MARKER,
# so it only fires for workers). Global — NOT per-worktree — because /continue's
# `git stash -u` would stash a worktree-local .claude/settings.json mid-turn.
ensure_marker_hook() {
  local hook="$HOME/.claude/hooks/devbrain-turn-marker.sh" src=""
  for c in "$SELF_DIR/../hooks/turn-marker.sh" "$SELF_DIR/turn-marker.sh"; do [ -f "$c" ] && { src="$c"; break; }; done
  mkdir -p "$HOME/.claude/hooks"
  [ -n "$src" ] && { cp "$src" "$hook"; chmod +x "$hook"; }
  [ -f "$hook" ] || { echo "orch: WARN turn-marker.sh not found — markers will not fire"; return; }
  local lib="$HOME/.claude/hooks/devbrain_lib.py"
  [ -f "$lib" ] || lib="$SELF_DIR/../hooks/devbrain_lib.py"
  command -v python3 >/dev/null 2>&1 && [ -f "$lib" ] || { echo "orch: WARN python3/devbrain_lib.py missing — register Stop hook manually: $hook"; return; }
  local set="$HOME/.claude/settings.json"; [ -f "$set" ] || echo '{}' > "$set"
  if ! grep -q "devbrain-turn-marker" "$set" 2>/dev/null; then
    python3 "$lib" register-hook "$set" Stop "" "$hook" \
      && echo "orch: registered turn-marker Stop hook globally"
  fi
}

# ---- nightshift + green-gate + serialized automerge -----------------------------
# Pick a python that satisfies the project's requires-python — bare `python3` may be
# OLDER than the project needs (e.g. macOS system 3.9 vs requires-python ">=3.11"), in
# which case `pip install -e .` fails and the gate is structurally incapable of ever
# passing. Echoes a usable interpreter, or "" when requires-python is set but no
# installed python satisfies it (the caller fails fast on that — see setup_nightshift).
pick_gate_python() {
  local req lo hi c
  req="$(grep -m1 -E '^[[:space:]]*requires-python' "$BASE/pyproject.toml" 2>/dev/null)"
  # Honor BOTH bounds (e.g. ">=3.11,<3.13") so we don't pick 3.13 and then fail the
  # preflight while 3.11/3.12 sit installed. Only 3.x is in play for requires-python;
  # a `<4.0`-style cap matches nothing here and correctly imposes no ceiling.
  local cap
  # Floor markers: `>=`/`>` (range), `~=` (compatible-release, ≈ `>=`), and `==` (exact pin).
  lo="$(printf '%s' "$req" | grep -oE '(>=?|~=|==)[[:space:]]*3\.[0-9]+' | grep -oE '[0-9]+$' | head -1)"
  cap="$(printf '%s' "$req" | grep -oE '<=?[[:space:]]*3\.[0-9]+' | head -1)"   # exclusive `<3.x` or inclusive `<=3.x`
  hi="$(printf '%s' "$cap" | grep -oE '[0-9]+$')"
  case "$cap" in *'<='*) hi=$((hi + 1));; esac   # inclusive cap → exclusive ceiling is one higher
  # `==3.x` pins a single minor (no `<` clause), so its ceiling is the floor + 1. `~=3.x`
  # is `>=3.x` with no real 3.x ceiling, so leave hi alone for it.
  case "$req" in *'=='*) [ -n "$lo" ] && hi="$((lo + 1))";; esac
  # Discover EVERY installed python3.X (lowest minor first) so we honor any floor or cap,
  # including caps below 3.11 like ">=3.8,<3.11"; generic `python3` is the last resort.
  # Lowest-first picks the interpreter nearest the declared floor, not the newest one.
  local cands
  cands="$(compgen -c 'python3.' 2>/dev/null | grep -E '^python3\.[0-9]+$' | sort -t. -k2 -n -u)"
  for c in $cands python3; do
    command -v "$c" >/dev/null 2>&1 || continue
    "$c" -c "import sys; m=sys.version_info[1]; sys.exit(0 if m>=${lo:-0} and m<${hi:-99} else 1)" 2>/dev/null \
      && { echo "$c"; return 0; }
  done
  echo ""   # requires-python set but unsatisfiable by any installed interpreter
}

setup_nightshift() {
  git -C "$BASE" fetch -q origin
  if [ "$KEEP_NIGHTSHIFT" = 1 ] && git -C "$BASE" ls-remote --exit-code --heads origin nightshift >/dev/null 2>&1; then
    echo "orch: keeping existing origin/nightshift"
  else
    git -C "$BASE" branch -f nightshift "origin/$BASE_BRANCH"
    git -C "$BASE" push -f -q origin nightshift
    echo "orch: nightshift reset to origin/$BASE_BRANCH"
  fi
  git -C "$BASE" worktree prune 2>/dev/null
  [ -d "$STAGE_WT" ] || git -C "$BASE" worktree add -f "$STAGE_WT" nightshift >/dev/null 2>&1
  git -C "$STAGE_WT" checkout -q nightshift 2>/dev/null; git -C "$STAGE_WT" reset -q --hard origin/nightshift
  mkdir -p "$RETRYDIR"
  # Exclude the state dir + common ephemeral build/venv dirs in ALL worktrees (shared
  # info/exclude) so /continue's `git add -A` never commits them AND the per-turn
  # `git clean -fd` (run_headless_turn) PRESERVES a worker's venv/build cache instead of
  # wiping it every turn. (Other uncommitted work is still discarded by the reset — that
  # is intentional: turns are atomic and branch off origin/nightshift fresh.)
  local excl="$BASE/.git/info/exclude" _p
  for _p in '.nightshift/' '.venv/' 'venv/' 'node_modules/' '__pycache__/'; do
    [ -f "$excl" ] && ! grep -qxF "$_p" "$excl" 2>/dev/null && echo "$_p" >> "$excl"
  done
  # Default the gate to `make test` for a Makefile-driven (non-pytest) project like this
  # one: without this the pytest gate collects nothing -> "inconclusive" -> a RED bash
  # suite slips past base-health AND every merge gate (caught only later in GitHub CI).
  if [ "$NO_GATE" != 1 ] && [ -z "$TEST_CMD" ] && [ ! -f "$STAGE_WT/pyproject.toml" ] \
     && [ -f "$STAGE_WT/Makefile" ] && grep -qE '^test:' "$STAGE_WT/Makefile" 2>/dev/null; then
    # Skip the slow docker clean-room + browser-dogfood tests in the PER-TURN gate so a
    # cold docker pull / playwright run can't blow the 600s timeout into a false RED base.
    # GitHub CI runs the FULL suite (incl. both) on every PR, so coverage is not lost.
    TEST_CMD="DEVBRAIN_TEST_SKIP='docker|dogfood' make test"
    echo "orch: gate = 'make test' (fast: skips docker+dogfood; CI runs the full set) — at base-health and before every merge"
  fi
  if [ "$NO_GATE" != 1 ] && [ -z "$TEST_CMD" ]; then
    GATE_PY="$(pick_gate_python)"
    if [ -z "$GATE_PY" ]; then
      echo "orch: FATAL — no installed python satisfies $(grep -m1 requires-python "$BASE/pyproject.toml" 2>/dev/null | tr -s ' ') for the green-gate." >&2
      echo "orch:   install an interpreter matching that requirement, or pass --test-cmd to pin your own gate, or --no-gate to skip it." >&2
      exit 1
    fi
    echo "orch: green-gate interpreter: $GATE_PY ($("$GATE_PY" --version 2>&1))"
    # Upgrade pip/setuptools/wheel FIRST — the venv default pip can be too old to do
    # PEP 660 editable installs from a pyproject-only project, which silently breaks
    # `pip install -e .` and leaves the package + its deps uninstalled (rc=2 gate).
    "$GATE_PY" -m venv "$VENV" >/dev/null 2>&1 \
      && "$VENV/bin/pip" install -q --upgrade pip setuptools wheel >/dev/null 2>&1 \
      && "$VENV/bin/pip" install -q pytest >/dev/null 2>&1 \
      && echo "orch: green-gate venv ready (pytest)" || echo "orch: WARN gate venv unavailable — gate may be inconclusive"
    # Fail fast on a structurally-impossible gate: if the gate venv can't even install
    # the BASE (origin/nightshift), it can never pass, so EVERY merge would be rejected.
    # Better to die at second 0 with an actionable message than discover it at hour 8.
    # Only meaningful for a packaged project — skip when there's no pyproject to install.
    if [ -f "$BASE/pyproject.toml" ] && [ -x "$VENV/bin/python" ]; then
      git -C "$STAGE_WT" reset -q --hard origin/nightshift 2>/dev/null
      if ! ( cd "$STAGE_WT" && { "$VENV/bin/pip" install -q -e ".[dev]" >/dev/null 2>&1 || "$VENV/bin/pip" install -q -e . >/dev/null 2>&1; } ); then
        echo "orch: FATAL — green-gate ($GATE_PY) cannot install origin/nightshift ('pip install -e .' failed)." >&2
        echo "orch:   the gate would reject every merge. Fix the env (interpreter/deps), or pass --test-cmd / --no-gate." >&2
        exit 1
      fi
      echo "orch: green-gate preflight OK — origin/nightshift installs under $GATE_PY"
    fi
  fi
}

run_gate() {  # $1 dir → 0 pass · 1 fail · 2 inconclusive ; sets GATE_DETAIL on fail, GATE_IMPORT_ERROR on collection/import-only
  local dir="$1" out rc; GATE_DETAIL=""; GATE_IMPORT_ERROR=0
  if [ -n "$TEST_CMD" ]; then
    out="$( cd "$dir" && timeout 600 bash -c "$TEST_CMD" 2>&1 )"; rc=$?
    [ "$rc" -eq 0 ] && { echo "  gate PASS: $TEST_CMD"; return 0; }
    GATE_DETAIL="$(printf '%s' "$out" | tail -3 | tr '\n' ' ' | cut -c1-240)"
    echo "  gate FAIL ($TEST_CMD): $GATE_DETAIL"; return 1
  fi
  [ -x "$VENV/bin/python" ] || { echo "  gate inconclusive (no venv)"; return 2; }
  # Install the package + its declared deps (dev extras if present) so pytest can
  # actually import it. If this fails the suite won't collect → rc=2 → FAIL below,
  # which is correct for MERGE admission: a branch that can't be installed/imported
  # must not merge. (base_gate reads GATE_IMPORT_ERROR to NOT treat this as a red base.)
  ( cd "$dir" && { "$VENV/bin/pip" install -q -e ".[dev]" >/dev/null 2>&1 || "$VENV/bin/pip" install -q -e . >/dev/null 2>&1; } ) || true
  out="$( cd "$dir" && timeout 600 "$VENV/bin/python" -m pytest -q 2>&1 )"; rc=$?
  GATE_DETAIL="$(printf '%s' "$out" | grep -E '^(FAILED|ERROR)' | head -4 | tr '\n' ' ')"
  [ -n "$GATE_DETAIL" ] || GATE_DETAIL="$(printf '%s' "$out" | tail -3 | tr '\n' ' ' | cut -c1-240)"
  # pytest prints FAILED for a real assertion failure, ERROR for a collection/import
  # failure. "ERROR but no FAILED" means the suite never ran — an environment problem,
  # not broken code. Flag it so base_gate can tell the two apart.
  if printf '%s' "$out" | grep -q '^ERROR' && ! printf '%s' "$out" | grep -q '^FAILED'; then
    GATE_IMPORT_ERROR=1
  fi
  case "$rc" in
    0) echo "  gate PASS (pytest)"; return 0;;
    5) echo "  gate inconclusive (no tests collected)"; return 2;;
    1) echo "  gate FAIL (pytest): $GATE_DETAIL"; return 1;;
    2) GATE_IMPORT_ERROR=1; echo "  gate FAIL (collection/import error): $GATE_DETAIL"; return 1;;
    *) echo "  gate inconclusive (pytest rc=$rc)"; return 2;;
  esac
}

notify() {  # $1 title-suffix · $2 message — native macOS toast (best-effort)
  [ "$NOTIFY" = 1 ] || return 0   # off by default (enable with --notify)
  command -v osascript >/dev/null 2>&1 && \
    osascript -e "display notification \"$2\" with title \"nightshift\" subtitle \"$1\"" 2>/dev/null || true
}
requeue() {  # $1 id ; $2 why — release back to open, or PARK for the human after $RETRIES
  local id="$1" why="${2:-could not merge}" f="$RETRYDIR/$id" n; n=$(cat "$f" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" > "$f"
  ( cd "$BASE" && "$TODO" note "$id" "attempt $n — $why" 2>/dev/null )   # feedback the next worker reads via `todo show`
  if [ "$n" -le "$RETRIES" ]; then ( cd "$BASE" && "$TODO" release "$id" 2>/dev/null ); echo "  requeued $id (attempt $n/$RETRIES): $why"
  else
    ( cd "$BASE" && "$TODO" hold "$id" "$why (after $RETRIES attempts)" 2>/dev/null )
    echo "  ⚠ $id held after ${n} attempts — $why (needs you)"
    notify "needs your review" "$id: $why"
  fi
}

task_status() { ( cd "$BASE" && "$TODO" show "$1" 2>/dev/null ) | sed -n 's/^status:[[:space:]]*//p' | head -1; }

# Once a todo/<id> branch is in nightshift its work is preserved by the merge — the branch
# is spent. Delete it (origin copy + any local ref) so todo/* branches don't accumulate on
# every turn. Best-effort: a local ref still checked out in a worker's worktree won't delete
# here, but that worktree resets to origin/nightshift on its next turn, so it doesn't linger.
drop_spent_branch() {  # $1 branch (todo/<id>)
  git -C "$BASE" push -q origin --delete "$1" 2>/dev/null   # the pushed copy ls-remote sees + what piles up
  git -C "$BASE" branch -qD "$1" 2>/dev/null                # local copy, if not checked out anywhere
}

# Serialized by construction: only the single orchestrator loop calls this.
# Returns: 0 NEW merge · 2 already-in-nightshift (no-op) · 1 conflict/fail/not-pushed.
merge_to_nightshift() {  # $1 branch (todo/<id>) ; $2 task id
  local br="$1" id="$2" verdict
  git -C "$BASE" ls-remote --exit-code --heads origin "$br" >/dev/null 2>&1 || { echo "orch:   $br not pushed — requeue"; requeue "$id" "worker turn produced no pushed branch"; return 1; }
  git -C "$BASE" fetch -q origin
  # Already in nightshift (e.g. a stale branch from a no-op turn) → ensure done, never
  # re-merge. This kills the re-merge churn (was 60×) AND makes reconcile cheap.
  if git -C "$BASE" merge-base --is-ancestor "origin/$br" origin/nightshift 2>/dev/null; then
    ( cd "$BASE" && "$TODO" done "$id" 2>/dev/null ); drop_spent_branch "$br"; return 2
  fi
  git -C "$STAGE_WT" checkout -q nightshift 2>/dev/null; git -C "$STAGE_WT" reset -q --hard origin/nightshift
  if ! git -C "$STAGE_WT" merge --no-ff -q -m "nightshift: merge $br into nightshift" "origin/$br" >/dev/null 2>&1; then
    local cf; cf="$(git -C "$STAGE_WT" diff --name-only --diff-filter=U 2>/dev/null | tr '\n' ' ')"
    git -C "$STAGE_WT" merge --abort 2>/dev/null
    echo "orch: ✗ $br CONFLICTS with nightshift ($cf)"; requeue "$id" "merge conflict with nightshift in: ${cf:-?} — rebuild on current origin/nightshift and resolve"; return 1
  fi
  if [ "$NO_GATE" = 1 ]; then verdict=0; else run_gate "$STAGE_WT"; verdict=$?; fi
  if [ "$verdict" -eq 0 ] || { [ "$verdict" -eq 2 ] && [ "$STRICT" != 1 ]; }; then
    if git -C "$STAGE_WT" push -q origin nightshift; then
      ( cd "$BASE" && "$TODO" done "$id" 2>/dev/null ); drop_spent_branch "$br"; echo "orch: ✓ merged $br → nightshift; task $id done"; return 0
    else
      git -C "$STAGE_WT" reset -q --hard origin/nightshift
      echo "orch: ✗ push of nightshift failed for $br — requeue"; requeue "$id" "git push to nightshift failed"; return 1
    fi
  else
    git -C "$STAGE_WT" reset -q --hard origin/nightshift
    echo "orch: ✗ $br failed gate — not merged"; requeue "$id" "gate failed: ${GATE_DETAIL:-tests failed} — reproduce by merging your branch onto origin/nightshift and running the test suite"; return 1
  fi
}

# Self-heal: merge any pushed todo/* branch stranded out of nightshift — e.g. a turn
# whose merge was never triggered (the PR #11 case). Idempotent and cheap: branches
# already in nightshift are skipped by the ancestor check before any heavy work.
reconcile() {
  git -C "$BASE" fetch -q origin 2>/dev/null
  local line br id st
  while IFS= read -r line; do
    br="${line##*refs/heads/}"; [ -n "$br" ] || continue
    id="${br#todo/}"; st="$(task_status "$id")"
    # Already in nightshift? Then the work shipped — mark it done so a worker never re-does an
    # already-merged task (the "blind queue trust" 0011 case), then skip. Was a bare
    # `continue` that left such tasks open and re-claimable.
    if git -C "$BASE" merge-base --is-ancestor "origin/$br" origin/nightshift 2>/dev/null; then
      case "$st" in done|held) ;; *) ( cd "$BASE" && "$TODO" done "$id" 2>/dev/null ) && echo "orch: ✓ $br already in nightshift — marked $id done (was ${st:-?})";; esac
      continue
    fi
    { [ "$st" = "held" ] || [ "$st" = "done" ]; } && continue
    # already gave up on this branch (hit the retry cap) — don't reconcile-loop a
    # stale branch that keeps conflicting (this was spinning 200-300× overnight).
    [ "$(cat "$RETRYDIR/$id" 2>/dev/null || echo 0)" -ge "$RETRIES" ] && continue
    echo "orch: ♻ reconcile — $br is pushed but not in nightshift; merging"
    merge_to_nightshift "$br" "$id"
  done < <(git -C "$BASE" ls-remote --heads origin 'todo/*' 2>/dev/null)
}

# A worker that dies mid-turn leaves its task stuck `taken` with no heartbeat, so
# `next` never hands it out again and it silently drops out of the queue. Reclaim any
# `taken` task that (a) is NOT held by a live worker turn and (b) whose claim is older
# than CLAIM_TTL — releasing it back to `open` so a healthy worker can pick it up.
is_worker_alive() {  # $1 index
  if [ "$MODE" = headless ]; then local p="${WTPID[$1]:-}"; [ -n "$p" ] && kill -0 "$p" 2>/dev/null
  else tmux has-session -t "${SESS[$1]:-}" 2>/dev/null; fi
}
epoch_of() {  # $1 ISO-8601 UTC (2026-06-19T14:05:44Z) → epoch seconds, or 0 (portable: BSD then GNU date)
  date -j -u -f '%Y-%m-%dT%H:%M:%SZ' "$1" +%s 2>/dev/null || date -u -d "$1" +%s 2>/dev/null || echo 0
}
reclaim_stale_claims() {
  local i b id ca age now_s active=" "; now_s=$(date +%s)
  for i in $(seq 0 $((N - 1))); do                       # tasks held by a LIVE worker turn = genuinely in progress
    is_worker_alive "$i" || continue
    b="$(git -C "${WT[$i]:-/nonexistent}" branch --show-current 2>/dev/null)"
    case "$b" in todo/*) active="${active}${b#todo/} ";; esac
  done
  for id in $( ( cd "$BASE" && "$TODO" list taken 2>/dev/null ) | grep -oE '[0-9]{4}-[a-z0-9-]+' ); do
    case "$active" in *" $id "*) continue;; esac          # a live turn owns it — leave it alone
    ca="$( ( cd "$BASE" && "$TODO" show "$id" 2>/dev/null ) | sed -n 's/^claimed_at:[[:space:]]*//p' | head -1 )"
    age=$(( now_s - $(epoch_of "$ca") ))                  # no/garbage claimed_at → epoch 0 → huge age → reclaim
    if [ "$age" -ge "$CLAIM_TTL" ]; then
      ( cd "$BASE" && "$TODO" release "$id" 2>/dev/null ) && echo "orch: ♻ reclaimed stale claim $id (taken, no live worker, lease > ${CLAIM_TTL}s)"
    fi
  done
}

# Base-health gate: is the base (origin/nightshift) green ON ITS OWN? If red, every task
# merge is doomed, so we auto-file a top-priority fix task instead of churning /continue.
base_gate() {  # 0 = nightshift green/inconclusive · 1 = nightshift RED (a genuine test FAILED)
  [ "$NO_GATE" = 1 ] && return 0
  git -C "$BASE" fetch -q origin 2>/dev/null
  git -C "$STAGE_WT" checkout -q nightshift 2>/dev/null; git -C "$STAGE_WT" reset -q --hard origin/nightshift
  run_gate "$STAGE_WT"; local rc=$?
  # RED only on a genuine test FAILED. "Couldn't build/import the base" (a collection/
  # import error) is an OPERATOR problem on a CI-green base — not broken code — so don't
  # stop the world and file a P99 "fix the tests" task that hijacks the fleet chasing a
  # phantom. Treat it as inconclusive (stay green) and surface it for the human instead.
  if [ "$rc" = 1 ] && [ "${GATE_IMPORT_ERROR:-0}" = 1 ]; then
    echo "orch: ⚠ base gate could not build/import origin/nightshift (environment, not code) — NOT flagging RED. Detail: ${GATE_DETAIL:-?}"
    notify "base gate env issue" "couldn't build/import nightshift — check the gate interpreter/deps"
    return 0
  fi
  case "$rc" in 0|2) return 0;; *) return 1;; esac
}
ensure_base_fix_task() {  # $1 = failing detail — file ONE high-priority fix task (deduped)
  local title="NIGHTSHIFT IS RED — fix the failing test(s) to unblock all merges"
  # Dedup on the EXACT title in a still-actionable state (anything but done/held), not a
  # loose "nightshift is red" substring that any unrelated task mentioning the phrase trips.
  ( cd "$BASE" && "$TODO" list all 2>/dev/null ) | grep -F "$title" | grep -Eqv 'done|held' && return 0
  # Pin the gate's own interpreter in the repro hint — a bare `python`/`python3` may be
  # older than requires-python, so a worker following the hint reproduces the false
  # failure (the env bug) rather than the real one. ${GATE_PY} is the eligible one we picked.
  local py="${GATE_PY:-python3}"
  ( cd "$BASE" && "$TODO" add "$title" -p 99 \
      -b "origin/nightshift fails its OWN test suite, so EVERY task merge fails the gate — the whole fleet is blocked until this is green. Fix the failing test(s) and push nightshift green. Failing: ${1:-?}. Reproduce: checkout nightshift, $py -m pip install -e '.[dev]', $py -m pytest -q." >/dev/null 2>&1 )
  echo "orch: 🩺 nightshift RED → filed priority-99 fix task — ${1:-?}"
}

# ---- boot --------------------------------------------------------------------
# Tests source this file with NIGHTSHIFT_LIB=1 to get the functions above WITHOUT
# launching the fleet (see test-nightshift-gate.sh). No effect on normal execution.
[ "${NIGHTSHIFT_LIB:-0}" = 1 ] && return 0

mkdir -p "$BASE/.nightshift"
printf '%s' "$NIGHTSHIFT_RULES" > "$RULES_FILE"   # workers read the rules from here at launch
exec > >(tee -a "$BASE/.nightshift/orchestrator.log") 2>&1   # stable log for the wall pane
echo "orch: starting $N workers on $BASE | mode=$MODE gate=$([ "$NO_GATE" = 1 ] && echo off || echo on)$([ "$MODE" = headless ] && echo " turn-timeout=${TURN_MAX}s" || echo " hang=${HANG}s")"
[ "$MODE" = tmux ] && ensure_marker_hook   # the Stop-hook marker is only needed for the tmux backend
setup_nightshift        # nightshift must exist before workers branch off it
declare -a WT SESS MARKER BASE_CNT LASTHASH LASTCHG STATE PROMPT_SENT WTLOG WTPID
# Reap in-flight turns + release their tasks on any exit. INT/TERM must EXIT after
# cleanup — returning from the handler would just resume the main loop (so `nightshift
# stop`'s SIGTERM would reap turns but leave the orchestrator running). cleanup is
# idempotent (CLEANED guard), so the exit re-firing the EXIT trap is a harmless no-op.
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM
for i in $(seq 0 $((N-1))); do
  if [ "$MODE" = headless ]; then spawn_worker_headless "$i"; else spawn_worker "$i"; fi
done
[ "$MODE" = tmux ] && echo "orch: workers booting; watch any with: tmux attach -t ns-w0"

START=$(date +%s); TURNS_DONE=0; PLANNED_LAST=0; NOMERGE=0; STALLED=0; LOOPS=0; BASE_RED=0; BR_ASSIGNED=0; LIMIT_HIT=0
reconcile   # self-heal any branch stranded out of nightshift from a prior run (e.g. PR #11)
reclaim_stale_claims   # free tasks stranded `taken` by a worker that died in a prior run
if ! base_gate; then BASE_RED=1; ensure_base_fix_task "$GATE_DETAIL"; fi   # don't build on a red base
[ "$FOREVER" = 1 ] && echo "orch: running FOREVER — respawns dead/idle workers, replans every ${REPLAN}s; stop with ostop/Ctrl-C"

# ---- the orchestration loop --------------------------------------------------
while :; do
  now=$(date +%s)
  [ "$MAXWALL"  -gt 0 ] && [ $((now - START)) -ge "$MAXWALL" ] && { echo "orch: wall-clock cap hit"; break; }
  [ "$MAXTURNS" -gt 0 ] && [ "$TURNS_DONE" -ge "$MAXTURNS" ]   && { echo "orch: max-turns cap hit"; break; }

  oc="$(open_count)"
  [ "$STALLED" = 1 ] && [ "$oc" -gt 0 ] && { echo "orch: ▶ resuming — $oc open task(s) available"; STALLED=0; NOMERGE=0; }
  LOOPS=$((LOOPS + 1))
  if [ $((LOOPS % RECON_EVERY)) -eq 0 ]; then
    reconcile
    reclaim_stale_claims   # periodically free tasks stranded `taken` by a dead worker
    if base_gate; then [ "$BASE_RED" = 1 ] && echo "orch: ✅ nightshift green again — resuming full fleet"; BASE_RED=0
    else BASE_RED=1; ensure_base_fix_task "$GATE_DETAIL"; fi
  fi
  BR_ASSIGNED=0   # while BASE_RED, only one worker is fed per cycle (funnel to the fix, no churn)
  for i in $(seq 0 $((N-1))); do
    if [ "$MODE" = headless ]; then
      [ -d "${WT[$i]:-}" ] || spawn_worker_headless "$i"   # re-create a deleted worktree
      hl_step "$i"
      continue
    fi
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
      # gate + merge the work this turn produced; track stall (no NEW merge).
      br="$(git -C "${WT[$i]}" branch --show-current 2>/dev/null)"
      case "$br" in
        todo/*) if merge_to_nightshift "$br" "${br#todo/}"; then NOMERGE=0; else NOMERGE=$((NOMERGE + 1)); fi;;
        *)      NOMERGE=$((NOMERGE + 1));;   # planning / no-branch turn → no merge
      esac
    fi

    if is_idle "$s"; then
      # stalled (or about to be) → don't hand out more work; the fleet goes quiet
      if [ "$STALLED" = 1 ] || [ "$NOMERGE" -ge "$STALL_K" ]; then STATE[$i]="parked"; continue; fi
      if [ "${STATE[$i]}" = "assigned" ]; then
        # idle but marker didn't advance — could be an API error OR the turn just hasn't
        # started yet. Wait out RESEND_GRACE so we don't double-send during startup (the
        # /continue spam). If a usage limit, the loop's 5-min backoff paces this.
        [ $((now - ${LASTCHG[$i]})) -lt "$RESEND_GRACE" ] && continue
        if is_stuck_error "$s"; then echo "orch: worker $i hit API/limit — resending"; fi
        send_prompt "$s" "${PROMPT_SENT[$i]}"; LASTCHG[$i]=$now; continue
      fi
      # while the base is red, feed only ONE worker per cycle (the priority-99 fix) — no churn
      [ "$BASE_RED" = 1 ] && [ "$BR_ASSIGNED" -ge 1 ] && { STATE[$i]="parked"; continue; }
      # needs an assignment
      if [ "$oc" -gt 0 ]; then
        send_prompt "$s" "/continue"; PROMPT_SENT[$i]="/continue"
        STATE[$i]="assigned"; BASE_CNT[$i]="$cur"; LASTCHG[$i]=$now; BR_ASSIGNED=$((BR_ASSIGNED + 1))
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
          tmux kill-session -t "$s" 2>/dev/null; release_branch_task "$i"   # kill FIRST, then wipe its branch
          spawn_worker "$i"; continue
        fi
      else
        LASTHASH[$i]="$h"; LASTCHG[$i]=$now
      fi
    fi
  done

  # Convergence: K turns with no NEW merge while open work remains → those open
  # tasks are undoable unattended. HOLD them (so `next` stops handing them out),
  # ping the human, and go quiet. The loop keeps running; it resumes the moment a
  # human releases a held task (open>0 → cleared at the top). No --max-turns needed.
  if [ "$STALLED" = 0 ] && [ "$NOMERGE" -ge "$STALL_K" ] && [ "$oc" -gt 0 ]; then
    n=0
    for id in $( ( cd "$BASE" && "$TODO" list 2>/dev/null ) | grep -oE '[0-9]{4}-[a-z0-9-]+' ); do
      ( cd "$BASE" && "$TODO" hold "$id" "stalled: no unattended progress — provision deps or release" >/dev/null 2>&1 ) && n=$((n + 1))
    done
    STALLED=1
    echo "orch: ⚠ STALLED — held $n undoable task(s); going quiet (release one to resume)"
    notify "stalled — needs you" "$n task(s) can't progress unattended; held for review"
  fi
  # On a usage limit, don't hammer it — poll only every LIMIT_BACKOFF (5 min) until
  # it resets; otherwise the normal fast poll. headless reads the flag set when a
  # turn's log showed a limit; tmux scrapes the live panes.
  if [ "$MODE" = headless ]; then
    if [ "$LIMIT_HIT" = 1 ]; then echo "orch: ⏳ usage limit hit — backing off ${LIMIT_BACKOFF}s before the next turn"; LIMIT_HIT=0; sleep "$LIMIT_BACKOFF"; else sleep "$POLL"; fi
  elif usage_limited; then echo "orch: ⏳ usage limit detected — backing off ${LIMIT_BACKOFF}s (ping ~every 5 min until reset)"; sleep "$LIMIT_BACKOFF"
  else sleep "$POLL"; fi
done

echo "orch: done. turns=$TURNS_DONE open=$(open_count) tasks left."
echo "orch: REVIEW WHAT LANDED →  git -C $STAGE_WT diff $BASE_BRANCH...nightshift   (then merge nightshift → $BASE_BRANCH)"
[ "$MODE" = tmux ] && echo "orch: worker sessions left alive: ns-w0 .. ns-w$((N-1))"
