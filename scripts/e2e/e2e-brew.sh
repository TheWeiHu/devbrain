#!/usr/bin/env bash
# Local driver for the clean-box brew e2e: build the linux binary, package it
# as a brew-installable local formula, ship everything to the box, run the
# assertion cycle (scripts/e2e/run.sh) there. Skips cleanly off the dev
# machine (no key). Env: E2E_HOST / E2E_KEY override the box coordinates.
set -euo pipefail
cd "$(dirname "$0")/../.."

HOST="${E2E_HOST:-ec2-user@3.224.127.196}"
KEY="${E2E_KEY:-$HOME/.ssh/LightsailProdKey-us-east-1.pem}"
[ -f "$KEY" ] || { echo "skip: e2e box key not present ($KEY)"; exit 0; }
command -v goreleaser >/dev/null 2>&1 || { echo "skip: goreleaser not installed"; exit 0; }

TMPKEY="$(mktemp)"; cp "$KEY" "$TMPKEY"; chmod 600 "$TMPKEY"
SSH="ssh -i $TMPKEY -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new"
trap 'rm -f "$TMPKEY"' EXIT

echo "== build linux/amd64 snapshot =="
GOOS=linux GOARCH=amd64 goreleaser build --snapshot --clean --single-target -o dist/devbrain-linux >/dev/null

echo "== package =="
STAGE="$(mktemp -d)"
cp dist/devbrain-linux "$STAGE/devbrain"
tar -czf "$STAGE/devbrain.tar.gz" -C "$STAGE" devbrain
SHA="$(shasum -a 256 "$STAGE/devbrain.tar.gz" | awk '{print $1}')"
cat > "$STAGE/devbrain.rb" <<EOF
class Devbrain < Formula
  desc "Turn the prompts you write into a durable, queryable brain any agent can resume from"
  homepage "https://github.com/TheWeiHu/devbrain"
  url "file:///home/ec2-user/e2e/devbrain.tar.gz"
  sha256 "$SHA"
  version "1.0.0-e2e"
  license "MIT"

  def install
    bin.install "devbrain"
  end

  test do
    system "#{bin}/devbrain", "version"
  end
end
EOF

echo "== ship + prep =="
$SSH "$HOST" 'mkdir -p ~/e2e'
scp -q -i "$TMPKEY" "$STAGE/devbrain.tar.gz" "$STAGE/devbrain.rb" scripts/e2e/run.sh scripts/e2e/prep-box.sh "$HOST:e2e/"
$SSH "$HOST" 'bash e2e/prep-box.sh' | tail -2

EXPECTED="$($SSH "$HOST" 'tar -xzf e2e/devbrain.tar.gz -C /tmp && /tmp/devbrain version && rm -f /tmp/devbrain')"
echo "== run (expected version: $EXPECTED) =="
$SSH "$HOST" "EXPECTED_VERSION='$EXPECTED' bash e2e/run.sh"
rm -rf "$STAGE" dist/devbrain-linux
