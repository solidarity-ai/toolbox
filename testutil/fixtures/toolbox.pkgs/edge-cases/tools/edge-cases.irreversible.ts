/**
 * Permanently delete all records matching the filter.
 * @effect irreversible
 * @param filter - SQL-like filter expression
 * @param confirm - Must be true to execute deletion
 */
export default function(filter: string, confirm: boolean): string {
  return "deleted";
}
