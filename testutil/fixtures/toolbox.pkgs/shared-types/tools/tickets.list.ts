/** A support ticket */
interface Ticket {
  /** Unique ticket identifier */
  id: string;
  /** Short summary of the issue */
  title: string;
  /** Current ticket status */
  status: "open" | "in_progress" | "resolved" | "closed";
  /** Name of the assigned agent */
  assignee?: string;
}

/**
 * List all tickets, optionally filtered by status.
 * @accessMode readOnly
 * @idempotent
 * @param status - Filter by ticket status
 */
export default function(status?: "open" | "in_progress" | "resolved" | "closed"): Ticket[] {
  return [];
}
