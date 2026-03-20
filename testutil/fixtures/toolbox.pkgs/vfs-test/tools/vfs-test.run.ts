declare function exec(
  binary: string,
  args: string[],
): Promise<{ stdout: string; stderr: string; exitCode: number }>;

export const params = {};
export const metadata = {
  description: "Run a WASM guest that reads /input.txt and writes /output.txt",
  idempotent: true,
  accessMode: "canDestruct",
};

export default async function tool(params: Record<string, never>, ctx: unknown) {
  const result = await exec("vfs-guest", []);
  return result.stdout;
}
