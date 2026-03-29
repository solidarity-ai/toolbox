/** Fields for creating or updating a ticket */
interface TicketFields {
  /** Short summary of the issue */
  title: string;
  /** Detailed description */
  description?: string;
  /** Priority level */
  priority: "low" | "medium" | "high" | "urgent";
  /** Tags for categorization */
  tags?: string[];
}

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
 * @effect reversible
 */
export default function(id: string, fields: TicketFields): Ticket {
  return { id, title: fields.title, status: "open" };
}
