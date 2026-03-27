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
 * @accessMode readOnly
 * @idempotent
 */
export default function(id: string): Ticket {
  return { id, title: "Example", status: "open" };
}
