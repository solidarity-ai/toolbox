export default async function tool(params: { a: number; b: number }, ctx: unknown) {
  return String(params.a + params.b);
}
