declare namespace resource_intrinsic_collisions {
  const workbook: {
    (path: string): {
      // Select and describe a workbook.
      get(): ToolCallPromise<{ path: string }>; // readonly
    };
    // Call the domain collection operation.
    call(): ToolCallPromise<string>; // irreversible
    /** Original function property displaced by call. */
    _call(thisArg: unknown, ...args: unknown[]): unknown;
    // Count available workbooks.
    length(): ToolCallPromise<number>; // irreversible
    /** Original function property displaced by length. */
    _length(): number;
    // Return the collection's domain name.
    name(): ToolCallPromise<string>; // irreversible
    /** Original function property displaced by name. */
    _name(): string;
    // Return prototype information from the domain.
    prototype(): ToolCallPromise<string>; // irreversible
    /** Original function property displaced by prototype. */
    _prototype(...args: unknown[]): unknown;
  };
}