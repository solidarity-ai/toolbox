#!/usr/bin/env node

const { spawnSync } = require("node:child_process");

const { findBinaryPath } = require("../lib/platform");

const result = spawnSync(findBinaryPath(), process.argv.slice(2), {
  stdio: "inherit",
});

if (result.error) {
  throw result.error;
}
if (typeof result.status === "number") {
  process.exit(result.status);
}
process.exit(1);
