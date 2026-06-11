type Mood = "happy" | "grumpy" | "sleepy";

interface Pet {
  name: string;
  mood: Mood;
}

/**
 * Fetch a single pet by name. The return type is a named interface with no
 * property descriptions, used by only this tool, so the SDK inlines its body —
 * which still references the nested `Mood` alias.
 * @effect readOnly
 */
export default function tool(name: string): Pet {
  return { name, mood: "happy" };
}
