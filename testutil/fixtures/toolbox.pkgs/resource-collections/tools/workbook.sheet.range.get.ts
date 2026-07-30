/** Read a range from a sheet in a workbook. */
export default async function tool(
  /** Local workbook path. */
  path: string,
  /** Worksheet name. */
  sheet: string,
  /** First cell in the selected range. */
  start: string,
  /** Last cell in the selected range. */
  end: string,
): Promise<{ workbook: string; sheet: string; start: string; end: string }> {
  return { workbook: path, sheet, start, end };
}
