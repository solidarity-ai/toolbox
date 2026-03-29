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
 * @accessMode readOnly
 * @idempotent
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
