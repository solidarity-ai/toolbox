declare namespace resource_collections {
  const workbook: {
    (path: string): {
      officejs: {
        // Run Office.js source against a workbook.
        run(code: string): ToolCallPromise<{ code: string; workbook: string }>; // irreversible
      };
      sheet: {
        (sheet: string): {
          // Read one sheet in a workbook.
          get(): ToolCallPromise<{ sheet: string; workbook: string }>; // readonly
          range: {
            (start: string, end: string): {
              // Read a range from a sheet in a workbook.
              get(): ToolCallPromise<{ end: string; sheet: string; start: string; workbook: string }>; // readonly
            };
          };
        };
        // List sheets in a workbook.
        list(): ToolCallPromise<{ sheets: string[]; workbook: string }>; // readonly
      };
    };
  };
}