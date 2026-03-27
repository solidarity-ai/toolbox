/**
 * @accessMode readOnly
 * @idempotent
 */
export default function tool(a: number, b: number): string {
  return String(a - b);
}
