/**
 * Fetches from two URLs and returns both responses.
 * Used to test that different credentials are injected for different hosts.
 * @effect readOnly
 * @idempotent
 */
export default async function tool(url1: string, url2: string): Promise<string> {
  const [resp1, resp2] = await Promise.all([fetch(url1), fetch(url2)]);
  const [body1, body2] = await Promise.all([resp1.text(), resp2.text()]);
  return JSON.stringify({
    response1: { status: resp1.status, body: body1 },
    response2: { status: resp2.status, body: body2 },
  });
}
