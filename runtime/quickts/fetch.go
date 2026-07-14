package quickts

import (
	"encoding/json"
	"fmt"

	"github.com/fastschema/qjs"
)

// installFetch injects a Fetch-API-compatible fetch() global into the QuickJS
// runtime. Headers, Request, and Response are pure JS; only the actual HTTP
// call crosses into Go via __go_fetch.
func installFetch(rt *qjs.Runtime, host Host) error {
	ctx := rt.Context()

	// __go_fetch is the single Go↔JS bridge for HTTP requests.
	// JS validates/normalizes headers before calling this.
	ctx.SetAsyncFunc("__go_fetch", func(this *qjs.This) {
		args := this.Args()
		if len(args) < 4 {
			this.Promise().Reject(ctx.NewString("TypeError: __go_fetch requires 4 arguments"))
			return
		}

		url := args[0].String()
		method := args[1].String()
		headersJSON := args[2].String()
		body := args[3].String()

		result, err := host.Fetch(url, method, headersJSON, body)
		if err != nil {
			this.Promise().Reject(ctx.NewString("TypeError: " + err.Error()))
			return
		}

		resultJSON, err := json.Marshal(result)
		if err != nil {
			this.Promise().Reject(ctx.NewString("TypeError: marshal response: " + err.Error()))
			return
		}

		jsCode := `(function(d) {
			var r = new Response(d.body, {status: d.status, statusText: d.statusText});
			r.url = d.url; r.type = 'basic';
			r.ok = d.status >= 200 && d.status < 300;
			r.headers = new Headers(d.headers);
			return r;
		})(` + string(resultJSON) + `)`

		val, evalErr := rt.Eval("__fetch_response.js", qjs.Code(jsCode))
		if evalErr != nil {
			this.Promise().Reject(ctx.NewString("TypeError: build response: " + evalErr.Error()))
			return
		}
		this.Promise().Resolve(val)
	})

	// Install pure-JS Fetch API classes and the fetch() wrapper.
	if _, err := rt.Eval("__toolbox_fetch.js", qjs.Code(fetchAPIJS)); err != nil {
		return fmt.Errorf("install fetch API: %w", err)
	}

	return nil
}

// fetchAPIJS is the pure-JS implementation of the Fetch API surface.
// Headers, Request, Response are entirely in JS. Only the actual HTTP call
// delegates to Go via __go_fetch.
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

// --- Iterator prototype ---

var __iteratorProto = Object.getPrototypeOf(Object.getPrototypeOf([][Symbol.iterator]()));
var __headersIterProto = Object.create(__iteratorProto);
Object.defineProperty(__headersIterProto, 'next', {
  configurable: true, enumerable: true, writable: true,
  value: function() { return this._next(); }
});

// --- Headers ---

class Headers {
  constructor(init) {
    this._list = [];
    if (init === null || typeof init === 'number' || typeof init === 'boolean')
      throw new TypeError('Cannot construct Headers from ' + init);
    if (init === undefined) return;
    if (typeof init === 'object' && typeof init[Symbol.iterator] === 'function') {
      for (var entry of init) {
        var pair = Array.from(entry);
        if (pair.length !== 2) throw new TypeError('Header pair must be 2-element sequence');
        this._list.push([__validateName(pair[0]).toLowerCase(), __validateValue(__normalizeValue(pair[1]))]);
      }
      this._sort();
      return;
    }
    if (typeof init === 'object') {
      for (var key of Object.keys(init))
        this._list.push([__validateName(key).toLowerCase(), __validateValue(__normalizeValue(String(init[key])))]);
      this._sort();
      return;
    }
    throw new TypeError('Cannot construct Headers');
  }
  _sort() { this._list.sort(function(a,b) { return a[0]<b[0]?-1:a[0]>b[0]?1:0; }); }
  append(n,v) { __validateName(n); this._list.push([n.toLowerCase(), __validateValue(__normalizeValue(String(v)))]); this._sort(); }
  delete(n) { __validateName(n); var l=n.toLowerCase(); this._list=this._list.filter(function(e){return e[0]!==l;}); }
  get(n) { __validateName(n); var l=n.toLowerCase(),v=[]; for(var i=0;i<this._list.length;i++) if(this._list[i][0]===l) v.push(this._list[i][1]); return v.length?v.join(', '):null; }
  has(n) { __validateName(n); var l=n.toLowerCase(); for(var i=0;i<this._list.length;i++) if(this._list[i][0]===l) return true; return false; }
  set(n,v) { __validateName(n); v=__validateValue(__normalizeValue(String(v))); var l=n.toLowerCase(); this._list=this._list.filter(function(e){return e[0]!==l;}); this._list.push([l,v]); this._sort(); }
  forEach(cb,thisArg) { if(typeof cb!=='function') throw new TypeError(cb+' is not a function'); var c=this._combined(); for(var i=0;i<c.length;i++) cb.call(thisArg,c[i][1],c[i][0],this); }
  _combined() {
    var r=[],p='';
    for(var i=0;i<this._list.length;i++) {
      var n=this._list[i][0],v=this._list[i][1];
      if(n==='set-cookie') r.push([n,v]);
      else if(n===p&&r.length) r[r.length-1][1]+=', '+v;
      else r.push([n,v]);
      p=n;
    }
    return r;
  }
  _makeIterator(t) { var s=this,idx=0,it=Object.create(__headersIterProto); it._next=function(){var e=s._combined();if(idx>=e.length)return{done:true,value:undefined};return{done:false,value:t(e[idx++])};}; return it; }
  entries() { return this._makeIterator(function(e){return e;}); }
  keys() { return this._makeIterator(function(e){return e[0];}); }
  values() { return this._makeIterator(function(e){return e[1];}); }
  [Symbol.iterator]() { return this.entries(); }
}

// --- Response ---

class Response {
  constructor(body, init) {
    this.status = (init && init.status) || 200;
    this.statusText = (init && init.statusText) || '';
    this.ok = this.status >= 200 && this.status < 300;
    this.headers = new Headers(init && init.headers);
    this.url = ''; this.type = 'basic';
    this._body = body !== undefined && body !== null ? String(body) : '';
    this._bodyUsed = false;
  }
  get bodyUsed() { return this._bodyUsed; }
  async text() { this._bodyUsed = true; return this._body; }
  async json() { this._bodyUsed = true; return JSON.parse(this._body); }
}

// --- fetch() wrapper ---

globalThis.fetch = async function fetch(resource, init) {
  var url = resource && typeof resource.url === 'string' ? resource.url : String(resource);
  var method = (init && init.method) || 'GET';
  var body = (init && init.body != null) ? String(init.body) : '';

  var upper = method.toUpperCase();
  if (body && (upper === 'HEAD' || upper === 'GET'))
    throw new TypeError('Request with GET/HEAD cannot have body');

  var h;
  if (init && init.headers) {
    h = (init.headers instanceof Headers) ? init.headers : new Headers(init.headers);
  }
  if (!h) h = new Headers();

  return __go_fetch(url, method, JSON.stringify(h._list), body);
};
`
