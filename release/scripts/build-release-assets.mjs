#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";

const argv = parseArgs(process.argv.slice(2));
const outDir = path.resolve(argv["out-dir"] || path.join(process.cwd(), "release-assets"));
const binariesDir = path.resolve(requiredArg(argv, "binaries-dir"));
const version = requiredArg(argv, "version");

const targets = [
  { binary: "toolbox-darwin-arm64", asset: `toolbox_${version}_darwin_arm64.tar.gz`, format: "tar" },
  { binary: "toolbox-darwin-x64", asset: `toolbox_${version}_darwin_x64.tar.gz`, format: "tar" },
  { binary: "toolbox-linux-arm64", asset: `toolbox_${version}_linux_arm64.tar.gz`, format: "tar" },
  { binary: "toolbox-linux-x64", asset: `toolbox_${version}_linux_x64.tar.gz`, format: "tar" },
  { binary: "toolbox-win32-x64.exe", asset: `toolbox_${version}_win32_x64.zip`, format: "zip" },
];

fs.rmSync(outDir, { recursive: true, force: true });
fs.mkdirSync(outDir, { recursive: true });

const checksums = [];
for (const target of targets) {
  const source = path.join(binariesDir, target.binary);
  if (!fs.existsSync(source)) {
    throw new Error(`missing binary ${source}`);
  }
  const assetPath = path.join(outDir, target.asset);
  if (target.format === "tar") {
    run("tar", ["-czf", assetPath, "-C", binariesDir, target.binary]);
  } else {
    run("zip", ["-j", assetPath, source]);
  }
  const checksum = crypto.createHash("sha256").update(fs.readFileSync(assetPath)).digest("hex");
  checksums.push(`${checksum}  ${target.asset}`);
}

fs.writeFileSync(path.join(outDir, `toolbox_${version}_checksums.txt`), `${checksums.join("\n")}\n`);

function run(command, args) {
  const result = spawnSync(command, args, { stdio: "inherit" });
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed with exit code ${result.status}`);
  }
}

function parseArgs(args) {
  const out = {};
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (!arg.startsWith("--")) {
      throw new Error(`unexpected arg ${arg}`);
    }
    out[arg.slice(2)] = args[i + 1];
    i += 1;
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
