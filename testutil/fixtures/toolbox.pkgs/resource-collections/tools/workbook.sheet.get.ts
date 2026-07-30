/** Read one sheet in a workbook. */
export default async function tool(
  /** Local workbook path. */
  path: string,
  /** Worksheet name. */
  sheet: string,
): Promise<{ workbook: string; sheet: string }> {
  return { workbook: path, sheet };
}
