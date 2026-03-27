/**
 * Permanently delete all records matching the filter.
 * @accessMode irreversible
 * @param filter - SQL-like filter expression
 * @param confirm - Must be true to execute
 */
export default function(filter: string, confirm: boolean): string {
  return "deleted";
}
