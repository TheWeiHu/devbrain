#!/usr/bin/env sh
# Run the Go suite with git sandboxed, and fail loudly if it mutated the real repo.
#
# `go test` runs every package with cwd inside this checkout, so a test that
# forgets `-C <tmpdir>` runs git against the real repo. That once flipped
# core.bare/origin here and — because the repo uses extensions.worktreeConfig —
# broke every worktree at once. It was racy and never reproduced, so detection,
# not prevention, is the load-bearing half:
#   sandbox: keeps a stray `git config --global` off the user's real config and
#            stops discovery walking up out of a tempdir. Does NOT cover the cwd
#            vector above (the repo's .git is found below the ceiling).
#   canary:  byte-compares the repo's shared config across the suite. This is
#            what catches the cwd vector, and would have caught the original.
#
# Args are passed through to `go test` (CI: `-race`). `$GO` overrides the toolchain.
set -eu

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

export GIT_CONFIG_GLOBAL="$tmp/gitconfig" GIT_CONFIG_SYSTEM=/dev/null
export GIT_CEILING_DIRECTORIES="${HOME:-/nonexistent}"

# The shared config — the file that got corrupted. Relative when at the repo root.
cfg="$(git rev-parse --git-common-dir 2>/dev/null || true)"
case "$cfg" in
	"") ;;                       # not a git checkout (tarball build): canary off
	/*) cfg="$cfg/config" ;;
	*)  cfg="$(pwd)/$cfg/config" ;;
esac
[ -n "$cfg" ] && [ -f "$cfg" ] && cp "$cfg" "$tmp/before" || cfg=""

status=0
"${GO:-go}" vet ./... && "${GO:-go}" test "$@" ./... || status=$?

if [ -n "$cfg" ] && ! cmp -s "$tmp/before" "$cfg"; then
	cp "$cfg" "$cfg.canary-mutated"
	echo "ERROR: the test suite mutated $cfg — a test escaped its sandbox." >&2
	echo "Restored the pre-suite config; mutated copy at $cfg.canary-mutated. Diff:" >&2
	diff "$tmp/before" "$cfg.canary-mutated" >&2 || true
	cp "$tmp/before" "$cfg"
	exit 1
fi
exit "$status"
