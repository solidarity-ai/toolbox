/**
 * @accessMode readOnly
 * @idempotent
 */
export default async function tool(url: string): Promise<string> {
  const response = await fetch(url);
  const body = await response.text();
  return JSON.stringify({
    status: response.status,
    body: body,
  });
}
