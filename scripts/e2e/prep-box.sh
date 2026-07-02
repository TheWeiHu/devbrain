#!/usr/bin/env bash
# One-time (idempotent) prep of the Linux e2e box for clean-environment brew
# installs: swapfile (Homebrew needs more memory than the box has), build
# deps, and Homebrew itself. Run ON the box (piped over ssh by e2e-brew).
set -uo pipefail

echo "== swap =="
if swapon --show 2>/dev/null | grep -q /swapfile; then
  echo "swapfile already active"
else
  sudo fallocate -l 2G /swapfile 2>/dev/null || sudo dd if=/dev/zero of=/swapfile bs=1M count=2048
  sudo chmod 600 /swapfile
  sudo mkswap /swapfile
  sudo swapon /swapfile
  grep -q '/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab >/dev/null
fi

echo "== packages =="
sudo dnf install -y -q git gcc procps-ng curl file tar 2>&1 | tail -1

echo "== homebrew =="
BREW=/home/linuxbrew/.linuxbrew/bin/brew
if [ -x "$BREW" ]; then
  echo "brew already installed: $("$BREW" --version | head -1)"
else
  export NONINTERACTIVE=1
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
fi
if ! grep -q linuxbrew ~/.bashrc 2>/dev/null; then
  {
    echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"'
    # the tiny box: never let brew self-update or run cleanup mid-test
    echo 'export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_INSTALL_CLEANUP=1'
  } >> ~/.bashrc
fi
"$BREW" --version | head -1

# Linuxbrew's compiler gate: any non-bottled formula (ours, pre-tap) refuses
# to install without brew's own toolchain present. Bottled, no compile.
"$BREW" list gcc >/dev/null 2>&1 || {
  export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_INSTALL_CLEANUP=1
  "$BREW" install -q gcc 2>&1 | tail -1
}
echo "prep-box: done"
