/** A paginated item in results */
interface PageInfo {
  /** Current page number (1-indexed) */
  page: number;
  /** Maximum items per page */
  pageSize: number;
}

/** Filter criteria for searching */
interface SearchFilter {
  /** Field name to filter on */
  field: string;
  /**
   * Comparison operator
   * @default "eq"
   */
  op: "eq" | "neq" | "gt" | "lt" | "gte" | "lte" | "contains";
  /** The value to compare against */
  value: string | number | boolean;
}

/** Nested config with deeply nested objects */
interface OutputConfig {
  /** Output format */
  format: "json" | "csv" | "table";
  /** Column configuration */
  columns?: {
    /** Which fields to include */
    include?: string[];
    /** Which fields to exclude */
    exclude?: string[];
  };
}

/**
 * Search for records with complex filtering, pagination, and output options.
 * @accessMode readOnly
 * @idempotent
 * @param query - The search query string
 * @param filters - Array of filter criteria to apply
 * @param tags - Optional tags to narrow results
 * @param pagination - Pagination settings
 * @param output - Output formatting configuration
 * @param dryRun - If true, validate the query without executing
 */
export default function(
  query: string,
  filters: SearchFilter[],
  tags?: string[],
  pagination?: PageInfo,
  output?: OutputConfig,
  dryRun?: boolean,
): string {
  return JSON.stringify({ query, filters, tags, pagination, output, dryRun });
}
