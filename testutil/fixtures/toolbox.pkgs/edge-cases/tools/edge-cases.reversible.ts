/**
 * @effect reversible
 * @idempotent
 */
export default function(title: string, content: string, tags?: string[]): string {
  return "created";
}
