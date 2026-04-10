#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const argv = parseArgs(process.argv.slice(2));
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const packagesRoot = path.join(repoRoot, "packages");
const outDir = path.resolve(argv["out-dir"] || path.join(repoRoot, "dist", "npm"));
const binariesDir = path.resolve(requiredArg(argv, "binaries-dir"));
const rawVersion = requiredArg(argv, "version");
const npmVersion = normalizeNpmVersion(rawVersion);

const platformPackages = [
  { dir: "toolbox-darwin-arm64", binary: "toolbox-darwin-arm64", installName: "toolbox" },
  { dir: "toolbox-darwin-x64", binary: "toolbox-darwin-x64", installName: "toolbox" },
  { dir: "toolbox-linux-arm64", binary: "toolbox-linux-arm64", installName: "toolbox" },
  { dir: "toolbox-linux-x64", binary: "toolbox-linux-x64", installName: "toolbox" },
  { dir: "toolbox-win32-x64", binary: "toolbox-win32-x64.exe", installName: "toolbox.exe" },
];

fs.rmSync(outDir, { recursive: true, force: true });
fs.mkdirSync(outDir, { recursive: true });
fs.cpSync(packagesRoot, path.join(outDir, "packages"), { recursive: true });

for (const packageDir of fs.readdirSync(path.join(outDir, "packages"))) {
  const manifestPath = path.join(outDir, "packages", packageDir, "package.json");
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  manifest.version = npmVersion;
  if (manifest.optionalDependencies) {
    for (const name of Object.keys(manifest.optionalDependencies)) {
      manifest.optionalDependencies[name] = npmVersion;
    }
  }
  fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
}

for (const pkg of platformPackages) {
  const src = path.join(binariesDir, pkg.binary);
  if (!fs.existsSync(src)) {
    throw new Error(`missing binary ${src}`);
  }
  const binDir = path.join(outDir, "packages", pkg.dir, "bin");
  fs.mkdirSync(binDir, { recursive: true });
  const dest = path.join(binDir, pkg.installName);
  fs.copyFileSync(src, dest);
  fs.chmodSync(dest, 0o755);
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

function normalizeNpmVersion(version) {
  return version.startsWith("v") ? version.slice(1) : version;
}
