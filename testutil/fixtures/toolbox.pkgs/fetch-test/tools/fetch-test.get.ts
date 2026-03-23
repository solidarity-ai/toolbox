export default async function tool(params: { url: string }, ctx: unknown) {
  const response = await fetch(params.url);
  const body = await response.text();
  return JSON.stringify({
    status: response.status,
    body: body,
  });
}
