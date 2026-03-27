/**
 * Create a draft document.
 * @accessMode reversible
 * @idempotent
 * @param title - Document title
 * @param content - Initial content
 * @param tags - Optional categorization tags
 */
export default function(title: string, content: string, tags?: string[]): string {
  return "created";
}
