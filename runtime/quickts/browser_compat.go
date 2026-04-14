package quickts

import (
	crand "crypto/rand"
	"fmt"

	"github.com/fastschema/qjs"
)

const maxCryptoGetRandomValuesBytes = 65536

// installBrowserCompat injects the browser-target globals that bundled npm
// packages commonly expect in QuickJS. Keep this surface focused on import-
// time/runtime compatibility primitives, not broad Node or Web Crypto emulation.
func installBrowserCompat(rt *qjs.Runtime, host Host) error {
	ctx := rt.Context()

	randomBytes := host.RandomBytes
	if randomBytes == nil {
		randomBytes = defaultRandomBytes
	}

	jsRandomBytes, err := qjs.FuncToJS(ctx, func(size int) ([]byte, error) {
		if size < 0 {
			return nil, fmt.Errorf("invalid random byte count: %d", size)
		}
		data, err := randomBytes(size)
		if err != nil {
			return nil, err
		}
		if data == nil {
			data = []byte{}
		}
		if len(data) != size {
			return nil, fmt.Errorf("random byte provider returned %d bytes, want %d", len(data), size)
		}
		return data, nil
	})
	if err != nil {
		return fmt.Errorf("bind random bytes: %w", err)
	}
	ctx.Global().SetPropertyStr("__toolboxRandomBytes", jsRandomBytes)

	if _, err := rt.Eval("__toolbox_browser_compat.js", qjs.Code(browserCompatJS)); err != nil {
		return fmt.Errorf("install browser compat: %w", err)
	}
	return nil
}

func defaultRandomBytes(n int) ([]byte, error) {
	data := make([]byte, n)
	if n == 0 {
		return data, nil
	}
	if _, err := crand.Read(data); err != nil {
		return nil, err
	}
	return data, nil
}

const browserCompatJS = `
(function() {
  const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  const randomBytes = globalThis.__toolboxRandomBytes;
  try { delete globalThis.__toolboxRandomBytes; } catch (_) {}

  function definePropertyIfMissing(target, name, value) {
    if (typeof target[name] !== "undefined") {
      return;
    }
    try {
      Object.defineProperty(target, name, {
        value: value,
        writable: true,
        enumerable: false,
        configurable: true,
      });
      return;
    } catch (_) {}
    try {
      target[name] = value;
    } catch (_) {}
  }

  function defineGlobalIfMissing(name, value) {
    definePropertyIfMissing(globalThis, name, value);
  }

  function getOrCreateObject(name, factory) {
    if (typeof globalThis[name] === "undefined") {
      defineGlobalIfMissing(name, factory());
    }
    return globalThis[name];
  }

  function makeNamedError(name, message) {
    const err = new Error(message);
    err.name = name;
    return err;
  }

  function isIntegerTypedArray(value) {
    return value instanceof Int8Array ||
      value instanceof Uint8Array ||
      value instanceof Uint8ClampedArray ||
      value instanceof Int16Array ||
      value instanceof Uint16Array ||
      value instanceof Int32Array ||
      value instanceof Uint32Array ||
      (typeof BigInt64Array !== "undefined" && value instanceof BigInt64Array) ||
      (typeof BigUint64Array !== "undefined" && value instanceof BigUint64Array);
  }

  function toUint8Array(value) {
    if (value == null) {
      return new Uint8Array(0);
    }
    if (value instanceof Uint8Array) {
      return value;
    }
    if (value instanceof ArrayBuffer) {
      return new Uint8Array(value);
    }
    if (ArrayBuffer.isView(value)) {
      return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
    }
    throw new TypeError("Failed to execute 'decode': expected an ArrayBuffer or ArrayBufferView");
  }

  function utf8Encode(input) {
    const out = [];
    for (const ch of String(input)) {
      const code = ch.codePointAt(0);
      if (code <= 0x7F) {
        out.push(code);
      } else if (code <= 0x7FF) {
        out.push(0xC0 | (code >> 6));
        out.push(0x80 | (code & 0x3F));
      } else if (code <= 0xFFFF) {
        out.push(0xE0 | (code >> 12));
        out.push(0x80 | ((code >> 6) & 0x3F));
        out.push(0x80 | (code & 0x3F));
      } else {
        out.push(0xF0 | (code >> 18));
        out.push(0x80 | ((code >> 12) & 0x3F));
        out.push(0x80 | ((code >> 6) & 0x3F));
        out.push(0x80 | (code & 0x3F));
      }
    }
    return new Uint8Array(out);
  }

  function appendReplacement(result) {
    return result + "\uFFFD";
  }

  function utf8Decode(input) {
    const bytes = toUint8Array(input);
    let result = "";

    for (let i = 0; i < bytes.length; ) {
      const b0 = bytes[i];
      if (b0 < 0x80) {
        result += String.fromCodePoint(b0);
        i++;
        continue;
      }

      if ((b0 & 0xE0) === 0xC0) {
        if (i + 1 >= bytes.length) {
          result = appendReplacement(result);
          break;
        }
        const b1 = bytes[i + 1];
        if ((b1 & 0xC0) !== 0x80) {
          result = appendReplacement(result);
          i++;
          continue;
        }
        const code = ((b0 & 0x1F) << 6) | (b1 & 0x3F);
        if (code < 0x80) {
          result = appendReplacement(result);
          i++;
          continue;
        }
        result += String.fromCodePoint(code);
        i += 2;
        continue;
      }

      if ((b0 & 0xF0) === 0xE0) {
        if (i + 2 >= bytes.length) {
          result = appendReplacement(result);
          break;
        }
        const b1 = bytes[i + 1];
        const b2 = bytes[i + 2];
        if ((b1 & 0xC0) !== 0x80 || (b2 & 0xC0) !== 0x80) {
          result = appendReplacement(result);
          i++;
          continue;
        }
        const code = ((b0 & 0x0F) << 12) | ((b1 & 0x3F) << 6) | (b2 & 0x3F);
        if (code < 0x800 || (code >= 0xD800 && code <= 0xDFFF)) {
          result = appendReplacement(result);
          i++;
          continue;
        }
        result += String.fromCodePoint(code);
        i += 3;
        continue;
      }

      if ((b0 & 0xF8) === 0xF0) {
        if (i + 3 >= bytes.length) {
          result = appendReplacement(result);
          break;
        }
        const b1 = bytes[i + 1];
        const b2 = bytes[i + 2];
        const b3 = bytes[i + 3];
        if ((b1 & 0xC0) !== 0x80 || (b2 & 0xC0) !== 0x80 || (b3 & 0xC0) !== 0x80) {
          result = appendReplacement(result);
          i++;
          continue;
        }
        const code = ((b0 & 0x07) << 18) | ((b1 & 0x3F) << 12) | ((b2 & 0x3F) << 6) | (b3 & 0x3F);
        if (code < 0x10000 || code > 0x10FFFF) {
          result = appendReplacement(result);
          i++;
          continue;
        }
        result += String.fromCodePoint(code);
        i += 4;
        continue;
      }

      result = appendReplacement(result);
      i++;
    }

    return result;
  }

  function btoa(input) {
    const text = String(input);
    for (let i = 0; i < text.length; i++) {
      if (text.charCodeAt(i) > 0xFF) {
        throw new TypeError("Failed to execute 'btoa': string contains characters outside of the Latin1 range");
      }
    }

    let result = "";
    for (let i = 0; i < text.length; i += 3) {
      const a = text.charCodeAt(i);
      const b = i + 1 < text.length ? text.charCodeAt(i + 1) : 0;
      const c = i + 2 < text.length ? text.charCodeAt(i + 2) : 0;

      result += base64Chars[a >> 2];
      result += base64Chars[((a & 3) << 4) | (b >> 4)];
      result += i + 1 < text.length ? base64Chars[((b & 15) << 2) | (c >> 6)] : "=";
      result += i + 2 < text.length ? base64Chars[c & 63] : "=";
    }
    return result;
  }

  function atob(input) {
    let text = String(input).replace(/[\t\n\f\r ]+/g, "");
    if (text.length % 4 === 1) {
      throw new TypeError("Failed to execute 'atob': invalid base64 input");
    }
    if (!/^[A-Za-z0-9+/]*={0,2}$/.test(text)) {
      throw new TypeError("Failed to execute 'atob': invalid base64 input");
    }
    if (text.length % 4 !== 0) {
      text += "=".repeat(4 - (text.length % 4));
    }

    let result = "";
    for (let i = 0; i < text.length; i += 4) {
      const a = base64Chars.indexOf(text[i]);
      const b = base64Chars.indexOf(text[i + 1]);
      const cChar = text[i + 2];
      const dChar = text[i + 3];
      const c = cChar === "=" ? -1 : base64Chars.indexOf(cChar);
      const d = dChar === "=" ? -1 : base64Chars.indexOf(dChar);

      if (a === -1 || b === -1 || c < -1 || d < -1) {
        throw new TypeError("Failed to execute 'atob': invalid base64 input");
      }

      result += String.fromCharCode((a << 2) | (b >> 4));
      if (cChar !== "=") {
        result += String.fromCharCode(((b & 15) << 4) | (c >> 2));
      }
      if (dChar !== "=") {
        result += String.fromCharCode(((c & 3) << 6) | d);
      }
    }
    return result;
  }

  function getRandomValues(view) {
    if (!isIntegerTypedArray(view)) {
      throw new TypeError("Failed to execute 'getRandomValues': expected an integer TypedArray");
    }
    if (view.byteLength > 65536) {
      throw makeNamedError("QuotaExceededError", "Failed to execute 'getRandomValues': byteLength exceeds 65536");
    }
    const bytes = new Uint8Array(randomBytes(view.byteLength));
    const target = new Uint8Array(view.buffer, view.byteOffset, view.byteLength);
    target.set(bytes);
    return view;
  }

  function randomUUID() {
    const bytes = getRandomValues(new Uint8Array(16));
    bytes[6] = (bytes[6] & 0x0F) | 0x40;
    bytes[8] = (bytes[8] & 0x3F) | 0x80;
    const hex = [];
    for (let i = 0; i < bytes.length; i++) {
      hex.push((bytes[i] + 0x100).toString(16).slice(1));
    }
    return (
      hex[0] + hex[1] + hex[2] + hex[3] + "-" +
      hex[4] + hex[5] + "-" +
      hex[6] + hex[7] + "-" +
      hex[8] + hex[9] + "-" +
      hex[10] + hex[11] + hex[12] + hex[13] + hex[14] + hex[15]
    );
  }

  class TextEncoder {
    get encoding() { return "utf-8"; }
    encode(input) { return utf8Encode(input); }
  }

  class TextDecoder {
    constructor() {}
    get encoding() { return "utf-8"; }
    decode(input) {
      if (input == null) {
        return "";
      }
      return utf8Decode(input);
    }
  }

  defineGlobalIfMissing("navigator", { userAgent: "toolbox" });
  defineGlobalIfMissing("TextEncoder", TextEncoder);
  defineGlobalIfMissing("TextDecoder", TextDecoder);
  defineGlobalIfMissing("btoa", btoa);
  defineGlobalIfMissing("atob", atob);

  const crypto = getOrCreateObject("crypto", function() { return {}; });
  if (crypto && (typeof crypto === "object" || typeof crypto === "function")) {
    definePropertyIfMissing(crypto, "getRandomValues", getRandomValues);
    definePropertyIfMissing(crypto, "randomUUID", randomUUID);
  }
})();
`
