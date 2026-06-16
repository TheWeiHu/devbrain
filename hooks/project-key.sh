#!/usr/bin/env bash
# devbrain — offline project-identity key.
#
# Maps the working repo to its projects/<key>/ folder. The key is <owner>__<repo>,
# parsed offline from the git remote URL. It's collision-resistant *by construction*
# — two repos that share a basename have different owners, so they land in different
# folders — which means there's no lookup table, no folder scan, and no per-folder
# comparison to resolve. Pure-local (no network) and never-fail, so it's safe to
# source from the UserPromptSubmit capture hook (which must always exit 0).
#
# Sourced by capture.sh, todo.sh, and the /continue + /distill skills so every part
# of devbrain resolves identity the same way. $DEVBRAIN_PROJECT overrides it; a repo
# with no remote falls back to the cwd basename (historical behavior).

# devbrain_sanitize <str> -> filesystem-safe slug (matches capture.sh / todo.sh).
devbrain_sanitize() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'; }

# devbrain_project_key [<cwd>] -> prints the projects/<key> folder name.
devbrain_project_key() {
  local cwd="${1:-$PWD}" remote url repo rest owner key
  [ -n "${DEVBRAIN_PROJECT:-}" ] && { devbrain_sanitize "$DEVBRAIN_PROJECT"; return 0; }
  remote="$(git -C "$cwd" remote get-url origin 2>/dev/null)"
  [ -z "$remote" ] && { devbrain_sanitize "$(basename "$cwd")"; return 0; }
  url="${remote%.git}"; url="${url%/}"                          # drop trailing .git / slash
  repo="${url##*/}"                                             # last path segment
  rest="${url%/*}"                                             # everything before it
  [ "$rest" = "$url" ] && owner="" || owner="${rest##*[:/]}"    # segment after the last : or /
  [ -n "$owner" ] && key="$(devbrain_sanitize "${owner}__${repo}")" || key="$(devbrain_sanitize "$repo")"
  printf '%s' "${key:-unknown}"
}
