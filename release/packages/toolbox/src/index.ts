import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";

import { findBinaryPath } from "./lib/platform.js";
import { RpcClient } from "./lib/rpc-client.js";

type JsonObject = Record<string, unknown>;

interface ToolDescriptor {
  name: string;
  description?: string;
  params_schema?: JsonObject | null;
}

interface ComposeToolsetResult {
  toolset_id: string;
  tools: ToolDescriptor[];
}

interface ToolInvokeResult {
  content: unknown;
}

interface SystemVersionResult {
  version: string;
}

export interface ToolsetFile {
  packages?: Record<string, string>;
  tools?: Array<Record<string, unknown>>;
  [key: string]: unknown;
}

export interface CreateClientOptions {
  binaryPath?: string;
  cwd?: string;
  env?: NodeJS.ProcessEnv;
}

export class Tool {
  readonly #client: RpcClient;
  readonly #toolsetId: string;

  readonly name: string;
  readonly description: string;
  readonly paramsSchema: JsonObject | null;

  constructor(client: RpcClient, toolsetId: string, descriptor: ToolDescriptor) {
    this.#client = client;
    this.#toolsetId = toolsetId;
    this.name = descriptor.name;
    this.description = descriptor.description || "";
    this.paramsSchema = descriptor.params_schema || null;
  }

  async invoke(params: JsonObject = {}): Promise<unknown> {
    const result = await this.#client.request<ToolInvokeResult>("tool.invoke", {
      toolset_id: this.#toolsetId,
      tool_name: this.name,
      params,
    });
    return result.content;
  }
}

export class ToolsetHandle {
  readonly #client: RpcClient;
  #closed = false;

  readonly id: string;
  readonly tools: Tool[];

  constructor(client: RpcClient, id: string, descriptors: ToolDescriptor[]) {
    this.#client = client;
    this.id = id;
    this.tools = descriptors.map((descriptor) => new Tool(client, id, descriptor));
  }

  async close(): Promise<void> {
    if (this.#closed) {
      return;
    }
    this.#closed = true;
    await this.#client.request("toolset.close", { toolset_id: this.id });
  }
}

export class ToolboxClient {
  readonly #rpcClient: RpcClient;

  readonly toolsetfile: {
    load: (filePath: string) => Promise<ToolsetFile>;
    write: (filePath: string, toolset: ToolsetFile) => Promise<void>;
    edit: (filePath: string, mutator: (toolset: ToolsetFile) => ToolsetFile | Promise<ToolsetFile | undefined> | undefined) => Promise<ToolsetFile>;
  };

  readonly toolset: {
    compose: (input: JsonObject) => Promise<ToolsetHandle>;
  };

  constructor(rpcClient: RpcClient) {
    this.#rpcClient = rpcClient;
    this.toolsetfile = {
      load: async (filePath) => this.#rpcClient.request<ToolsetFile>("toolsetfile.load", { path: filePath }),
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
        const result = await this.#rpcClient.request<ComposeToolsetResult>("toolset.compose", input);
        return new ToolsetHandle(this.#rpcClient, result.toolset_id, result.tools);
      },
    };
  }

  async version(): Promise<string> {
    const result = await this.#rpcClient.request<SystemVersionResult>("system.version", {});
    return result.version;
  }

  async close(): Promise<void> {
    await this.#rpcClient.close();
  }
}

export function findBinary(options: CreateClientOptions = {}): string {
  return findBinaryPath(options);
}

export async function createClient(options: CreateClientOptions = {}): Promise<ToolboxClient> {
  const binaryPath = findBinary(options);
  const child: ChildProcessWithoutNullStreams = spawn(binaryPath, ["_sdkbridge", "serve-stdio"], {
    cwd: options.cwd,
    env: { ...process.env, ...options.env },
    stdio: ["pipe", "pipe", "pipe"],
  });
  const rpcClient = new RpcClient(child);
  await rpcClient.request("system.ping", {});
  return new ToolboxClient(rpcClient);
}
