declare namespace calc {
  namespace calc {
    function add(a: 42, b: number): ToolCallPromise<number>; // readonly
    function asyncAdd(a: number, b: number): ToolCallPromise<number>; // readonly
    function sub(a: number, b: number): ToolCallPromise<number>; // readonly
  }
}