const { spawn } = require("node:child_process");

const { findBinaryPath } = require("./lib/platform");
const { RpcClient } = require("./lib/rpc-client");

class Tool {
  constructor(client, toolsetId, descriptor) {
    this.#client = client;
    this.#toolsetId = toolsetId;
    this.name = descriptor.name;
    this.description = descriptor.description || "";
    this.paramsSchema = descriptor.params_schema || null;
  }

  #client;
  #toolsetId;

  async invoke(params = {}) {
    const result = await this.#client.request("tool.invoke", {
      toolset_id: this.#toolsetId,
      tool_name: this.name,
      params,
    });
    return result.content;
  }
}

class ToolsetHandle {
  constructor(client, id, descriptors) {
    this.#client = client;
    this.id = id;
    this.tools = descriptors.map((descriptor) => new Tool(client, id, descriptor));
  }

  #client;
  #closed = false;

  async close() {
    if (this.#closed) {
      return;
    }
    this.#closed = true;
    await this.#client.request("toolset.close", { toolset_id: this.id });
  }
}

class ToolboxClient {
  constructor(rpcClient) {
    this.#rpcClient = rpcClient;
    this.toolsetfile = {
      load: async (filePath) => this.#rpcClient.request("toolsetfile.load", { path: filePath }),
      write: async (filePath, toolset) => {
        await this.#rpcClient.request("toolsetfile.write", {
          path: filePath,
          toolset,
        });
      },
      edit: async (filePath, mutator) => {
        const toolset = await this.toolsetfile.load(filePath);
        const maybeNext = await mutator(toolset);
        const nextToolset = maybeNext === undefined ? toolset : maybeNext;
        await this.toolsetfile.write(filePath, nextToolset);
        return nextToolset;
      },
    };
    this.toolset = {
      compose: async (input) => {
        const result = await this.#rpcClient.request("toolset.compose", input);
        return new ToolsetHandle(this.#rpcClient, result.toolset_id, result.tools);
      },
    };
  }

  #rpcClient;

  async version() {
    const result = await this.#rpcClient.request("system.version", {});
    return result.version;
  }

  async close() {
    await this.#rpcClient.close();
  }
}

function findBinary(options = {}) {
  return findBinaryPath(options);
}

async function createClient(options = {}) {
  const binaryPath = findBinary(options);
  const child = spawn(binaryPath, ["_sdkbridge", "serve-stdio"], {
    cwd: options.cwd,
    env: { ...process.env, ...options.env },
    stdio: ["pipe", "pipe", "pipe"],
  });
  const rpcClient = new RpcClient(child);
  await rpcClient.request("system.ping", {});
  return new ToolboxClient(rpcClient);
}

module.exports = {
  createClient,
  findBinary,
  ToolboxClient,
  ToolsetHandle,
  Tool,
};
