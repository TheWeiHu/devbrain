#!/usr/bin/env bash
# Post-release canary: confirm the Homebrew tap serves the expected devbrain version
# and that the installed binary actually runs `doctor` (not the help fallback an older
# binary prints). Run after a release once the tap PR is merged. Mutates the local brew
# install (upgrades devbrain), so it's a real-machine check, not a dry run.
# Usage: scripts/brew-canary.sh <version>   e.g. scripts/brew-canary.sh 1.3.0
set -uo pipefail

want="${1:?usage: brew-canary.sh <version>}"
tap=theweihu/devbrain/devbrain

brew update >/dev/null 2>&1
brew upgrade "$tap" >/dev/null 2>&1 || brew install "$tap" >/dev/null 2>&1

got="$(devbrain version 2>/dev/null | head -1)"
if [ "$got" != "$want" ]; then
  echo "✗ version: want $want, got '${got:-<none>}' — is the tap PR merged and the release published?"
  exit 1
fi

# `doctor` exits 0 (healthy) or 1 (found problems); both mean it EXISTS and ran. A binary
# too old to have doctor prints the top-level help instead, so assert on doctor's header.
# Capture first, THEN grep — piping directly would let doctor's exit-1 poison the pipe
# under `pipefail`, failing the check even when the header matched.
doctor_out="$(devbrain doctor 2>&1)"
if ! printf '%s\n' "$doctor_out" | grep -q 'doctor — capture wiring'; then
  echo "✗ 'devbrain doctor' did not run — binary predates doctor (tap not really on $want?)"
  exit 1
fi

echo "✓ brew serves devbrain $got and 'devbrain doctor' runs"
