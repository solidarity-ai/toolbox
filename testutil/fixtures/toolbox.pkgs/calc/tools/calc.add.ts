/**
 * @effect readOnly
 * @idempotent
 */
export default function tool(a: number, b: number): number {
  return a + b;
}
