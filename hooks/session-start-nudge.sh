#!/usr/bin/env bash
# devbrain — SessionStart brain nudge.
#
# Fires when a session starts (startup|resume). If the working repo maps to a
# REAL project (owner__repo) that already has a brain — pages and/or open tasks —
# it injects a tiny, project-specific line of additionalContext telling the agent
# to query the brain BEFORE it answers, asks, or starts work. This is the dynamic,
# per-session counterpart to the standing CLAUDE.md instruction: same ask, but it
# arrives at the moment the model forms its plan and names real counts, so it reads
# as actionable rather than boilerplate.
#
# It is a NUDGE, not a query — it never runs gbrain itself (no latency, no cost, no
# stale-context injection); the agent still chooses the search terms. Read-only,
# never blocks, always exits 0 (fail open -> simply no nudge). Identity is resolved
# from cwd via the shared offline resolver, exactly like capture.sh / capture-gbrain.sh.
#
# Why only real hosted repos: a remote-less / non-repo cwd collapses to the shared
# "miscellaneous" bucket, which exists almost everywhere — nudging there would fire
# in throwaway dirs and train people to ignore it. We nudge only when you're in a
# tracked project with an actual brain to consult.

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

# Hook payload is JSON on stdin. Read it via the shared python shim (no jq); fail OPEN
# (exit 0, no nudge) if python3 or the shim is missing — never block/break session start.
payload="$(cat 2>/dev/null)" || exit 0
_hd="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_hd/hook-common.sh" "$HOME/.claude/hooks/devbrain-hook-common.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
command -v devbrain_read_event >/dev/null 2>&1 || exit 0
devbrain_has_python_lib || exit 0

cwd="$(devbrain_read_event cwd)"
[ -n "$cwd" ] || cwd="$PWD"

# Identity — shared OFFLINE resolver, so we read the SAME projects/<owner>__<repo>
# folder capture.sh and the skills use. Installed alongside as devbrain-project-key.sh.
devbrain_source_project_key || exit 0
project="$(devbrain_project_key "$cwd" "$DATA")"

# Only nudge for a real hosted project; skip the shared miscellaneous bucket.
[ -n "$project" ] && [ "$project" != "miscellaneous" ] || exit 0

pdir="$DATA/projects/$project"
[ -d "$pdir" ] || exit 0

# Count what's there to consult. Brain pages = brain/*.md; open tasks = todo files
# whose frontmatter status is `open`. Both are cheap globs/greps; no gbrain call.
pages=0
for f in "$pdir"/brain/*.md; do [ -e "$f" ] && pages=$((pages+1)); done
tasks=0
if [ -d "$pdir/todo" ]; then
  tasks="$(grep -lE '^status:[[:space:]]*open[[:space:]]*$' "$pdir"/todo/*.md 2>/dev/null | grep -c . )"
  [ -n "$tasks" ] || tasks=0
fi

# Nothing to consult -> no nudge (a project with no brain yet just gets silence).
[ "$pages" -gt 0 ] || [ "$tasks" -gt 0 ] || exit 0

# Build a human counts phrase: "12 brain pages and 3 open tasks" (drop zero parts).
plural() { [ "$1" = 1 ] && printf '%s' "$2" || printf '%ss' "$2"; }
parts=""
[ "$pages" -gt 0 ] && parts="$pages $(plural "$pages" 'brain page')"
if [ "$tasks" -gt 0 ]; then
  [ -n "$parts" ] && parts="$parts and "
  parts="$parts$tasks open $(plural "$tasks" task)"
fi

msg="devbrain: this repo maps to project \`$project\` with $parts. Before you answer a \
non-trivial question, ask the user something the brain may already record, or start work, \
query the brain FIRST: \`gbrain search \"<terms>\"\` (or \`gbrain query \"<question>\"\` with an \
OpenAI key). No gbrain installed? \`devbrain brain search \"<terms>\"\` is a drop-in that greps \
the pages offline. The brain is usually faster and more current than re-deriving from the code. \
To READ a page a search surfaces, pass its FULL \`<project>/<page>\` slug from the output to \
\`gbrain get \"<project>/<page>\" --fuzzy\` (or \`devbrain brain get …\`) — not the bare page name \
(the brain is one namespace, so a bare slug is page_not_found). To resume this project in full \
— brief + work the top task — run /continue."

# SessionStart injects context via hookSpecificOutput.additionalContext. The shim
# builds valid JSON regardless of what's in $msg (quotes, backticks, etc.).
printf '%s' "$msg" | python3 "$DEVBRAIN_LIB" session-start-context 2>/dev/null

exit 0
