declare function __invokeTool<T>(toolName: string, args: unknown): T;
export const tools = {
  calc: {
    add(args: { a?: number; b?: number }): string { return __invokeTool<string>("calc.add", args); },
    asyncAdd(args: { a?: number; b?: number }): string { return __invokeTool<string>("calc.asyncAdd", args); },
    sub(args: { a?: number; b?: number }): string { return __invokeTool<string>("calc.sub", args); },
  },
};
