export declare const tools: {
  calc: {
    // Add two numbers. (readonly)
    add(/** The second number */ b: number): string;
    // Add two numbers asynchronously. (readonly)
    asyncAdd(/** The first number */ a: number, /** The second number */ b: number): string;
    // Subtract two numbers. (readonly)
    sub(/** The first number */ a: number, /** The second number */ b: number): string;
  };
};
