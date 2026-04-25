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

declare namespace calc {
  namespace calc {
    function add(b: number): ToolCallPromise<number>; // readonly
    function asyncAdd(a: number, b: number): ToolCallPromise<number>; // readonly
    function sub(a: number, b: number): ToolCallPromise<number>; // readonly
  }
}