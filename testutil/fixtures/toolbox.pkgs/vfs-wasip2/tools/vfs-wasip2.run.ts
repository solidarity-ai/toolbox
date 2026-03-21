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
  description: "Run a wasip2 WASM guest that reads /input.txt and writes /output.txt, then read the output via fs",
  idempotent: true,
  accessMode: "canDestruct",
};

export default async function tool(params: Record<string, never>, ctx: unknown) {
  const result = await exec("vfs-guest", []);
  if (result.exitCode !== 0) {
    return `exec failed: ${result.stderr}`;
  }
  // Read the file the WASM guest wrote, via the shared VFS.
  const output = fs.readFileSync("/output.txt");
  return output;
}
