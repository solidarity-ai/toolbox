export declare const tools: {
  edgeCases: {
    // (irreversible)
    asyncComplex(ids: string[], concurrency?: number): { /** Error messages for failed items */ errors?: string[]; /** Number of items that failed */ failed?: number; /** Number of items successfully processed */ succeeded?: number };
    // (readonly)
    complex(query: string, filters: SearchFilter[], tags?: string[], pagination?: PageInfo, output?: OutputConfig, dryRun?: boolean): string;
    // Permanently delete all records matching the filter. (irreversible)
    irreversible(/** SQL-like filter expression */ filter: string, /** Must be true to execute deletion */ confirm: boolean): string;
    // (readonly)
    minimal(): string;
    // (readonly)
    multilineParam(
      // The raw input string to transform
      input: string,
      // A sequence of transformation steps to apply.
      // Each step is executed in order, and the output of one step
      // becomes the input of the next.
      pipeline: string[],
      options?: { dryRun?: boolean; verbose?: boolean }
    ): string;
    // (readonly)
    multilineReturn(input: string): MultilineReturnResult;
    // (readonly)
    namedExport(data: string, algorithm: "md5" | "sha256" | "sha512"): string;
    // (readonly)
    noDescription(x: number, y: number): number;
    // (readonly)
    noParams(): string;
    // (irreversible, idempotent)
    paramsObject(params: { /** Action to take on each item */ action?: "archive" | "delete" | "restore"; /** Optional callback URL for completion notification */ callbackUrl?: string; /** Whether to continue on individual item failures */ continueOnError?: boolean; /** List of item IDs to process */ ids?: string[] }): string;
    // (reversible, idempotent)
    reversible(title: string, content: string, tags?: string[]): string;
  };
};

// A paginated item in results
type PageInfo = { /** Current page number (1-indexed) */ page?: number; /** Maximum items per page */ pageSize?: number };
// Filter criteria for searching
type SearchFilter = { /** Field name to filter on */ field?: string; /** Comparison operator */ op?: "contains" | "eq" | "gt" | "gte" | "lt" | "lte" | "neq"; /** The value to compare against */ value?: boolean | number | string };
// Nested config with deeply nested objects
type OutputConfig = { /** Column configuration */ columns?: { exclude?: string[]; include?: string[] }; /** Output format */ format?: "csv" | "json" | "table" };
interface MultilineReturnResult {
  // Detailed recommendations for improvement.
  // Each entry is a separate actionable item
  // that should be addressed independently.
  recommendations?: string[];
  // Overall score from 0 to 100.
  // Higher values indicate better quality.
  score?: number;
  // Short summary of findings
  summary?: string;
}
