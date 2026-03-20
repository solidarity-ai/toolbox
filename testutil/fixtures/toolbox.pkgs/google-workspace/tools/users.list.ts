declare function exec(
  binary: string,
  args: string[],
): Promise<{ stdout: string; stderr: string; exitCode: number }>;

export const params = {};
export const metadata = {
  readOnly: true,
  idempotent: true,
  description: "List Google Workspace users",
};

export default async function tool(params: Record<string, never>, ctx: unknown) {
  const result = await exec("gwc", ["users", "list", "--format", "json"]);
  const payload = JSON.parse(result.stdout) as {
    users: Array<{ primaryEmail: string }>;
  };
  return payload.users[0]?.primaryEmail ?? "";
}
