/** Run Office.js source against a workbook. */
export default async function tool(
  /** Local workbook path. */
  path: string,
  /** Office.js source to execute. */
  code: string,
): Promise<{ workbook: string; code: string }> {
  return { workbook: path, code };
}
