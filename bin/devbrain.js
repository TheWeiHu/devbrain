#!/usr/bin/env node
// devbrain — npm front-end, kept as a thin shim. The runtime is a single Go
// binary distributed via Homebrew / GitHub releases; this package only
// forwards to an installed `devbrain` on PATH, and points at the real install
// channels when it isn't there. No download logic on purpose.
import { spawnSync } from "node:child_process";

const args = process.argv.slice(2);

const INSTALL = `devbrain — git-backed prompt memory + resume skills for coding agents

The devbrain CLI is a single binary. Install it, then wire this machine:

  brew install TheWeiHu/devbrain/devbrain   # or grab a release tarball:
  # https://github.com/TheWeiHu/devbrain/releases

  devbrain install

This npm package is only a forwarder for machines that already have the
binary on PATH.`;

if (args.length === 0 || ["help", "--help", "-h"].includes(args[0])) {
  console.log(INSTALL);
  process.exit(0);
}

const r = spawnSync("devbrain", args, { stdio: "inherit" });
if (r.error && r.error.code === "ENOENT") {
  console.error(`devbrain binary not found on PATH.\n\n${INSTALL}`);
  process.exit(1);
}
process.exit(r.status ?? 1);
