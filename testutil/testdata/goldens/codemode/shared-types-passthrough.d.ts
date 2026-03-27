export declare const tools: {
  tickets: {
    // (irreversible)
    create(fields: Fields): Ticket;
    // (readonly)
    get(id: string): Ticket;
    // (readonly)
    list(status?: "closed" | "in_progress" | "open" | "resolved"): Ticket[];
    // (reversible)
    update(id: string, fields: Fields): Ticket;
  };
};

type Fields = { description?: string; priority?: "high" | "low" | "medium" | "urgent"; tags?: string[]; title?: string };
// A support ticket
type Ticket = { /** Name of the assigned agent */ assignee?: string; /** Unique ticket identifier */ id?: string; /** Current ticket status */ status?: "closed" | "in_progress" | "open" | "resolved"; /** Short summary of the issue */ title?: string };
