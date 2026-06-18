# typed: false
# frozen_string_literal: true

# devbrain Homebrew formula — installs the CLI ARTIFACT only. It deliberately does
# NOT wire the machine at brew-install time: a formula must be relocatable and free
# of $HOME side effects, and devbrain's wiring (capture hooks, flusher, skills,
# ~/.claude/CLAUDE.md) is a per-user action. So `brew install` lays the tree down
# and puts `devbrain` on PATH; the user then runs `devbrain setup` to wire.
#
# Lives in this repo as the canonical source. To publish, copy it into the tap repo
# `TheWeiHu/homebrew-devbrain` at `Formula/devbrain.rb` (see ../homebrew/README.md).
#
# MAINTAINER: on each release, bump `url` to the new tag and refresh `sha256`:
#     curl -sL https://github.com/TheWeiHu/devbrain/archive/refs/tags/vX.Y.Z.tar.gz | shasum -a 256
class Devbrain < Formula
  desc "Turn the prompts you write in any repo into a durable, queryable brain"
  homepage "https://github.com/TheWeiHu/devbrain"
  url "https://github.com/TheWeiHu/devbrain/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000" # REPLACE at release
  license "MIT"
  head "https://github.com/TheWeiHu/devbrain.git", branch: "main"

  depends_on "jq"
  depends_on "python@3.12"

  def install
    # Ship the whole tree so install.sh can copy hooks/, scripts/, skills/, VERSION
    # out to ~/.claude at `devbrain setup` time. devbrain resolves siblings by its
    # own path, so libexec/scripts/devbrain finds libexec/setup and libexec/VERSION.
    libexec.install Dir["*"]
    bin.install_symlink libexec/"scripts/devbrain"
  end

  def caveats
    <<~EOS
      This installed the devbrain CLI only — it did not touch your machine.
      To wire THIS machine (capture hooks, 5-min flusher, /continue + /distill
      skills, ~/.claude/CLAUDE.md), run:

        devbrain setup

      It is idempotent, reversible, and never touches your working repos. Your
      prompts + brain live in a SEPARATE private store you own (default
      ~/devbrain-data). Tear down anytime with the repo's scripts/uninstall.sh.

      Optional, for search: install `bun` (https://bun.sh) so the gbrain engine
      can auto-install, and set OPENAI_API_KEY for semantic ranking.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/devbrain version")
    assert_match "devbrain setup", shell_output("#{bin}/devbrain help")
  end
end
