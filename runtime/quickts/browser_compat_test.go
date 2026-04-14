package quickts

import (
	"strings"
	"testing"

	"github.com/fastschema/qjs"
)

func TestInstallBrowserCompat_ProvidesCommonGlobals(t *testing.T) {
	rt := newBrowserCompatRuntime(t)

	if err := installBrowserCompat(rt, Host{RandomBytes: sequentialRandomBytes}); err != nil {
		t.Fatalf("installBrowserCompat: %v", err)
	}

	got := evalBrowserCompatString(t, rt, `(() => {
		const words = new Uint16Array(2);
		const sameWords = globalThis.crypto.getRandomValues(words) === words;
		const sameBytes = (() => {
			const bytes = new Uint8Array(4);
			return globalThis.crypto.getRandomValues(bytes) === bytes;
		})();
		return JSON.stringify({
			hasNavigatorUserAgent: typeof globalThis.navigator.userAgent === "string" && globalThis.navigator.userAgent.length > 0,
			textRoundTrip: new TextDecoder().decode(new TextEncoder().encode("A€😀")),
			btoa: globalThis.btoa("Man"),
			atob: globalThis.atob("TWE="),
			bytes: Array.from(globalThis.crypto.getRandomValues(new Uint8Array(4))),
			wordBytes: Array.from(new Uint8Array(words.buffer)),
			sameBytes,
			sameWords,
			uuid: globalThis.crypto.randomUUID(),
			hasSubtle: "subtle" in globalThis.crypto,
		});
	})()`)
	want := `{"hasNavigatorUserAgent":true,"textRoundTrip":"A€😀","btoa":"TWFu","atob":"Ma","bytes":[0,1,2,3],"wordBytes":[0,1,2,3],"sameBytes":true,"sameWords":true,"uuid":"00010203-0405-4607-8809-0a0b0c0d0e0f","hasSubtle":false}`
	if got != want {
		t.Fatalf("browser compat mismatch:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestInstallBrowserCompat_DoesNotOverwriteExistingGlobals(t *testing.T) {
	rt := newBrowserCompatRuntime(t)

	if _, err := rt.Eval("__preset.js", qjs.Code(`
globalThis.TextEncoder = class TextEncoder {
  encode() { return new Uint8Array([9, 9, 9]); }
};
globalThis.TextDecoder = class TextDecoder {
  decode() { return "custom-text"; }
};
globalThis.btoa = function btoa() { return "custom-btoa"; };
globalThis.atob = function atob() { return "custom-atob"; };
globalThis.crypto = {
  getRandomValues(value) {
    if (typeof value.fill === "function") value.fill(255);
    return value;
  },
  randomUUID() { return "custom-uuid"; },
};
`)); err != nil {
		t.Fatalf("preset globals: %v", err)
	}

	if err := installBrowserCompat(rt, Host{RandomBytes: sequentialRandomBytes}); err != nil {
		t.Fatalf("installBrowserCompat: %v", err)
	}

	got := evalBrowserCompatString(t, rt, `(() => {
		const bytes = new Uint8Array(4);
		return JSON.stringify({
			textRoundTrip: new TextDecoder().decode(new TextEncoder().encode("ignored")),
			btoa: globalThis.btoa("ignored"),
			atob: globalThis.atob("ignored"),
			bytes: Array.from(globalThis.crypto.getRandomValues(bytes)),
			uuid: globalThis.crypto.randomUUID(),
		});
	})()`)
	want := `{"textRoundTrip":"custom-text","btoa":"custom-btoa","atob":"custom-atob","bytes":[255,255,255,255],"uuid":"custom-uuid"}`
	if got != want {
		t.Fatalf("expected existing globals to win:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestInstallBrowserCompat_GetRandomValuesRejectsInvalidInputs(t *testing.T) {
	rt := newBrowserCompatRuntime(t)

	if err := installBrowserCompat(rt, Host{RandomBytes: sequentialRandomBytes}); err != nil {
		t.Fatalf("installBrowserCompat: %v", err)
	}

	got := evalBrowserCompatString(t, rt, `(() => {
		const out = {};
		try {
			globalThis.crypto.getRandomValues(new Float32Array(1));
			out.float = "no error";
		} catch (err) {
			out.float = String(err.name) + ": " + String(err.message);
		}
		try {
			globalThis.crypto.getRandomValues(new DataView(new ArrayBuffer(4)));
			out.dataView = "no error";
		} catch (err) {
			out.dataView = String(err.name) + ": " + String(err.message);
		}
		try {
			globalThis.crypto.getRandomValues(new Uint8Array(65537));
			out.large = "no error";
		} catch (err) {
			out.large = String(err.name) + ": " + String(err.message);
		}
		return JSON.stringify(out);
	})()`)
	if !strings.Contains(got, `"float":"TypeError:`) {
		t.Fatalf("expected float typed array rejection, got %q", got)
	}
	if !strings.Contains(got, `"dataView":"TypeError:`) {
		t.Fatalf("expected DataView rejection, got %q", got)
	}
	if !strings.Contains(got, `"large":"QuotaExceededError:`) {
		t.Fatalf("expected quota rejection, got %q", got)
	}
	if !strings.Contains(got, "65536") {
		t.Fatalf("expected size guidance, got %q", got)
	}
}

func TestInstallBrowserCompat_BtoaRejectsNonLatin1(t *testing.T) {
	rt := newBrowserCompatRuntime(t)

	if err := installBrowserCompat(rt, Host{RandomBytes: sequentialRandomBytes}); err != nil {
		t.Fatalf("installBrowserCompat: %v", err)
	}

	got := evalBrowserCompatString(t, rt, `(() => {
		try {
			globalThis.btoa("€");
			return "no error";
		} catch (err) {
			return String(err.name) + ": " + String(err.message);
		}
	})()`)
	if !strings.Contains(got, "TypeError") {
		t.Fatalf("expected TypeError, got %q", got)
	}
	if !strings.Contains(got, "Latin1") {
		t.Fatalf("expected Latin1 guidance, got %q", got)
	}
}

func TestInstallBrowserCompat_AtobRejectsMalformedBase64(t *testing.T) {
	rt := newBrowserCompatRuntime(t)

	if err := installBrowserCompat(rt, Host{RandomBytes: sequentialRandomBytes}); err != nil {
		t.Fatalf("installBrowserCompat: %v", err)
	}

	got := evalBrowserCompatString(t, rt, `(() => {
		try {
			globalThis.atob("***");
			return "no error";
		} catch (err) {
			return String(err.name) + ": " + String(err.message);
		}
	})()`)
	if !strings.Contains(got, "TypeError") {
		t.Fatalf("expected TypeError, got %q", got)
	}
	if !strings.Contains(got, "base64") {
		t.Fatalf("expected base64 guidance, got %q", got)
	}
}

func sequentialRandomBytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, nil
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i)
	}
	return out, nil
}

func newBrowserCompatRuntime(t *testing.T) *qjs.Runtime {
	t.Helper()

	rt, err := qjs.New()
	if err != nil {
		t.Fatalf("qjs.New: %v", err)
	}
	t.Cleanup(func() {
		rt.Close()
	})
	return rt
}

func evalBrowserCompatString(t *testing.T, rt *qjs.Runtime, source string) string {
	t.Helper()

	val, err := rt.Eval("__browser_compat_test.js", qjs.Code(source))
	if err != nil {
		t.Fatalf("eval %q: %v", source, err)
	}
	defer val.Free()
	return val.String()
}
