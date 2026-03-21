declare function exec(
  binary: string,
  args: string[],
): Promise<{ stdout: string; stderr: string; exitCode: number }>;

export const params = {};
export const metadata = {
  description: "Fetch a URL via HTTP GET using a wasip2 WASM component",
  idempotent: true,
  accessMode: "readOnly",
};

export default async function tool(params: Record<string, never>, ctx: unknown) {
  const result = await exec("http-client", []);
  if (result.exitCode !== 0) {
    throw new Error(result.stderr || `http-client failed with exit code ${result.exitCode}`);
  }
  return result.stdout;
}
