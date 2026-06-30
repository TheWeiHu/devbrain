#!/usr/bin/env bash
# devbrain — gbrain call trace (PostToolUse hook on the Bash tool).
#
# Fires AFTER every Bash tool call. If the command ran a `gbrain` subcommand,
# append ONE JSON line to projects/<project>/gbrain-queries.log:
#   {ts, project, cmd, modes, hits, slugs}
# Deterministic — it fires on every Bash call the agent makes, so it can't be
# bypassed and needs no wrapper to be "remembered." Logging only; never blocks,
# always exit 0. Identity is resolved from cwd, exactly like capture.sh.
#
# It logs DIRECTIONAL signal, not exact per-call terms — and that's deliberate:
#  - `cmd`   — the redacted, whitespace-collapsed, truncated command. Its literal
#              text carries the topic even when the query itself is a `$var`
#              (e.g. the words "recent decisions, open items, conventions").
#  - `modes` — the gbrain subcommands the command used (whitelisted, so "gbrain"
#              inside a string/filename doesn't masquerade as a call).
#  - hits/slugs — what actually surfaced in the output; the strongest "aboutness"
#              signal and reliable regardless of how the query was built.
# Why not exact terms: the hook only has the command TEXT (a `$var` is unexpanded)
# plus the combined output, and parsing arbitrary shell for exact per-call queries
# is unreliable (loops run once in text, quoting, vars). Recovering expanded terms
# would need non-portable shell-trace parsing — deliberately not done. `cmd` +
# `slugs` are reliable and directionally describe what a query was about.

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

# Hook payload is JSON on stdin. Field extraction (the per-harness event shim, keyed by
# $DEVBRAIN_HARNESS) lives in devbrain_lib.py — fail OPEN (exit 0) if python3 is missing.
payload="$(cat 2>/dev/null)" || exit 0
# Raw fast-bail: this fires on EVERY Bash tool call, so skip the whole shim (no
# subprocess) for the ~all payloads that mention no gbrain at all. False positives
# (gbrain only in output) are caught by the whitelist gate below — no spurious log.
case "$payload" in *gbrain*) ;; *) exit 0 ;; esac
command -v python3 >/dev/null 2>&1 || exit 0

_hd="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_hd/hook-common.sh" "$HOME/.claude/hooks/devbrain-hook-common.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
command -v devbrain_read_event >/dev/null 2>&1 || exit 0
devbrain_has_python_lib || exit 0

tool="$(devbrain_read_event tool)"
[ "$tool" = "Bash" ] || exit 0
cmd="$(devbrain_read_event command)"
[ -n "$cmd" ] || exit 0
case "$cmd" in *gbrain*) ;; *) exit 0 ;; esac     # gbrain in command (not just output)

cwd="$(devbrain_read_event cwd)"
[ -n "$cwd" ] || cwd="$PWD"

# tool_response shape varies by version (object with .stdout, or a bare string) — the
# shim coerces whatever it is into printed text so we can parse result lines from it.
out="$(devbrain_read_event tool-response)"

# Identity — shared OFFLINE resolver, so we write to the SAME projects/<owner>__<repo>
# folder capture.sh uses. Installed alongside as devbrain-project-key.sh.
devbrain_source_project_key || exit 0
# --- Route the trace to the repo this call ACTUALLY queried -------------------------
# Identity defaults to the payload cwd (the agent's shell dir). But agents routinely
# run gbrain against another repo's brain — most often by cd-ing into a worktree inline
# from a non-repo parent (`cd <repo> && gbrain …`, or `v="<repo>" (cd "$v" && gbrain …)`),
# whose cwd then has no remote and dumps the trace into the shared "miscellaneous" bucket.
# Two signals recover the real target; either beats cwd, and $DEVBRAIN_PROJECT (explicit
# user override, already honored above) trumps both:
#   1. slug prefix — when the call returned hits, result lines read "[score] owner__repo/page".
#      The prefix names the brain that answered: authoritative (gbrain's OWN output, no
#      command-parsing guesswork), so it wins outright.
#   2. inline `cd` target — writes and zero-hit reads surface no slug; for those, recover
#      the `cd <repo>` the command ran in and use it when it's a hosted <owner>__<repo>.
project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"

if [ -z "${DEVBRAIN_PROJECT:-}" ]; then
  # 1. Slug prefix from the output. Require the owner__repo shape so a slug-less line
  #    (or the miscellaneous bucket's session files) can't hijack routing.
  slug_proj="$(printf '%s\n' "$out" | sed -n 's/^\[[0-9.][0-9.]*\][[:space:]][[:space:]]*\([A-Za-z0-9._-]*__[A-Za-z0-9._-]*\)\/.*/\1/p' | head -1)"
  if [ -n "$slug_proj" ]; then
    project="$slug_proj"
  else
    # 2. Inline `cd` target. Extracted via python kept at statement level, NOT inside a
    #    $(...) — a quoted heredoc with unbalanced quotes/parens confuses that parser —
    #    so it writes the resolved target to a temp file we read back.
    cd_target=""
    _cdf="$(mktemp 2>/dev/null)" || _cdf=""
    if [ -n "$_cdf" ]; then
      CMD="$cmd" python3 - >"$_cdf" 2>/dev/null <<'PY'
import os, re
cmd = os.environ.get("CMD", "")
# Leading var assignments at a command position (start / after ; && || | ( or ws).
vars = {}
for m in re.finditer(r'(?:^|[\s;&|(])([A-Za-z_]\w*)=("(?:[^"\\]|\\.)*"|\'[^\']*\'|[^\s;&|()]*)', cmd):
    v = m.group(2)
    if v[:1] in ('"', "'"): v = v[1:-1]
    vars[m.group(1)] = v
# First `cd <target>` — literal path or a $VAR / ${VAR} / "$VAR" reference.
m = re.search(r'(?:^|[\s;&|(])cd\s+("(?:[^"\\]|\\.)*"|\'[^\']*\'|[^\s;&|()]+)', cmd)
if not m: raise SystemExit
t = m.group(1)
if t[:1] in ('"', "'"): t = t[1:-1]
mv = re.fullmatch(r'\$\{?(\w+)\}?', t)
if mv: t = vars.get(mv.group(1), "")
if t.startswith("~"): t = os.path.expanduser(t)
print(t)
PY
      cd_target="$(cat "$_cdf" 2>/dev/null)"
      rm -f "$_cdf" 2>/dev/null
    fi
    case "$cd_target" in /*|"") ;; *) cd_target="$cwd/$cd_target" ;; esac   # relative -> resolve vs cwd
    if [ -n "$cd_target" ] && [ -d "$cd_target" ]; then
      cd_project="$(devbrain_project_key "$cd_target" "$DATA")"
      case "$cd_project" in
        ""|miscellaneous|unknown) ;;          # cd target isn't a hosted repo -> keep cwd identity
        *) project="$cd_project" ;;           # the call's real target -> attribute there
      esac
    fi
  fi
fi

# Build one directional record from the command + output. Keep this delegated to
# devbrain_lib.py so the dashboard and hook parse gbrain command text identically.
record="$(TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)" python3 "$DEVBRAIN_LIB" gbrain-record "$cmd" "$out" "$project" 2>/dev/null)"
[ -n "$record" ] || exit 0     # no real gbrain subcommand -> nothing to log, touch nothing

log="$DATA/projects/$project/gbrain-queries.log"
mkdir -p "$DATA/projects/$project" 2>/dev/null || exit 0
printf '%s\n' "$record" >> "$log" 2>/dev/null

exit 0
