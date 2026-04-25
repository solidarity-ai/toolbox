type ToolCallTask<T = unknown> = {
  toolCallId: string;
};

type ToolCallPromise<T> = Promise<T> & {
  toolCallTask: ToolCallTask<T>;
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
      approval: { approvalId?: string };
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
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "cancelled";
      params?: unknown;
    }
  | {
      toolCallId: string;
      toolName: string;
      status: "unknown";
      params?: unknown;
    };

declare function $tool_call<T>(refOrId: ToolCallTask<T> | string): ToolCallView<T>;
declare function $tool_call<T>(ref: ToolCallPromise<T>): ToolCallView<T>;

declare namespace githubIssues {
  namespace githubIssues {
    function get(owner: string, repo: string, number: number, token?: string): ToolCallPromise<{ assignees: string[]; body: string; created_at: string; labels: string[]; number: number; state: "closed" | "open"; title: string; updated_at: string }>; // readonly
  }
}