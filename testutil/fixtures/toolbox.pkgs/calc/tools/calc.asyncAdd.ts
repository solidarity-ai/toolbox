/**
 * Add two numbers asynchronously.
 * @accessMode readOnly
 * @idempotent
 * @param a - The first number
 * @param b - The second number
 */
export default async function tool(a: number, b: number): Promise<string> {
  return String(a + b);
}
