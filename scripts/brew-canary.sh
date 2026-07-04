#!/usr/bin/env bash
# Post-release canary: confirm the Homebrew tap serves the expected devbrain version
# and that the installed binary actually runs `doctor` (not the help fallback an older
# binary prints). Run right after `goreleaser release` pushes the formula. Mutates the
# local brew install (upgrades devbrain), so it's a real-machine check, not a dry run.
# Usage: scripts/brew-canary.sh <version>   e.g. scripts/brew-canary.sh 1.3.0
set -uo pipefail

want="${1:?usage: brew-canary.sh <version>}"
tap=theweihu/devbrain/devbrain

brew update >/dev/null 2>&1 || { echo "✗ brew update failed"; exit 1; }
# Fail loudly if brew can't get it — a silent failure here would otherwise let the
# PATH-binary checks below false-pass on a stale/dev install.
brew upgrade "$tap" >/dev/null 2>&1 || brew install "$tap" >/dev/null 2>&1 \
  || { echo "✗ brew could not install/upgrade $tap — did goreleaser push the formula to the tap?"; exit 1; }

# Check the BREW-installed binary explicitly, not whatever `devbrain` is first on PATH
# (a dev build earlier in PATH could otherwise mask a bad tap install).
bin="$(brew --prefix "$tap" 2>/dev/null)/bin/devbrain"
[ -x "$bin" ] || { echo "✗ brew-installed devbrain not found at $bin"; exit 1; }

got="$("$bin" version 2>/dev/null | head -1)"
if [ "$got" != "$want" ]; then
  echo "✗ version: want $want, got '${got:-<none>}' — did the tap formula update to $want?"
  exit 1
fi

# `doctor` exits 0 (healthy) or 1 (found problems); both mean it EXISTS and ran. A binary
# too old to have doctor prints the top-level help instead, so assert on doctor's header.
# Capture first, THEN grep — piping directly would let doctor's exit-1 poison the pipe
# under `pipefail`, failing the check even when the header matched.
doctor_out="$("$bin" doctor 2>&1)"
if ! printf '%s\n' "$doctor_out" | grep -q 'doctor — capture wiring'; then
  echo "✗ 'devbrain doctor' did not run — binary predates doctor (tap not really on $want?)"
  exit 1
fi

echo "✓ brew serves devbrain $got and 'devbrain doctor' runs"
