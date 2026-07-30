/** List sheets in a workbook. */
export default async function tool(
  /** Local workbook path. */
  path: string,
): Promise<{ workbook: string; sheets: string[] }> {
  return { workbook: path, sheets: ["Forecast", "Actuals"] };
}
