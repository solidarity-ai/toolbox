/**
 * @effect readOnly
 * @idempotent
 */
export default async function tool(
  url: string,
  authorization?: string,
  xApiKey?: string,
): Promise<string> {
  const headers: Record<string, string> = {};
  if (authorization) {
    headers.Authorization = authorization;
  }
  if (xApiKey) {
    headers["X-API-Key"] = xApiKey;
  }

  const response = await fetch(url, Object.keys(headers).length === 0 ? undefined : { headers });
  const body = await response.text();
  return JSON.stringify({
    status: response.status,
    body: body,
  });
}
