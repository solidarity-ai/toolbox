#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";

const argv = parseArgs(process.argv.slice(2));
const distDir = path.resolve(requiredArg(argv, "dist-dir"));
const dryRun = argv["dry-run"] === "true";
const explicitTag = argv.tag;

const publishOrder = [
  "toolbox-darwin-arm64",
  "toolbox-darwin-x64",
  "toolbox-linux-arm64",
  "toolbox-linux-x64",
  "toolbox-win32-x64",
  "toolbox",
];

for (const pkg of publishOrder) {
  const cwd = path.join(distDir, "packages", pkg);
  const manifestPath = path.join(cwd, "package.json");
  if (!fs.existsSync(manifestPath)) {
    throw new Error(`missing package manifest in ${cwd}`);
  }
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));

  const args = ["publish", "--access", "public"];
  const publishTag = explicitTag || defaultTagForVersion(manifest.version);
  if (publishTag) {
    args.push("--tag", publishTag);
  }
  if (dryRun) {
    args.push("--dry-run");
  }
  const result = spawnSync("npm", args, { cwd, stdio: "inherit" });
  if (result.status !== 0) {
    throw new Error(`npm publish failed for ${pkg}`);
  }
}

function parseArgs(args) {
  const out = {};
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (!arg.startsWith("--")) {
      throw new Error(`unexpected arg ${arg}`);
    }
    out[arg.slice(2)] = args[i + 1] ?? "true";
    if (args[i + 1] && !args[i + 1].startsWith("--")) {
      i += 1;
    }
  }
  return out;
}

function requiredArg(args, name) {
  const value = args[name];
  if (!value) {
    throw new Error(`missing --${name}`);
  }
  return value;
}

function defaultTagForVersion(version) {
  return version.includes("-") ? "next" : "";
}
