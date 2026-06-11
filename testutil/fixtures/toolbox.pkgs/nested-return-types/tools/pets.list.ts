type Mood = "happy" | "grumpy" | "sleepy";

interface PetSummary {
  name: string;
  mood: Mood;
}

interface PetListResult {
  total: number;
  pets: PetSummary[];
}

/**
 * List pets. The inlined result body references the nested `PetSummary` type,
 * which in turn references the `Mood` alias.
 * @effect readOnly
 */
export default function tool(limit?: number): PetListResult {
  return { total: 0, pets: [] };
}
