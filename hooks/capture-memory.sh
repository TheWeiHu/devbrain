#!/usr/bin/env bash
# devbrain — Stage A capture, memory side (SessionEnd hook).
#
# Mirrors Claude Code's per-project memory store into the devbrain data repo. Claude
# writes durable curated facts to ~/.claude/projects/<project-slug>/memory/*.md; that
# store is the LONGEST-LIVED, highest-fidelity source on the machine — it survives the
# transcript pruning that drops raw sessions after a few weeks. Copying it into the
# data repo makes those facts durable (pushed off-machine) and lets /distill fold them
# into brain pages.
#
# Fires on SessionEnd: once per session, mirrors the WHOLE memory dir for the session's
# cwd (so memory written by a prior crashed session in the same repo is picked up the
# next time a session there ends). Model-free, never blocks, always exit 0.
#
# Stage A (this hook) mirrors raw memory; Stage B (/distill) curates it into brain
# pages — exactly how capture.sh/capture-response.sh feed prompts/responses.

DATA="${DEVBRAIN_DATA:-$HOME/devbrain-data}"

payload="$(cat 2>/dev/null)" || exit 0
command -v jq >/dev/null 2>&1 || exit 0

transcript="$(printf '%s' "$payload" | jq -r '.transcript_path // empty' 2>/dev/null)"
cwd="$(printf '%s'        "$payload" | jq -r '.cwd // empty' 2>/dev/null)"
[ -n "$cwd" ] || cwd="$PWD"

# Memory lives next to the transcript: <project-slug>/memory/. Deriving it from
# transcript_path is EXACT — no fragile cwd->slug reconstruction.
[ -n "$transcript" ] || exit 0
memdir="$(dirname "$transcript")/memory"
[ -d "$memdir" ] || exit 0

# Resolve project identity the SAME way as capture.sh / capture-response.sh, via the
# shared offline resolver, so memory lands in the same projects/<owner>__<repo> folder.
_pk="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)"
for _c in "$_pk/devbrain-project-key.sh" "$_pk/project-key.sh" "$HOME/.claude/hooks/devbrain-project-key.sh"; do
  [ -f "$_c" ] && { . "$_c"; break; }
done
project="$(devbrain_project_key "$cwd" "$DATA")"; [ -n "$project" ] || project="unknown"

dest="$DATA/projects/$project/memory"
mkdir -p "$dest" 2>/dev/null || exit 0

# Same high-confidence, prefix-anchored, fail-open redaction as the other capture
# hooks — a memory file can carry a key.
redact() {
  sed -E \
    -e 's/sk-[A-Za-z0-9_-]{20,}/[REDACTED]/g' \
    -e 's/(gh[pousr]_)[A-Za-z0-9]{20,}/[REDACTED]/g' \
    -e 's/github_pat_[A-Za-z0-9_]{20,}/[REDACTED]/g' \
    -e 's/(AKIA|ASIA)[0-9A-Z]{16}/[REDACTED]/g' \
    -e 's/xox[baprs]-[A-Za-z0-9-]{10,}/[REDACTED]/g' \
    -e 's/(Bearer )[A-Za-z0-9._-]{16,}/\1[REDACTED]/g'
}

# Mirror each memory file (redacted), but only when new or changed — so unchanged
# sessions produce no churn and the flusher has nothing to commit.
for f in "$memdir"/*.md; do
  [ -e "$f" ] || continue
  base="$(basename "$f")"
  out="$dest/$base"
  red="$(redact < "$f" 2>/dev/null)"
  [ -n "$red" ] || red="$(cat "$f" 2>/dev/null)"   # fail open: keep original if sed hiccups
  if [ ! -e "$out" ] || ! printf '%s' "$red" | cmp -s - "$out"; then
    printf '%s' "$red" > "$out" 2>/dev/null || true
  fi
done

exit 0
