/**
 * @accessMode readOnly
 * @idempotent
 */
export default async function tool(a: number, b: number): Promise<number> {
  return a + b;
}
