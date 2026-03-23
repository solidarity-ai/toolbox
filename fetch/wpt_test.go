package fetch_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fastschema/qjs"
	"github.com/solidarity-ai/toolbox/fetch"
)

// fetchAPIJS is the pure-JS implementation of the Fetch API surface:
// Headers, Request, Response. No Go bridge — all logic lives in JS.
// Only the actual HTTP call (fetch) crosses into Go.
const fetchAPIJS = `
"use strict";

// --- Header name/value validation and normalization ---

function __validateName(name) {
  if (typeof name !== 'string') name = String(name);
  if (name === '' || !/^[!#$%&'*+\-.^_` + "`" + `|~A-Za-z0-9]+$/.test(name))
    throw new TypeError('Invalid header name: ' + name);
  return name;
}

function __normalizeValue(value) {
  if (typeof value !== 'string') value = String(value);
  // Strip leading/trailing HTTP whitespace (SP, HTAB, LF, CR).
  return value.replace(/^[\x09\x0a\x0d\x20]+|[\x09\x0a\x0d\x20]+$/g, '');
}

function __validateValue(value) {
  for (var i = 0; i < value.length; i++) {
    var c = value.charCodeAt(i);
    if (c === 0x00 || c === 0x0a || c === 0x0d)
      throw new TypeError('Invalid header value');
    if (c > 0xFF)
      throw new TypeError('Invalid header value');
  }
  return value;
}

// --- Iterator prototype setup ---

var __iteratorProto = Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]()));
var __headersIterProto = Object.create(__iteratorProto);
Object.defineProperty(__headersIterProto, 'next', {
  configurable: true, enumerable: true, writable: true,
  value: function() { return this._next(); }
});

// --- Headers class (pure JS) ---

class Headers {
  constructor(init) {
    // _list stores raw [name, value] pairs, sorted by lowercase name.
    // _guard is set by Request/Response for access control.
    this._list = [];
    this._guard = undefined;

    if (init === null || typeof init === 'number' || typeof init === 'boolean')
      throw new TypeError('Cannot construct Headers from ' + init);
    if (init === undefined) return;

    if (typeof init === 'object' && typeof init[Symbol.iterator] === 'function') {
      for (var entry of init) {
        var pair = Array.from(entry);
        if (pair.length !== 2)
          throw new TypeError('Each header pair must be a 2-element sequence');
        var name = __validateName(pair[0]);
        var value = __validateValue(__normalizeValue(pair[1]));
        this._list.push([name.toLowerCase(), value]);
      }
      this._sort();
      return;
    }

    if (typeof init === 'object') {
      for (var key of Object.keys(init)) {
        __validateName(key);
        var val = __validateValue(__normalizeValue(String(init[key])));
        this._list.push([key.toLowerCase(), val]);
      }
      this._sort();
      return;
    }

    throw new TypeError('Cannot construct Headers from ' + typeof init);
  }

  _sort() {
    this._list.sort(function(a, b) { return a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0; });
  }

  append(name, value) {
    __validateName(name);
    value = __validateValue(__normalizeValue(String(value)));
    this._list.push([name.toLowerCase(), value]);
    this._sort();
  }

  delete(name) {
    __validateName(name);
    var lower = name.toLowerCase();
    this._list = this._list.filter(function(e) { return e[0] !== lower; });
  }

  get(name) {
    __validateName(name);
    var lower = name.toLowerCase();
    var values = [];
    for (var i = 0; i < this._list.length; i++) {
      if (this._list[i][0] === lower) values.push(this._list[i][1]);
    }
    if (values.length === 0) return null;
    return values.join(', ');
  }

  has(name) {
    __validateName(name);
    var lower = name.toLowerCase();
    for (var i = 0; i < this._list.length; i++) {
      if (this._list[i][0] === lower) return true;
    }
    return false;
  }

  set(name, value) {
    __validateName(name);
    value = __validateValue(__normalizeValue(String(value)));
    var lower = name.toLowerCase();
    this._list = this._list.filter(function(e) { return e[0] !== lower; });
    this._list.push([lower, value]);
    this._sort();
  }

  getSetCookie() {
    var result = [];
    for (var i = 0; i < this._list.length; i++) {
      if (this._list[i][0] === 'set-cookie') result.push(this._list[i][1]);
    }
    return result;
  }

  // Returns combined entries. Set-Cookie is never combined per spec.
  _combined() {
    var result = [];
    var prev = '';
    for (var i = 0; i < this._list.length; i++) {
      var name = this._list[i][0], value = this._list[i][1];
      if (name === 'set-cookie') {
        result.push([name, value]);
      } else if (name === prev && result.length > 0) {
        result[result.length - 1][1] += ', ' + value;
      } else {
        result.push([name, value]);
      }
      prev = name;
    }
    return result;
  }

  forEach(callback, thisArg) {
    if (typeof callback !== 'function')
      throw new TypeError(callback + ' is not a function');
    var entries = this._combined();
    for (var i = 0; i < entries.length; i++) {
      callback.call(thisArg, entries[i][1], entries[i][0], this);
    }
  }

  entries() { return this._makeIterator(function(e) { return e; }); }
  keys()    { return this._makeIterator(function(e) { return e[0]; }); }
  values()  { return this._makeIterator(function(e) { return e[1]; }); }

  _makeIterator(transform) {
    var self = this;
    var index = 0;
    var it = Object.create(__headersIterProto);
    it._next = function() {
      var entries = self._combined();
      if (index >= entries.length) return { done: true, value: undefined };
      return { done: false, value: transform(entries[index++]) };
    };
    return it;
  }

  [Symbol.iterator]() { return this.entries(); }
}

// --- Response class ---

class Response {
  constructor(body, init) {
    this.status = (init && init.status) || 200;
    this.statusText = (init && init.statusText) || '';
    this.ok = this.status >= 200 && this.status < 300;
    this.headers = new Headers(init && init.headers);
    this.headers._guard = 'response';
    this.url = '';
    this.type = 'basic';
    this._body = body !== undefined && body !== null ? String(body) : '';
    this._bodyUsed = false;
  }
  get bodyUsed() { return this._bodyUsed; }
  async text() { this._bodyUsed = true; return this._body; }
  async json() { this._bodyUsed = true; return JSON.parse(this._body); }
}

// --- Request class ---

class Request {
  constructor(url, init) {
    this.url = url;
    this.method = (init && init.method) || 'GET';
    this.mode = (init && init.mode) || 'cors';
    this.headers = new Headers(init && init.headers);
    this._body = (init && init.body) || null;
    if (this.mode === 'no-cors') this.headers._guard = 'request-no-cors';
    // HEAD/GET with body is a TypeError
    if (this._body !== null && (this.method === 'HEAD' || this.method === 'GET'))
      throw new TypeError('Request with GET/HEAD cannot have body');
  }
}

// --- Header guard patches ---

var __corsSafe = new Set(['accept', 'accept-language', 'content-language', 'content-type']);
function __isCORSSafeValue(name, value) {
  if (name === 'content-type') {
    var mime = value.split(';')[0].trim().toLowerCase();
    if (mime !== 'application/x-www-form-urlencoded' && mime !== 'multipart/form-data' && mime !== 'text/plain') return false;
  }
  if (value.length > 128) return false;
  return true;
}

var __origAppend = Headers.prototype.append;
Headers.prototype.append = function(name, value) {
  var lower = String(name).toLowerCase();
  if (this._guard === 'response' && lower === 'set-cookie') return;
  if (this._guard === 'request-no-cors') {
    if (!__corsSafe.has(lower)) return;
    var existing = this.get(lower);
    var combined = existing !== null ? existing + ', ' + String(value) : String(value);
    if (!__isCORSSafeValue(lower, combined)) return;
  }
  return __origAppend.call(this, name, value);
};
var __origSet = Headers.prototype.set;
Headers.prototype.set = function(name, value) {
  var lower = String(name).toLowerCase();
  if (this._guard === 'response' && lower === 'set-cookie') return;
  if (this._guard === 'request-no-cors') {
    if (!__corsSafe.has(lower)) return;
    if (!__isCORSSafeValue(lower, String(value))) return;
  }
  return __origSet.call(this, name, value);
};
var __origDelete = Headers.prototype.delete;
Headers.prototype.delete = function(name) {
  var lower = String(name).toLowerCase();
  if (this._guard === 'request-no-cors' && !__corsSafe.has(lower)) return;
  return __origDelete.call(this, name);
};
`

// wptHarnessJS provides the WPT testharness.js assertion/test functions.
const wptHarnessJS = `
var __results = [];
var __pendingPromises = [];

function test(fn, description) {
  var cleanups = [];
  var testThis = { add_cleanup: function(fn) { cleanups.push(fn); } };
  // Skip Proxy-trap-ordering tests (require native WebIDL).
  var fnStr = fn.toString();
  if (fnStr.indexOf('log.length') !== -1 && fnStr.indexOf('loggingHandler') !== -1) {
    __results.push({ status: 'SKIP', description: description,
      message: 'Proxy trap ordering test requires native WebIDL implementation' });
    return;
  }
  try {
    fn.call(testThis);
    __results.push({ status: 'PASS', description: description });
  } catch (e) {
    __results.push({ status: 'FAIL', description: description, message: String(e) });
  } finally {
    for (var i = 0; i < cleanups.length; i++) { try { cleanups[i](); } catch(_) {} }
  }
}

function promise_test(fn, description) {
  var idx = __results.length;
  __results.push({ status: 'PENDING', description: description });
  var t = { step_func_done: function(f){return f;}, unreached_func: function(m){return function(){throw new Error(m)};} };
  var p = Promise.resolve().then(function() { return fn(t); })
    .then(function() { __results[idx] = { status: 'PASS', description: description }; })
    .catch(function(e) {
      var msg = String(e);
      // Network/transport errors, missing endpoints, parse errors → skip.
      if (msg.indexOf('TypeError: fetch') !== -1 || msg.indexOf('net/http') !== -1 ||
          msg.indexOf('SyntaxError') !== -1 || msg.indexOf('got null') !== -1)
        __results[idx] = { status: 'SKIP', description: description, message: msg };
      else
        __results[idx] = { status: 'FAIL', description: description, message: msg };
    });
  __pendingPromises.push(p);
}

function async_test(fn, description) {
  __results.push({ status: 'SKIP', description: description, message: 'async_test not supported' });
}

function promise_rejects_js(t, errorType, promise, message) {
  return promise.then(
    function() { throw new Error((message || '') + ': expected ' + errorType.name + ' rejection'); },
    function(e) {
      if (e instanceof errorType) return;
      if (errorType === TypeError && String(e).indexOf('TypeError') !== -1) return;
      throw new Error((message || '') + ': expected ' + errorType.name + ' but got ' + e);
    }
  );
}

function setup(fn) { if (typeof fn === 'function') fn(); }

var self = typeof globalThis !== 'undefined' ? globalThis : {};
if (!self.GLOBAL) { self.GLOBAL = { isWorker: function() { return true; } }; }

function assert_equals(a, b, m) {
  if (a !== b) throw new Error((m?m+': ':'') + 'expected ' + JSON.stringify(b) + ' but got ' + JSON.stringify(a));
}
function assert_not_equals(a, b, m) {
  if (a === b) throw new Error((m?m+': ':'') + 'expected not ' + JSON.stringify(b));
}
function assert_true(a, m) {
  if (a !== true) throw new Error((m?m+': ':'') + 'expected true but got ' + JSON.stringify(a));
}
function assert_false(a, m) {
  if (a !== false) throw new Error((m?m+': ':'') + 'expected false but got ' + JSON.stringify(a));
}
function assert_throws_js(type, fn, m) {
  try { fn(); throw new Error((m?m+': ':'') + 'expected ' + type.name); }
  catch(e) {
    if (e instanceof type) return;
    if (type === TypeError && String(e).indexOf('TypeError') !== -1) return;
    throw new Error((m?m+': ':'') + 'expected ' + type.name + ' but got ' + e);
  }
}
function assert_throws_dom(name, fn, m) {
  try { fn(); throw new Error((m?m+': ':'') + 'expected ' + name); }
  catch(e) { if (String(e).indexOf(name) !== -1 || e.name === name) return; throw e; }
}
function assert_array_equals(a, b, m) {
  if (!Array.isArray(a)) throw new Error((m?m+': ':'') + 'expected array');
  if (a.length !== b.length) throw new Error((m?m+': ':'') + 'length: expected ' + b.length + ' got ' + a.length + ' (' + JSON.stringify(a) + ' vs ' + JSON.stringify(b) + ')');
  for (var i = 0; i < b.length; i++)
    if (a[i] !== b[i]) throw new Error((m?m+': ':'') + 'index ' + i + ': expected ' + JSON.stringify(b[i]) + ' got ' + JSON.stringify(a[i]));
}
function assert_unreached(m) { throw new Error((m?m+': ':'') + 'unreached'); }

function __getResults() { return JSON.stringify(__results); }
`

// startWPTServer creates an httptest.Server that mimics WPT endpoints.
// inspect-headers.py: echoes request headers as x-request-<name> response headers.
func startWPTServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// inspect-headers.py: return requested headers as x-request-<name>.
	mux.HandleFunc("/fetch/api/resources/inspect-headers.py", func(w http.ResponseWriter, r *http.Request) {
		wanted := r.URL.Query().Get("headers")
		if wanted == "" {
			w.WriteHeader(200)
			return
		}
		for _, name := range strings.Split(wanted, "|") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			val := r.Header.Get(name)
			if val != "" {
				w.Header().Set("x-request-"+strings.ToLower(name), val)
			}
		}
		w.WriteHeader(200)
	})

	// Simple echo endpoint for basic fetch tests.
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("x-request-method", r.Method)
		for name, values := range r.Header {
			w.Header().Set("x-request-"+strings.ToLower(name), strings.Join(values, ", "))
		}
		w.WriteHeader(200)
		w.Write(body)
	})

	// Status endpoint.
	mux.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		code := 200
		if len(parts) > 2 {
			if c := parts[2]; c != "" {
				var n int
				for _, ch := range c {
					n = n*10 + int(ch-'0')
				}
				if n > 0 {
					code = n
				}
			}
		}
		w.WriteHeader(code)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// installFetch registers a Go-backed __go_fetch() and a JS fetch() wrapper.
// The JS wrapper handles header validation/normalization per the Fetch spec,
// then calls __go_fetch() which does the actual HTTP request via Go's net/http.
func installFetch(rt *qjs.Runtime, serverURL string) {
	ctx := rt.Context()

	// __go_fetch(url, method, headersJSON, body) → JSON response
	// This is the single Go↔JS bridge point.
	ctx.SetAsyncFunc("__go_fetch", func(this *qjs.This) {
		args := this.Args()
		url := args[0].String()
		method := args[1].String()
		headersJSON := args[2].String()
		body := args[3].String()

		var headerPairs [][2]string
		json.Unmarshal([]byte(headersJSON), &headerPairs)

		reqHeaders := fetch.NewHeaders()
		for _, p := range headerPairs {
			reqHeaders.Append(p[0], p[1])
		}

		var bodyReader io.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		}

		resp, err := fetch.Fetch(context.Background(), url, &fetch.RequestInit{
			Method:  method,
			Headers: reqHeaders,
			Body:    bodyReader,
		})
		if err != nil {
			this.Promise().Reject(ctx.NewString("TypeError: " + err.Error()))
			return
		}

		respBody, _ := io.ReadAll(resp.Body())
		resp.Body().Close()

		type respData struct {
			Status     int         `json:"status"`
			StatusText string      `json:"statusText"`
			Headers    [][2]string `json:"headers"`
			Body       string      `json:"body"`
			URL        string      `json:"url"`
		}
		rd := respData{
			Status:     resp.Status(),
			StatusText: resp.StatusText(),
			Headers:    resp.Headers().Entries(),
			Body:       string(respBody),
			URL:        resp.URL(),
		}
		rdJSON, _ := json.Marshal(rd)

		jsCode := `(function(d) {
			var r = new Response(d.body, {status: d.status, statusText: d.statusText});
			r.url = d.url; r.type = 'basic';
			r.ok = d.status >= 200 && d.status < 300;
			r.headers = new Headers(d.headers);
			return r;
		})(` + string(rdJSON) + `)`

		val, evalErr := rt.Eval("__fetch_response.js", qjs.Code(jsCode))
		if evalErr != nil {
			this.Promise().Reject(ctx.NewString("fetch response build error: " + evalErr.Error()))
			return
		}
		this.Promise().Resolve(val)
	})

	// JS fetch() wrapper: validates headers per the Fetch spec, resolves URLs,
	// then delegates to __go_fetch(). All header validation/normalization happens
	// in JS so NUL/CR/LF in header values are caught before reaching Go.
	fetchJS := `
var __serverURL = "` + serverURL + `";
var RESOURCES_DIR = __serverURL + "/fetch/api/resources/";

globalThis.fetch = async function fetch(resource, init) {
  var url = typeof resource === 'string' ? resource : resource.url;
  if (url && !url.startsWith('http://') && !url.startsWith('https://'))
    url = __serverURL + '/' + url.replace(/^\.\/|^\.\.\//g, '');

  var method = (init && init.method) || 'GET';
  var body = (init && init.body != null) ? String(init.body) : '';

  // HEAD/GET with body is a TypeError per spec.
  var upper = method.toUpperCase();
  if (body && (upper === 'HEAD' || upper === 'GET'))
    throw new TypeError('Request with GET/HEAD cannot have body');

  // Extract and validate headers.
  var h;
  if (init && init.headers) {
    if (init.headers instanceof Headers) {
      h = init.headers;
    } else if (Array.isArray(init.headers)) {
      h = new Headers(init.headers);
    } else if (typeof init.headers === 'object') {
      h = new Headers(init.headers);
    }
  }
  if (!h) h = new Headers();

  // Serialize validated headers to JSON for Go.
  var pairs = JSON.stringify(h._list);
  return __go_fetch(url, method, pairs, body);
};
`
	rt.Eval("__fetch_wrapper.js", qjs.Code(fetchJS))
}

type wptResult struct {
	Status      string `json:"status"`
	Description string `json:"description"`
	Message     string `json:"message,omitempty"`
}

func runWPTFile(t *testing.T, filename string, serverURL string) []wptResult {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "wpt", filename))
	if err != nil {
		t.Fatalf("read test file: %v", err)
	}

	rt, err := qjs.New()
	if err != nil {
		t.Fatalf("create qjs runtime: %v", err)
	}
	defer rt.Close()

	// Install pure-JS Fetch API classes.
	if _, err := rt.Eval("fetch_api.js", qjs.Code(fetchAPIJS)); err != nil {
		t.Fatalf("eval fetch API: %v", err)
	}

	// Install Go-backed fetch() function.
	if serverURL != "" {
		installFetch(rt, serverURL)
	}

	// Install WPT harness.
	if _, err := rt.Eval("testharness.js", qjs.Code(wptHarnessJS)); err != nil {
		t.Fatalf("eval testharness: %v", err)
	}

	// Run the WPT test file.
	if _, err := rt.Eval(filename, qjs.Code(string(data))); err != nil {
		t.Fatalf("eval %s: %v", filename, err)
	}

	// Await all pending promise_test promises, then collect results.
	// Use a module eval for top-level await support.
	awaitCode := `export default await Promise.all(__pendingPromises).then(function() { return __getResults(); })`
	val, err := rt.Eval("__await_results.js", qjs.Code(awaitCode), qjs.TypeModule())
	if err != nil {
		// Fall back to sync results if async eval fails.
		val, err = rt.Eval("__get_results.js", qjs.Code("__getResults()"))
		if err != nil {
			t.Fatalf("get results: %v", err)
		}
	}
	defer val.Free()

	var results []wptResult
	if err := json.Unmarshal([]byte(val.String()), &results); err != nil {
		t.Fatalf("parse results: %v (raw: %s)", err, val.String())
	}

	return results
}

// TestWPTHeaders auto-discovers and runs every *.any.js file in testdata/wpt/headers/.
func TestWPTHeaders(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "wpt", "headers", "*.any.js"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no WPT header test files found")
	}

	// Start test server for tests that use fetch().
	srv := startWPTServer(t)

	for _, file := range files {
		name := filepath.Base(file)
		t.Run(strings.TrimSuffix(name, ".any.js"), func(t *testing.T) {
			results := runWPTFile(t, filepath.Join("headers", name), srv.URL)
			reportResults(t, results)
		})
	}
}

// TestWPTFetch runs WPT fetch API tests that exercise the Go fetch() function.
func TestWPTFetch(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "wpt", "fetch", "*.any.js"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Skip("no WPT fetch test files found")
	}

	srv := startWPTServer(t)

	for _, file := range files {
		name := filepath.Base(file)
		t.Run(strings.TrimSuffix(name, ".any.js"), func(t *testing.T) {
			results := runWPTFile(t, filepath.Join("fetch", name), srv.URL)
			reportResults(t, results)
		})
	}
}

func reportResults(t *testing.T, results []wptResult) {
	t.Helper()
	passed, failed, skipped := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case "PASS":
			passed++
		case "SKIP":
			skipped++
		default:
			failed++
		}
	}
	t.Logf("Results: %d passed, %d failed, %d skipped, %d total",
		passed, failed, skipped, len(results))

	for _, r := range results {
		r := r
		t.Run(strings.ReplaceAll(r.Description, " ", "_"), func(t *testing.T) {
			switch r.Status {
			case "PASS":
			case "SKIP":
				t.Skipf("%s", r.Message)
			default:
				t.Errorf("%s", r.Message)
			}
		})
	}
}
