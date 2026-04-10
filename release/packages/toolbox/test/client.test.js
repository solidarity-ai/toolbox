import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { createClient } from "../dist/index.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, "../../../../");
let cachedBinaryPath;

function testBinaryPath() {
  return process.env.TOOLBOX_BINARY_PATH || buildLocalBinary();
}

function buildLocalBinary() {
  if (cachedBinaryPath) {
    return cachedBinaryPath;
  }

  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "toolbox-node-test-"));
  const binaryPath = path.join(tempRoot, process.platform === "win32" ? "toolbox.exe" : "toolbox");
  const env = {
    ...process.env,
    GOCACHE: path.join(tempRoot, "gocache"),
  };
  const result = spawnSync("go", ["build", "-o", binaryPath, "."], {
    cwd: path.join(repoRoot, "cmd/toolbox"),
    env,
    encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr || result.stdout);
  cachedBinaryPath = binaryPath;
  return cachedBinaryPath;
}

function writeToolsetDir() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "toolbox-toolset-"));
  const toolsetPath = path.join(dir, "toolbox.toolset.json");
  const moduleName = "fixtures.local/calc";
  fs.writeFileSync(toolsetPath, JSON.stringify({
    packages: {
      [moduleName]: "v1.2.3",
    },
    tools: [
      { tool: `${moduleName}@v1.2.3/calc.add` },
    ],
  }));
  fs.writeFileSync(path.join(dir, "toolbox.toolset.local.json"), JSON.stringify({
    replace: {
      [moduleName]: path.join(repoRoot, "testutil/fixtures/toolbox.pkgs/calc"),
    },
  }));
  return toolsetPath;
}

test("client composes a toolset and invokes a tool", async () => {
  const client = await createClient({ binaryPath: testBinaryPath() });
  try {
    const toolset = await client.toolset.compose({
      mode: "direct",
      toolset_file: writeToolsetDir(),
    });
    const add = toolset.tools.find((tool) => tool.name === "calc.add");
    assert.ok(add, "expected calc.add tool");
    assert.equal(await add.invoke({ a: 2, b: 5 }), "7");
    await toolset.close();
  } finally {
    await client.close();
  }
});

test("client writes and reloads a toolset file", async () => {
  const client = await createClient({ binaryPath: testBinaryPath() });
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "toolbox-toolset-write-"));
  const toolsetPath = path.join(dir, "toolbox.toolset.json");
  try {
    await client.toolsetfile.write(toolsetPath, {
      packages: {
        "example.com/z": "v1.0.0",
        "example.com/a": "v1.0.0",
      },
      tools: [
        { tool: "example.com/z@v1.0.0/z.run" },
      ],
    });
    const loaded = await client.toolsetfile.load(toolsetPath);
    assert.equal(loaded.packages["example.com/a"], "v1.0.0");
    const raw = fs.readFileSync(toolsetPath, "utf8");
    assert.match(raw, /"example.com\/a"/);
  } finally {
    await client.close();
  }
});
