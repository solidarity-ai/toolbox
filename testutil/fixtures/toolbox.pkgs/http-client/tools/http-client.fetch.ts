declare function exec(
  binary: string,
  args: string[],
): Promise<{ stdout: string; stderr: string; exitCode: number }>;

/**
 * Fetch a URL via HTTP GET using a wasip2 WASM component.
 * @accessMode readOnly
 * @idempotent
 */
export default async function tool(): Promise<string> {
  const result = await exec("http-client", []);
  if (result.exitCode !== 0) {
    throw new Error(result.stderr || `http-client failed with exit code ${result.exitCode}`);
  }
  return result.stdout;
}
