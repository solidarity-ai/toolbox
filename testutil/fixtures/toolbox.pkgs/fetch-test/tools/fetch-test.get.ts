/**
 * Fetch a URL and return the response.
 * @accessMode readOnly
 * @idempotent
 * @param url - The URL to fetch
 */
export default async function tool(url: string): Promise<string> {
  const response = await fetch(url);
  const body = await response.text();
  return JSON.stringify({
    status: response.status,
    body: body,
  });
}
