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

declare namespace githubIssues {
  namespace githubIssues {
    function get(owner: string, repo: string, number: number, token?: string): ToolCallPromise<{ assignees: string[]; body: string; created_at: string; labels: string[]; number: number; state: "closed" | "open"; title: string; updated_at: string }>; // readonly
  }
}