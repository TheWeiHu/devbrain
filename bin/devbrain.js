#!/usr/bin/env node
// devbrain — npm front-end. `npx devbrain install` runs the proven bash setup
// (setup → scripts/install.sh) straight from the unpacked package; that installer
// already copies stable copies into ~/.claude, so the package dir can be npm's
// throwaway cache. After install, use the `devbrain` command directly.
import { spawnSync } from "node:child_process";
import { dirname, join } from "node:path";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { homedir } from "node:os";

const root = dirname(dirname(fileURLToPath(import.meta.url))); // package root
const cmd = process.argv[2] || "help";
const rest = process.argv.slice(3);

const HELP = `devbrain — git-backed prompt memory + resume skills for Claude Code

  npx getdevbrain install [--with nightshift] [--without flusher,...] [--no-open]
  npx getdevbrain uninstall

Install opens the dashboard (devbrain queue) when done; pass --no-open to skip it.

Env: DEVBRAIN_DATA=~/path  DEVBRAIN_DATA_REMOTE=git@host:you/brain.git
After install, run the installed CLI directly: devbrain help (todo · queue · import · …).`;

// install/uninstall route to the bash scripts shipped in this package.
const map = {
  install:   join(root, "setup"),
  uninstall: join(root, "scripts", "uninstall.sh"),
};

if (cmd === "help" || cmd === "--help" || cmd === "-h") {
  console.log(HELP);
  process.exit(0);
}

// Known installer verbs → run the packaged bash.
if (map[cmd]) {
  // Mark npm-driven installs so `setup` defaults to opening the dashboard at the
  // end (even when stdin is piped, e.g. `npx getdevbrain install` in some shells).
  // The user can still suppress it with `--no-open` / DEVBRAIN_OPEN_DASHBOARD=0.
  const env = cmd === "install"
    ? { ...process.env, DEVBRAIN_FROM_NPM: "1" }
    : process.env;
  const r = spawnSync(map[cmd], rest, { stdio: "inherit", env });
  process.exit(r.status ?? 1);
}

// Anything else → forward to the already-installed `devbrain` CLI if present,
// so `npm i -g devbrain` users don't lose todo/queue/import/etc.
const installed = join(homedir(), ".claude", "hooks", "devbrain");
if (existsSync(installed)) {
  const r = spawnSync(installed, [cmd, ...rest], { stdio: "inherit", env: process.env });
  process.exit(r.status ?? 1);
}

console.error(`unknown command: ${cmd}\n\n${HELP}`);
process.exit(1);
