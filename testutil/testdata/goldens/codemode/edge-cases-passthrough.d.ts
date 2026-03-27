type OutputConfig = { columns?: { exclude?: string[]; include?: string[] }; format?: "csv" | "json" | "table" };
type PageInfo = { page?: number; pageSize?: number };
type SearchFilter = { field?: string; op?: "contains" | "eq" | "gt" | "gte" | "lt" | "lte" | "neq"; value?: boolean | number | string };

export declare const tools: {
  edgeCases: {
    /** Process items in batch asynchronously. (irreversible) */
    asyncComplex(/** Array of item IDs to process */ ids: string[], /** Max parallel operations */ concurrency?: number): { errors?: string[]; failed?: number; succeeded?: number };
    /** Search for records with complex filtering, pagination, and output options. (readonly) */
    complex(/** The search query string */ query: string, /** Array of filter criteria to apply */ filters: SearchFilter[], /** Optional tags to narrow results */ tags?: string[], /** Pagination settings */ pagination?: PageInfo, /** Output formatting configuration */ output?: OutputConfig, /** If true, validate the query without executing */ dryRun?: boolean): string;
    /** Permanently delete all records matching the filter. (irreversible) */
    irreversible(/** SQL-like filter expression */ filter: string, /** Must be true to execute */ confirm: boolean): string;
    /** A minimal no-op tool. (readonly) */
    minimal(): string;
    /**
     * Transform data with a configurable pipeline. (readonly)
     * @param pipeline - A sequence of transformation steps to apply.
     *   Each step is executed in order, and the output of one step
     *   becomes the input of the next.
     */
    multilineParam(/** The raw input string to transform */ input: string, pipeline: string[], /** Optional configuration */ options?: { dryRun?: boolean; verbose?: boolean }): string;
    /** Compute the hash of input data. (readonly) */
    namedExport(/** The data to hash */ data: string, /** Hash algorithm to use */ algorithm: "md5" | "sha256" | "sha512"): string;
    /** (irreversible) */
    noDescription(x: number, y: number): number;
    /** Return the current server status. (readonly) */
    noParams(): string;
    /** Process a batch of items with a specified action. (irreversible, idempotent) */
    paramsObject(/** The batch operation parameters */ params: { action?: "archive" | "delete" | "restore"; callbackUrl?: string; continueOnError?: boolean; ids?: string[] }): string;
    /** Create a draft document. (reversible, idempotent) */
    reversible(/** Document title */ title: string, /** Initial content */ content: string, /** Optional categorization tags */ tags?: string[]): string;
  };
};
