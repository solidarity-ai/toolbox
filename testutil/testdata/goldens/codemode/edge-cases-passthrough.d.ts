export declare const tools: {
  edgeCases: {
    // Process items in batch asynchronously. (irreversible)
    asyncComplex(/** Array of item IDs to process */ ids: string[], /** Max parallel operations */ concurrency?: number): { /** Error messages for failed items */ errors?: string[]; /** Number of items that failed */ failed?: number; /** Number of items successfully processed */ succeeded?: number };
    // Search for records with complex filtering, pagination, and output options. (readonly)
    complex(/** The search query string */ query: string, /** Array of filter criteria to apply */ filters: SearchFilter[], /** Optional tags to narrow results */ tags?: string[], /** Pagination settings */ pagination?: PageInfo, /** Output formatting configuration */ output?: OutputConfig, /** If true, validate the query without executing */ dryRun?: boolean): string;
    // Permanently delete all records matching the filter. (irreversible)
    irreversible(/** SQL-like filter expression */ filter: string, /** Must be true to execute */ confirm: boolean): string;
    // A minimal no-op tool. (readonly)
    minimal(): string;
    /**
     * Transform data with a configurable pipeline. (readonly)
     * @param pipeline - A sequence of transformation steps to apply.
     *   Each step is executed in order, and the output of one step
     *   becomes the input of the next.
     */
    multilineParam(/** The raw input string to transform */ input: string, pipeline: string[], /** Optional configuration */ options?: { dryRun?: boolean; verbose?: boolean }): string;
    // Run a detailed analysis on the input data. (readonly)
    multilineReturn(/** The data to analyze */ input: string): MultilineReturnResult;
    // Compute the hash of input data. (readonly)
    namedExport(/** The data to hash */ data: string, /** Hash algorithm to use */ algorithm: "md5" | "sha256" | "sha512"): string;
    // (irreversible)
    noDescription(x: number, y: number): number;
    // Return the current server status. (readonly)
    noParams(): string;
    // Process a batch of items with a specified action. (irreversible, idempotent)
    paramsObject(/** The batch operation parameters */ params: { action?: "archive" | "delete" | "restore"; callbackUrl?: string; continueOnError?: boolean; ids?: string[] }): string;
    // Create a draft document. (reversible, idempotent)
    reversible(/** Document title */ title: string, /** Initial content */ content: string, /** Optional categorization tags */ tags?: string[]): string;
  };
};

// A paginated item in results
type PageInfo = { /** Current page number (1-indexed) */ page?: number; /** Maximum items per page */ pageSize?: number };
// Filter criteria for searching
type SearchFilter = { /** Field name to filter on */ field?: string; /** Comparison operator */ op?: "contains" | "eq" | "gt" | "gte" | "lt" | "lte" | "neq"; /** The value to compare against */ value?: boolean | number | string };
// Nested config with deeply nested objects
type OutputConfig = { /** Column configuration */ columns?: { exclude?: string[]; include?: string[] }; /** Output format */ format?: "csv" | "json" | "table" };
// Run a detailed analysis on the input data.
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
