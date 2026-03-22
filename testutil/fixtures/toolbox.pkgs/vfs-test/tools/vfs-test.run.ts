declare function exec(
  binary: string,
  args: string[],
): Promise<{ stdout: string; stderr: string; exitCode: number }>;

declare const fs: {
  readFileSync(path: string): string;
  writeFileSync(path: string, data: string): void;
};

export const params = {};
export const metadata = {
  description: "Run a WASM guest that can round-trip, delete, or rename files through the shared VFS",
  idempotent: true,
  accessMode: "canDestruct",
};

type ToolParams = {
  action?: "roundtrip" | "delete" | "rename";
};

export default async function tool(params: ToolParams, ctx: unknown) {
  const action = params.action ?? "roundtrip";
  const execArgs = action === "roundtrip" ? [] : [action];
  const result = await exec("vfs-guest", execArgs);
  if (result.exitCode !== 0) {
    return `exec failed: ${result.stderr}`;
  }
  if (action !== "roundtrip") {
    return result.stdout;
  }
  // Read the file the WASM guest wrote, via the shared VFS.
  const output = fs.readFileSync("/output.txt");
  return output;
}
