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
 * Retrieve a ticket by ID.
 * @accessMode readOnly
 * @idempotent
 * @param id - The ticket ID to look up
 */
export default function(id: string): Ticket {
  return { id, title: "Example", status: "open" };
}
