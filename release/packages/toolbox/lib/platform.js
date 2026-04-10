const fs = require("node:fs");
const path = require("node:path");

const PLATFORM_PACKAGES = {
  "darwin-arm64": "@include-tools/toolbox-darwin-arm64",
  "darwin-x64": "@include-tools/toolbox-darwin-x64",
  "linux-arm64": "@include-tools/toolbox-linux-arm64",
  "linux-x64": "@include-tools/toolbox-linux-x64",
  "win32-x64": "@include-tools/toolbox-win32-x64",
};

function currentTarget() {
  return `${process.platform}-${process.arch}`;
}

function resolvePlatformPackageName() {
  const target = currentTarget();
  const packageName = PLATFORM_PACKAGES[target];
  if (!packageName) {
    throw new Error(`unsupported toolbox platform ${target}`);
  }
  return packageName;
}

function findBinaryPath(options = {}) {
  const explicitPath = options.binaryPath || process.env.TOOLBOX_BINARY_PATH;
  if (explicitPath) {
    const resolved = path.resolve(explicitPath);
    if (!fs.existsSync(resolved)) {
      throw new Error(`toolbox binary not found at ${resolved}`);
    }
    return resolved;
  }

  const packageName = resolvePlatformPackageName();
  let exported;
  try {
    exported = require(packageName);
  } catch (error) {
    throw new Error(`unable to resolve ${packageName}: ${error.message}`);
  }

  const binaryPath = typeof exported === "string" ? exported : exported && exported.binaryPath;
  if (!binaryPath) {
    throw new Error(`${packageName} does not export a binaryPath`);
  }
  if (!fs.existsSync(binaryPath)) {
    throw new Error(`resolved toolbox binary does not exist at ${binaryPath}`);
  }
  return binaryPath;
}

module.exports = {
  PLATFORM_PACKAGES,
  currentTarget,
  findBinaryPath,
  resolvePlatformPackageName,
};
