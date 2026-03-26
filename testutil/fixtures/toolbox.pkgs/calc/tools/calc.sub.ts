/**
 * Subtract two numbers.
 * @accessMode readOnly
 * @idempotent
 * @param a - The first number
 * @param b - The second number
 */
export default function tool(a: number, b: number): string {
  return String(a - b);
}
