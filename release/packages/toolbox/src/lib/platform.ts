import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";

import type { CreateClientOptions } from "../index.js";

const require = createRequire(import.meta.url);

export const PLATFORM_PACKAGES: Record<string, string> = {
  "darwin-arm64": "@include-tools/toolbox-darwin-arm64",
  "darwin-x64": "@include-tools/toolbox-darwin-x64",
  "linux-arm64": "@include-tools/toolbox-linux-arm64",
  "linux-x64": "@include-tools/toolbox-linux-x64",
  "win32-x64": "@include-tools/toolbox-win32-x64",
};

export function currentTarget(): string {
  return `${process.platform}-${process.arch}`;
}

export function resolvePlatformPackageName(): string {
  const target = currentTarget();
  const packageName = PLATFORM_PACKAGES[target];
  if (!packageName) {
    throw new Error(`unsupported toolbox platform ${target}`);
  }
  return packageName;
}

export function findBinaryPath(options: CreateClientOptions = {}): string {
  const explicitPath = options.binaryPath || process.env.TOOLBOX_BINARY_PATH;
  if (explicitPath) {
    const resolved = path.resolve(explicitPath);
    if (!fs.existsSync(resolved)) {
      throw new Error(`toolbox binary not found at ${resolved}`);
    }
    return resolved;
  }

  const packageName = resolvePlatformPackageName();
  let exported: unknown;
  try {
    exported = require(packageName);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`unable to resolve ${packageName}: ${message}`);
  }

  const binaryPath = typeof exported === "string"
    ? exported
    : exported && typeof exported === "object" && "binaryPath" in exported
      ? exported.binaryPath
      : undefined;
  if (typeof binaryPath !== "string" || binaryPath.length === 0) {
    throw new Error(`${packageName} does not export a binaryPath`);
  }
  if (!fs.existsSync(binaryPath)) {
    throw new Error(`resolved toolbox binary does not exist at ${binaryPath}`);
  }
  return binaryPath;
}
