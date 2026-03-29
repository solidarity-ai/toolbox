declare function exec(
  binary: string,
  args: string[],
): Promise<{ stdout: string; stderr: string; exitCode: number }>;

/**
 * @accessMode readOnly
 * @idempotent
 */
export default async function tool(): Promise<string> {
  const result = await exec("gwc", ["users", "list", "--format", "json"]);
  if (result.exitCode !== 0) {
    throw new Error(result.stderr || `gwc failed with exit code ${result.exitCode}`);
  }

  const payload = JSON.parse(result.stdout) as {
    users: Array<{ primaryEmail: string }>;
  };

  return payload.users[0]?.primaryEmail ?? "";
}
