#!/usr/bin/env bash
# devbrain — shared, OFFLINE project-identity resolver.
#
# Single source of truth for "which projects/<key>/ folder does this working repo
# map to?" Sourced by capture.sh (the hot hook), todo.sh, and the /continue +
# /distill skills so identity is resolved the SAME way everywhere.
#
# Contract: PURE-LOCAL and NEVER-FAIL. It only parses the git remote URL *string*
# and reads/writes a per-folder `.identity` file — it makes NO network call (no
# `gh api`), never blocks, and every path degrades to a basename guess. Safe to
# source from the UserPromptSubmit hook, which must always exit 0.
#
# Why this exists: project identity used to be just the sanitized repo BASENAME of
# the remote (owner/host dropped). That silently merged two different repos sharing
# a basename (github.com/orgA/api vs orgB/api) into one folder, and a repo rename
# orphaned its history. This records the canonical remote per folder and keys new
# or colliding projects by `<owner>__<repo>`.
#
# SCOPE (task 0014 MVP): record identity + resolve collisions, backward-compatibly.
# Existing legacy-basename folders are ADOPTED in place (their `.identity` is
# backfilled on next use) — they are NOT renamed to the canonical key here, and
# rename detection/merge is a deferred follow-up (it needs the now-recorded remote).

# devbrain_sanitize <str> -> filesystem-safe slug (matches capture.sh / todo.sh).
devbrain_sanitize() { printf '%s' "$1" | tr '[:upper:] ' '[:lower:]-' | tr -cd '[:alnum:]._-'; }

# devbrain_parse_remote <remote-url> -> prints "owner<TAB>repo<TAB>host" (offline).
# Handles git@host:owner/repo(.git), ssh://git@host/owner/repo, https://host/owner/
# repo(.git), and bare host/owner/repo. owner = second-to-last path segment, repo =
# last. Fields are empty when unparseable.
devbrain_parse_remote() {
  local url="$1" path host owner repo rest
  url="${url%.git}"; url="${url%/}"
  case "$url" in
    *://*)   path="${url#*://}"; path="${path#*@}"; host="${path%%/*}"; path="${path#*/}" ;;
    *@*:*)   path="${url#*@}"; host="${path%%:*}"; path="${path#*:}" ;;
    *)       path="$url"; host="${path%%/*}"; path="${path#*/}" ;;
  esac
  repo="${path##*/}"
  rest="${path%/*}"
  if [ "$rest" = "$path" ]; then owner=""; else owner="${rest##*/}"; fi
  printf '%s\t%s\t%s' "$owner" "$repo" "$host"
}

# devbrain_normalize_remote <remote-url> -> "host/owner/repo" lowercased, for
# comparing two URL forms (scp vs https) of the same repo.
devbrain_normalize_remote() {
  local owner repo host
  IFS=$'\t' read -r owner repo host <<EOF
$(devbrain_parse_remote "$1")
EOF
  printf '%s/%s/%s' "$(printf '%s' "$host"  | tr '[:upper:]' '[:lower:]')" \
                    "$(printf '%s' "$owner" | tr '[:upper:]' '[:lower:]')" \
                    "$(printf '%s' "$repo"  | tr '[:upper:]' '[:lower:]')"
}

# devbrain_identity_matches <idfile> <remote> -> 0 if the folder's recorded remote
# is the same repo as <remote> (or no remote was recorded — legacy adopt).
devbrain_identity_matches() {
  local recorded; recorded="$(sed -n 's/^remote: //p' "$1" 2>/dev/null | head -1)"
  [ -n "$recorded" ] || return 0
  [ "$(devbrain_normalize_remote "$recorded")" = "$(devbrain_normalize_remote "$2")" ]
}

# devbrain_write_identity <dir> <remote> <owner> <repo> <host> — write .identity
# once (never clobbers an existing one). Best-effort; never fails the caller.
devbrain_write_identity() {
  local dir="$1" idfile="$1/.identity"
  [ -f "$idfile" ] && return 0
  mkdir -p "$dir" 2>/dev/null || return 0
  {
    printf '# devbrain project identity — the canonical remote this folder belongs to.\n'
    printf '# Written once on folder creation; read offline to detect collisions/renames.\n'
    printf 'remote: %s\n' "$2"
    printf 'owner: %s\n'  "$3"
    printf 'repo: %s\n'   "$4"
    printf 'host: %s\n'   "$5"
  } >> "$idfile" 2>/dev/null || true
  return 0
}

# devbrain_project_key [<cwd>] [<data>] -> prints the resolved project folder name
# and ensures projects/<key>/.identity exists. Offline + never-fail.
#
# Resolution (offline): canonical key is <owner>__<repo>. Prefer an existing
# canonical folder; else adopt an existing legacy-basename folder unless its
# recorded remote differs (a COLLISION -> use the canonical key instead); else a
# brand-new project gets the canonical key. $DEVBRAIN_PROJECT overrides everything.
devbrain_project_key() {
  local cwd="${1:-$PWD}" data="${2:-${DEVBRAIN_DATA:-$HOME/devbrain-data}}"
  if [ -n "${DEVBRAIN_PROJECT:-}" ]; then
    local p; p="$(devbrain_sanitize "$DEVBRAIN_PROJECT")"; printf '%s' "${p:-unknown}"; return 0
  fi
  local remote owner repo host legacy canon key idfile
  remote="$(git -C "$cwd" remote get-url origin 2>/dev/null)"
  if [ -z "$remote" ]; then
    legacy="$(devbrain_sanitize "$(basename "$cwd")")"; printf '%s' "${legacy:-unknown}"; return 0
  fi
  IFS=$'\t' read -r owner repo host <<EOF
$(devbrain_parse_remote "$remote")
EOF
  legacy="$(devbrain_sanitize "${repo:-$(basename "${remote%.git}")}")"; [ -n "$legacy" ] || legacy="unknown"
  if [ -n "$owner" ] && [ -n "$repo" ]; then
    canon="$(devbrain_sanitize "${owner}__${repo}")"
  else
    canon="$legacy"
  fi
  if [ -d "$data/projects/$canon" ]; then
    key="$canon"
  elif [ -d "$data/projects/$legacy" ]; then
    idfile="$data/projects/$legacy/.identity"
    if [ -f "$idfile" ] && ! devbrain_identity_matches "$idfile" "$remote"; then
      key="$canon"          # legacy folder is owned by a different remote -> collision
    else
      key="$legacy"         # same repo, or pre-identity legacy folder -> adopt in place
    fi
  else
    key="$canon"            # brand-new project -> canonical key
  fi
  devbrain_write_identity "$data/projects/$key" "$remote" "$owner" "$repo" "$host"
  printf '%s' "$key"
  return 0
}
