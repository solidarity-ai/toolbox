declare function exec(
  binary: string,
  args: string[],
): Promise<{ stdout: string; stderr: string; exitCode: number }>;

declare const fs: {
  readFileSync(path: string): string;
  writeFileSync(path: string, data: string): void;
};

/**
 * Run a WASM guest that can round-trip, delete, or rename files through the shared VFS.
 * @accessMode irreversible
 */
export default async function tool(action?: "roundtrip" | "delete" | "rename"): Promise<string> {
  const resolvedAction = action ?? "roundtrip";
  const execArgs = resolvedAction === "roundtrip" ? [] : [resolvedAction];
  const result = await exec("vfs-guest", execArgs);
  if (result.exitCode !== 0) {
    return `exec failed: ${result.stderr}`;
  }
  if (resolvedAction !== "roundtrip") {
    return result.stdout;
  }
  // Read the file the WASM guest wrote, via the shared VFS.
  const output = fs.readFileSync("/output.txt");
  return output;
}
