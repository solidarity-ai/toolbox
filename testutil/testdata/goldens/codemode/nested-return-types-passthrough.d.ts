declare namespace nestedReturnTypes {
  namespace pets {
    // Fetch a single pet by name. The return type is a named interface with no
    // property descriptions, used by only this tool, so the SDK inlines its body —
    // which still references the nested `Mood` alias.
    function get(name: string): ToolCallPromise<{ mood: Mood; name: string }>; // readonly
    // List pets. The inlined result body references the nested `PetSummary` type,
    // which in turn references the `Mood` alias.
    function list(limit?: number): ToolCallPromise<{ pets: PetSummary[]; total: number }>; // readonly
  }

  type Mood = "grumpy" | "happy" | "sleepy";
  type PetSummary = { mood: Mood; name: string };
}