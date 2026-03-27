type Ticket = { assignee?: string; id?: string; status?: "closed" | "in_progress" | "open" | "resolved"; title?: string };

interface CreateResult {
  /** Name of the assigned agent */
  assignee?: string;
  /** Unique ticket identifier */
  id?: string;
  /** Current ticket status */
  status?: "closed" | "in_progress" | "open" | "resolved";
  /** Short summary of the issue */
  title?: string;
}

export declare const tools: {
  tickets: {
    /** Create a new support ticket. (irreversible) */
    create(/** The ticket data */ fields: { description?: string; priority?: "high" | "low" | "medium" | "urgent"; tags?: string[]; title?: string }): CreateResult;
    /** Retrieve a ticket by ID. (readonly) */
    get(/** The ticket ID to look up */ id: string): CreateResult;
    /** List all tickets, optionally filtered by status. (readonly) */
    list(/** Filter by ticket status */ status?: "closed" | "in_progress" | "open" | "resolved"): Ticket[];
    /** Update an existing ticket. (reversible) */
    update(/** The ticket ID to update */ id: string, /** The fields to update */ fields: { description?: string; priority?: "high" | "low" | "medium" | "urgent"; tags?: string[]; title?: string }): CreateResult;
  };
};
