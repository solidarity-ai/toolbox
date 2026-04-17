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

declare namespace calc {
  namespace calc {
    function add(b: number): ToolCallPromise<number>; // readonly
    function asyncAdd(a: number, b: number): ToolCallPromise<number>; // readonly
    function sub(a: number, b: number): ToolCallPromise<number>; // readonly
  }
}