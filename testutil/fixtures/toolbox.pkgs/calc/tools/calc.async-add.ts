/**
 * @accessMode readOnly
 * @idempotent
 */
export default async function tool(a: number, b: number): Promise<string> {
  return String(a + b);
}
