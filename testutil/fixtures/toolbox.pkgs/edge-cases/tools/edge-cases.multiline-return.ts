/** Result of a detailed analysis */
interface AnalysisResult {
  /**
   * Overall score from 0 to 100.
   * Higher values indicate better quality.
   */
  score: number;
  /** Short summary of findings */
  summary: string;
  /**
   * Detailed recommendations for improvement.
   * Each entry is a separate actionable item
   * that should be addressed independently.
   */
  recommendations: string[];
}

/**
 * @accessMode readOnly
 */
export default function(input: string): AnalysisResult {
  return { score: 100, summary: "ok", recommendations: [] };
}
