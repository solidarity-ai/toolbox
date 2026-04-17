type ToolCallTask<T = unknown> = {
  toolCallId: string;
};

type ToolCallPromise<T> = Promise<T> & {
  task: ToolCallTask<T>;
};

type ToolCallView<T = unknown> =
  | {
      toolCallId: string;
      toolName: string;
      status: "started";
      params?: unknown;
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "needsApproval";
      params?: unknown;
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "success";
      params?: unknown;
      result: T;
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "failed";
      params?: unknown;
      error: unknown;
    };

declare function $tool_call<T>(ref: ToolCallPromise<T> | ToolCallTask<T>): ToolCallView<T>;

declare namespace sharedTypes {
  namespace tickets {
    function create(fields: Fields): ToolCallPromise<Ticket>; // irreversible
    function get(id: string): ToolCallPromise<Ticket>; // readonly
    function list(status?: "closed" | "in_progress" | "open" | "resolved"): ToolCallPromise<Ticket[]>; // readonly
    function update(id: string, fields: Fields): ToolCallPromise<Ticket>; // reversible
  }

  type Fields = { description?: string; priority: "high" | "low" | "medium" | "urgent"; tags?: string[]; title: string };
  // A support ticket
  type Ticket = { /** Name of the assigned agent */ assignee?: string; /** Unique ticket identifier */ id: string; /** Current ticket status */ status: "closed" | "in_progress" | "open" | "resolved"; /** Short summary of the issue */ title: string };
}