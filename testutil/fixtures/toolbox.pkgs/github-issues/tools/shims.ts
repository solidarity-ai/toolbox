// Runtime shims for QuickJS — provides minimal browser globals that npm
// packages expect when bundled with browser/default export conditions.
// Note: console is provided by the runtime (installConsole), not here.

// Navigator stub — QuickJS may have read-only navigator
try { (globalThis as any).navigator = { userAgent: "toolbox" }; } catch {}

// process stub
(globalThis as any).process = { env: {}, version: "v20.0.0", platform: "linux", arch: "x64" };

// btoa / atob (Base64)
(globalThis as any).btoa = (input: string): string => {
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  let result = "";
  for (let i = 0; i < input.length; i += 3) {
    const a = input.charCodeAt(i);
    const b = i + 1 < input.length ? input.charCodeAt(i + 1) : 0;
    const c = i + 2 < input.length ? input.charCodeAt(i + 2) : 0;
    result += chars[a >> 2];
    result += chars[((a & 3) << 4) | (b >> 4)];
    result += i + 1 < input.length ? chars[((b & 15) << 2) | (c >> 6)] : "=";
    result += i + 2 < input.length ? chars[c & 63] : "=";
  }
  return result;
};

(globalThis as any).atob = (input: string): string => {
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  input = input.replace(/=+$/, "");
  let result = "";
  for (let i = 0; i < input.length; i += 4) {
    const a = chars.indexOf(input[i]);
    const b = chars.indexOf(input[i + 1]);
    const c = chars.indexOf(input[i + 2]);
    const d = chars.indexOf(input[i + 3]);
    result += String.fromCharCode((a << 2) | (b >> 4));
    if (c !== -1) result += String.fromCharCode(((b & 15) << 4) | (c >> 2));
    if (d !== -1) result += String.fromCharCode(((c & 3) << 6) | d);
  }
  return result;
};

// Minimal crypto stub — enough to not crash on import, will fail on actual use
if (!(globalThis as any).crypto) {
  (globalThis as any).crypto = { subtle: {} };
}

// TextEncoder/TextDecoder — QuickJS may not have these
if (typeof (globalThis as any).TextEncoder === "undefined") {
  (globalThis as any).TextEncoder = class TextEncoder {
    encode(input: string): Uint8Array {
      const buf = new Uint8Array(input.length * 3);
      let pos = 0;
      for (let i = 0; i < input.length; i++) {
        let c = input.charCodeAt(i);
        if (c < 0x80) {
          buf[pos++] = c;
        } else if (c < 0x800) {
          buf[pos++] = 0xc0 | (c >> 6);
          buf[pos++] = 0x80 | (c & 0x3f);
        } else {
          buf[pos++] = 0xe0 | (c >> 12);
          buf[pos++] = 0x80 | ((c >> 6) & 0x3f);
          buf[pos++] = 0x80 | (c & 0x3f);
        }
      }
      return buf.slice(0, pos);
    }
  };
}

if (typeof (globalThis as any).TextDecoder === "undefined") {
  (globalThis as any).TextDecoder = class TextDecoder {
    decode(input: Uint8Array): string {
      let result = "";
      for (let i = 0; i < input.length; ) {
        const b = input[i];
        if (b < 0x80) {
          result += String.fromCharCode(b);
          i++;
        } else if ((b & 0xe0) === 0xc0) {
          result += String.fromCharCode(((b & 0x1f) << 6) | (input[i + 1] & 0x3f));
          i += 2;
        } else {
          result += String.fromCharCode(((b & 0x0f) << 12) | ((input[i + 1] & 0x3f) << 6) | (input[i + 2] & 0x3f));
          i += 3;
        }
      }
      return result;
    }
  };
}
