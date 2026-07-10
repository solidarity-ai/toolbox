package quickts

import (
	_ "embed"
	"fmt"
	"sync"

	"github.com/fastschema/qjs"
)

// urlPolyfillJS is a vendored whatwg-url + buffer bundle built from cached npm
// packages. QuickJS does not provide URL/URLSearchParams, but the TypeScript
// checker exposes the DOM globals and generated toolbox packages use them for
// host allowlist and URL manipulation.
//
//go:embed url_polyfill.js
var urlPolyfillJS string

var (
	urlPolyfillOnce    sync.Once
	urlPolyfillBC      []byte
	urlPolyfillBCError error
)

// urlPolyfillBytecode compiles the polyfill to QuickJS bytecode once per
// process in a throwaway runtime. Executing bytecode per tool VM is ~11x
// faster than re-parsing the ~460KB source bundle on every boot. The bytecode
// never leaves the process, so it is always consistent with the embedded
// QuickJS build that executes it.
func urlPolyfillBytecode() ([]byte, error) {
	urlPolyfillOnce.Do(func() {
		rt, err := qjs.New()
		if err != nil {
			urlPolyfillBCError = fmt.Errorf("compile URL polyfill: create runtime: %w", err)
			return
		}
		defer rt.Close()
		urlPolyfillBC, err = rt.Compile("__toolbox_url_polyfill.js", qjs.Code(urlPolyfillJS))
		if err != nil {
			urlPolyfillBCError = fmt.Errorf("compile URL polyfill: %w", err)
		}
	})
	return urlPolyfillBC, urlPolyfillBCError
}

func installURL(rt *qjs.Runtime) error {
	bytecode, err := urlPolyfillBytecode()
	if err != nil {
		return err
	}
	if _, err := rt.Eval("__toolbox_url_polyfill.js", qjs.Bytecode(bytecode)); err != nil {
		return fmt.Errorf("install URL polyfill: %w", err)
	}
	return nil
}
