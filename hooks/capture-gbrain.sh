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

# Hook payload is JSON on stdin. jq to parse; python3 to extract+redact (both are
# devbrain deps already) — fail OPEN (exit 0) if either is missing, never block.
payload="$(cat 2>/dev/null)" || exit 0
command -v jq >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

tool="$(printf '%s' "$payload" | jq -r '.tool_name // empty' 2>/dev/null)"
[ "$tool" = "Bash" ] || exit 0
cmd="$(printf '%s' "$payload" | jq -r '.tool_input.command // empty' 2>/dev/null)"
[ -n "$cmd" ] || exit 0
case "$cmd" in *gbrain*) ;; *) exit 0 ;; esac     # fast bail: no gbrain in this command

cwd="$(printf '%s' "$payload" | jq -r '.cwd // empty' 2>/dev/null)"
[ -n "$cwd" ] || cwd="$PWD"

# tool_response shape varies by version (object with .stdout, or a bare string) —
# coerce whatever it is into the printed text so we can parse result lines from it.
out="$(printf '%s' "$payload" | jq -r '
  (.tool_response // "") as $r
  | if   ($r|type)=="object" then ($r.stdout // $r.output // ($r|tostring))
    elif ($r|type)=="string" then $r
    else ($r|tostring) end' 2>/dev/null)"

# Identity — shared OFFLINE resolver, so we write to the SAME projects/<owner>__<repo>
# folder capture.sh uses. Installed alongside as devbrain-project-key.sh.
_pk="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_pk/devbrain-project-key.sh" "$_pk/project-key.sh" "$HOME/.claude/hooks/devbrain-project-key.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"

log="$DATA/projects/$project/gbrain-queries.log"
mkdir -p "$DATA/projects/$project" 2>/dev/null || exit 0

# Build one directional record from the command + output. The command snippet is
# redacted via the shared rule lib (devbrain_lib.redact) so secrets never reach the
# log even though it's the private data repo.
_libdir="$_pk"; [ -f "$_libdir/devbrain_lib.py" ] || _libdir="$HOME/.claude/hooks"
TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)" python3 - "$cmd" "$out" "$project" "$_libdir" >> "$log" 2>/dev/null <<'PY'
import sys, re, json, os
cmd, out, project, libdir = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
sys.path.insert(0, libdir)
try:
    import devbrain_lib; redact = devbrain_lib.redact
except Exception:
    redact = lambda s: s
ts = os.environ.get("TS", "")

# Which gbrain subcommands the command actually used. Whitelisted to real ones, so
# the word "gbrain" inside a string ("log gbrain queries") or a filename
# (capture-gbrain.sh) doesn't masquerade as a call. A space after the subcommand is
# required, which also rules out path-y refs like gbrain-queries.log.
WHITELIST = {"query", "search", "ask", "get", "put", "delete",
             "list", "tag", "link", "embed", "sync", "import", "export"}
modes = []
for m in re.finditer(r'gbrain\s+([a-z][a-z-]*)', cmd):
    s = m.group(1)
    if s in WHITELIST and s not in modes:
        modes.append(s)
if not modes:
    sys.exit(0)   # no real gbrain subcommand -> not a call, don't log

# The command itself, redacted + whitespace-collapsed + truncated. Carries the
# topic words even when the query arg is a variable.
snippet = redact(re.sub(r'\s+', ' ', cmd).strip())
if len(snippet) > 300:
    snippet = snippet[:300] + "…"

# What surfaced — result lines look like "[0.83] owner__repo/slug -- snippet".
slugs, hits = [], 0
for ln in out.splitlines():
    if re.match(r'\[[0-9.]+\]', ln):
        hits += 1
        mm = re.match(r'\[[0-9.]+\]\s+(\S+)\s+--', ln)
        if mm and mm.group(1) not in slugs:
            slugs.append(mm.group(1))

print(json.dumps({"ts": ts, "project": project, "cmd": snippet,
                  "modes": modes, "hits": hits, "slugs": slugs},
                 ensure_ascii=False))
PY

exit 0
