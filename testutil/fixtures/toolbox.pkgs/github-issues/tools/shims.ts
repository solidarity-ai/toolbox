const noop = (..._args: unknown[]) => {};
globalThis.console = { log: noop, warn: noop, error: noop, info: noop, debug: noop } as any;
