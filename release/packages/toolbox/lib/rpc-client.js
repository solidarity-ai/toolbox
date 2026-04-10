const readline = require("node:readline");

class RpcClient {
  constructor(child) {
    this.#child = child;
    this.#stderr = "";
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
    child.stderr.on("data", (chunk) => {
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

  #child;
  #closed = false;
  #nextId = 1;
  #pending = new Map();
  #stderr = "";
  #exitPromise;

  async request(method, params) {
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
    });
  }

  async close() {
    if (this.#closed) {
      return;
    }
    try {
      await this.request("bridge.shutdown", {});
    } catch (_) {
      // If the bridge is already going away, stdin shutdown below will finish cleanup.
    }
    this.#child.stdin.end();
    await this.#exitPromise;
  }

  #handleLine(line) {
    let message;
    try {
      message = JSON.parse(line);
    } catch (error) {
      this.#failAll(new Error(`invalid toolbox bridge response: ${error.message}`));
      return;
    }

    const pending = this.#pending.get(message.id);
    if (!pending) {
      return;
    }
    this.#pending.delete(message.id);

    if (message.error) {
      pending.reject(new Error(message.error.message || "toolbox bridge request failed"));
      return;
    }

    pending.resolve(message.result);
  }

  #failAll(error) {
    for (const pending of this.#pending.values()) {
      pending.reject(error);
    }
    this.#pending.clear();
  }

  #stderrSuffix() {
    if (!this.#stderr) {
      return "";
    }
    return `\nstderr:\n${this.#stderr.trimEnd()}`;
  }
}

module.exports = {
  RpcClient,
};
