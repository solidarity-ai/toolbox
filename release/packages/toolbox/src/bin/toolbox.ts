#!/usr/bin/env node

import { spawnSync } from "node:child_process";

import { findBinaryPath } from "../lib/platform.js";

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
