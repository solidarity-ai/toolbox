export declare const tools: {
  tickets: {
    /** Create a new support ticket. (irreversible) */
    create(/** The ticket data */ fields: Fields): Ticket;
    /** Retrieve a ticket by ID. (readonly) */
    get(/** The ticket ID to look up */ id: string): Ticket;
    /** List all tickets, optionally filtered by status. (readonly) */
    list(/** Filter by ticket status */ status?: "closed" | "in_progress" | "open" | "resolved"): Ticket[];
    /** Update an existing ticket. (reversible) */
    update(/** The ticket ID to update */ id: string, /** The fields to update */ fields: Fields): Ticket;
  };
};

type Fields = { description?: string; priority?: "high" | "low" | "medium" | "urgent"; tags?: string[]; title?: string };
type Ticket = { /** Name of the assigned agent */ assignee?: string; /** Unique ticket identifier */ id?: string; /** Current ticket status */ status?: "closed" | "in_progress" | "open" | "resolved"; /** Short summary of the issue */ title?: string };
