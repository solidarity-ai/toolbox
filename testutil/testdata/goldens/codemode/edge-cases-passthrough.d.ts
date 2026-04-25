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

declare namespace edgeCases {
  namespace edgeCases {
    function asyncComplex(ids: string[], concurrency?: number): ToolCallPromise<{ /** Error messages for failed items */ errors: string[]; /** Number of items that failed */ failed: number; /** Number of items successfully processed */ succeeded: number }>; // irreversible
    function complex(query: string, filters: SearchFilter[], tags?: string[], pagination?: PageInfo, output?: OutputConfig, dryRun?: boolean): ToolCallPromise<string>; // readonly
    // Permanently delete all records matching the filter.
    function irreversible(/** SQL-like filter expression */ filter: string, /** Must be true to execute deletion */ confirm: boolean): ToolCallPromise<string>; // irreversible
    function minimal(): ToolCallPromise<string>; // readonly
    function multilineParam(
      // The raw input string to transform
      input: string,
      // A sequence of transformation steps to apply.
      // Each step is executed in order, and the output of one step
      // becomes the input of the next.
      pipeline: string[],
      options?: { dryRun?: boolean; verbose?: boolean }
    ): ToolCallPromise<string>; // readonly
    function multilineReturn(input: string): ToolCallPromise<MultilineReturnResult>; // readonly
    function namedExport(data: string, algorithm: "md5" | "sha256" | "sha512"): ToolCallPromise<string>; // readonly
    function noDescription(x: number, y: number): ToolCallPromise<number>; // readonly
    function noParams(): ToolCallPromise<string>; // readonly
    function paramsObject(params: { /** Action to take on each item */ action: "archive" | "delete" | "restore"; /** Optional callback URL for completion notification */ callbackUrl?: string; /** Whether to continue on individual item failures */ continueOnError?: boolean; /** List of item IDs to process */ ids: string[] }): ToolCallPromise<string>; // irreversible, idempotent
    function reversible(title: string, content: string, tags?: string[]): ToolCallPromise<string>; // reversible, idempotent
  }

  // A paginated item in results
  type PageInfo = { /** Current page number (1-indexed) */ page: number; /** Maximum items per page */ pageSize: number };
  // Filter criteria for searching
  type SearchFilter = { /** Field name to filter on */ field: string; /** Comparison operator */ op: "contains" | "eq" | "gt" | "gte" | "lt" | "lte" | "neq"; /** The value to compare against */ value: boolean | number | string };
  // Nested config with deeply nested objects
  type OutputConfig = { /** Column configuration */ columns?: { exclude?: string[]; include?: string[] }; /** Output format */ format: "csv" | "json" | "table" };
  // Result of a detailed analysis
  interface MultilineReturnResult {
    // Detailed recommendations for improvement.
    // Each entry is a separate actionable item
    // that should be addressed independently.
    recommendations: string[];
    // Overall score from 0 to 100.
    // Higher values indicate better quality.
    score: number;
    // Short summary of findings
    summary: string;
  }
}