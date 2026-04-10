import readline from "node:readline";
import type { ChildProcessWithoutNullStreams } from "node:child_process";

interface JsonRpcSuccess<TResult> {
  id?: string;
  result: TResult;
}

interface JsonRpcError {
  id?: string;
  error: {
    message?: string;
  };
}

type JsonRpcMessage<TResult> = JsonRpcSuccess<TResult> | JsonRpcError;

export class RpcClient {
  readonly #child: ChildProcessWithoutNullStreams;
  #closed = false;
  #nextId = 1;
  readonly #pending = new Map<string, { resolve: (result: any) => void; reject: (error: Error) => void }>();
  #stderr = "";
  readonly #exitPromise: Promise<{ code: number | null; signal: NodeJS.Signals | null }>;

  constructor(child: ChildProcessWithoutNullStreams) {
    this.#child = child;
    this.#exitPromise = new Promise((resolve) => {
      child.once("exit", (code, signal) => {
        this.#closed = true;
        this.#failAll(new Error(`toolbox bridge exited with code ${code} signal ${signal}${this.#stderrSuffix()}`));
        resolve({ code, signal });
      });
    });

    child.once("error", (error) => {
      this.#closed = true;
      this.#failAll(error);
    });
    child.stderr.on("data", (chunk: Buffer) => {
      this.#stderr += chunk.toString();
      if (this.#stderr.length > 8192) {
        this.#stderr = this.#stderr.slice(this.#stderr.length - 8192);
      }
    });

    const lines = readline.createInterface({
      input: child.stdout,
      crlfDelay: Infinity,
    });
    lines.on("line", (line) => {
      if (line.trim() === "") {
        return;
      }
      this.#handleLine(line);
    });
  }

  async request<TResult>(method: string, params: Record<string, unknown>): Promise<TResult> {
    if (this.#closed) {
      throw new Error(`toolbox bridge is closed${this.#stderrSuffix()}`);
    }

    const id = `req_${this.#nextId++}`;
    const payload = JSON.stringify({
      jsonrpc: "2.0",
      id,
      method,
      params,
    });

    return new Promise((resolve, reject) => {
      this.#pending.set(id, { resolve, reject });
      this.#child.stdin.write(`${payload}\n`, (error) => {
        if (!error) {
          return;
        }
        this.#pending.delete(id);
        reject(error);
      });
    }) as Promise<TResult>;
  }

  async close(): Promise<void> {
    if (this.#closed) {
      return;
    }
    try {
      await this.request("bridge.shutdown", {});
    } catch {
      // If the bridge is already going away, stdin shutdown below will finish cleanup.
    }
    this.#child.stdin.end();
    await this.#exitPromise;
  }

  #handleLine(line: string): void {
    let message: JsonRpcMessage<unknown>;
    try {
      message = JSON.parse(line) as JsonRpcMessage<unknown>;
    } catch (error) {
      const messageText = error instanceof Error ? error.message : String(error);
      this.#failAll(new Error(`invalid toolbox bridge response: ${messageText}`));
      return;
    }

    if (!message.id) {
      return;
    }

    const pending = this.#pending.get(message.id);
    if (!pending) {
      return;
    }
    this.#pending.delete(message.id);

    if ("error" in message) {
      pending.reject(new Error(message.error.message || "toolbox bridge request failed"));
      return;
    }

    pending.resolve(message.result);
  }

  #failAll(error: Error): void {
    for (const pending of this.#pending.values()) {
      pending.reject(error);
    }
    this.#pending.clear();
  }

  #stderrSuffix(): string {
    if (!this.#stderr) {
      return "";
    }
    return `\nstderr:\n${this.#stderr.trimEnd()}`;
  }
}
