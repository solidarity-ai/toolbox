package quickts

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing/fstest"
	"time"

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
	"github.com/fastschema/qjs"
	"github.com/mackross/repljs/jswire"
	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/fsoverlay"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const (
	runnerTSFile = "__toolbox_run.ts"
	runnerJSFile = "__toolbox_run.js"
	// This static checker shim only covers the current plain typescript-sandbox
	// delta beyond the default DOM libs. It deserves more design once we start
	// composing distinct runtime shims/compat layers, and we may eventually
	// want the checker to stop relying on the default DOM libs entirely.
	hostCompatDTSFile        = "__toolbox_host_compat.d.ts"
	checkSessionReuseTimeout = 5 * time.Second
)

// nodeBuiltins are marked as external so esbuild doesn't try to bundle them.
// npm packages that reference these will fail at runtime in QuickJS unless
// the code path is never actually reached (e.g. conditional requires).
var nodeBuiltins = []string{
	"node:*",
}

type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// FetchResult holds the response from a fetch call.
type FetchResult struct {
	Status     int         `json:"status"`
	StatusText string      `json:"statusText"`
	Headers    [][2]string `json:"headers"`
	Body       string      `json:"body"`
	URL        string      `json:"url"`
}

type Host struct {
	Exec      func(binary string, args []string) (ExecResult, error)
	ReadFile  func(path string) (string, error)
	WriteFile func(path string, data string) error
	Fetch     func(url, method, headersJSON, body string) (FetchResult, error)
	// RandomBytes supplies cryptographically secure random bytes for browser
	// compatibility helpers like crypto.getRandomValues/randomUUID. If nil,
	// installBrowserCompat falls back to crypto/rand.
	RandomBytes func(size int) ([]byte, error)
	// Console receives JS console output. If nil, console calls are no-ops.
	Console func(level string, args []string)
}

// Run is the minimal TS-tool runtime seam.
func Run(def tooldef.TSToolDef, args jswire.Value, sig *toolbox.FuncSignature) (jswire.Value, error) {
	return RunContext(context.Background(), def, args, sig)
}

// RunContext is the minimal TS-tool runtime seam with cancellation.
func RunContext(ctx context.Context, def tooldef.TSToolDef, args jswire.Value, sig *toolbox.FuncSignature) (jswire.Value, error) {
	return RunWithHostContext(ctx, def, args, Host{}, nil, sig)
}

// RunWithSession is like Run but accepts a session pointer for caching.
func RunWithSession(def tooldef.TSToolDef, args jswire.Value, session **toolbox.CheckSession, sig *toolbox.FuncSignature) (jswire.Value, error) {
	return RunWithSessionContext(context.Background(), def, args, session, sig)
}

// RunWithSessionContext is like RunContext but accepts a session pointer for caching.
func RunWithSessionContext(ctx context.Context, def tooldef.TSToolDef, args jswire.Value, session **toolbox.CheckSession, sig *toolbox.FuncSignature) (jswire.Value, error) {
	return RunWithHostContext(ctx, def, args, Host{}, session, sig)
}

// RunApprovalPresentation calls an optional displayApproval export from the
// tool module with no host functions installed. When sig is available,
// displayApproval receives the same positional arguments as the default tool.
// It returns an empty string when the export is absent or returns null/undefined.
func RunApprovalPresentation(ctx context.Context, def tooldef.TSToolDef, args map[string]any, session **toolbox.CheckSession, sig *toolbox.FuncSignature) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := RunModuleSourceWithHostContext(ctx, def, nil, approvalPresentationRunnerSource(def.Entry, args, sig), Host{}, session)
	if err != nil {
		return "", err
	}
	value, err := jswire.DecodeGoja(goja.New(), result)
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

// RunWithHost is the same minimal runtime seam with optional host imports.
// If *session is non-nil the TypeScript checker reuses it for incremental
// checking. On first call the created session is written back through the pointer.
// If sig is non-nil, args are spread as individual function params in order;
// otherwise they are passed as a single object (legacy style).
func RunWithHost(def tooldef.TSToolDef, args jswire.Value, host Host, session **toolbox.CheckSession, sig *toolbox.FuncSignature) (jswire.Value, error) {
	return RunWithHostContext(context.Background(), def, args, host, session, sig)
}

// RunWithHostContext is RunWithHost with cancellation support.
func RunWithHostContext(ctx context.Context, def tooldef.TSToolDef, args jswire.Value, host Host, session **toolbox.CheckSession, sig *toolbox.FuncSignature) (result jswire.Value, err error) {
	return RunModuleSourceWithHostContext(ctx, def, args, runnerSource(def.Entry, args, sig), host, session)
}

func RunModuleSourceWithHostContext(ctx context.Context, def tooldef.TSToolDef, args jswire.Value, source string, host Host, session **toolbox.CheckSession) (result jswire.Value, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if ctx.Err() != nil {
				result = nil
				err = ctx.Err()
				return
			}
			panic(recovered)
		}
	}()
	var checkSession *toolbox.CheckSession
	if session != nil {
		checkSession = *session
	}
	files, err := withRunner(def.Files, source)
	if err != nil {
		return nil, err
	}

	checkCtx, cancelCheck := checkerContext(ctx, checkSession)
	defer cancelCheck()

	diagnostics, checkSession, err := toolbox.Check(checkCtx, toolbox.CheckInput{
		Files:            files,
		Entry:            runnerTSFile,
		CurrentDirectory: "/",
	}, checkSession)
	if session != nil {
		*session = checkSession
	}
	if err != nil {
		// TODO: Normalize checker/runtime errors into an LLM-friendly shape instead
		// of returning raw compiler/library text.
		return nil, fmt.Errorf("typescript check failed: %w", err)
	}
	if len(diagnostics) > 0 {
		// TODO: Normalize checker/runtime errors into an LLM-friendly shape instead
		// of returning raw compiler/library text.
		return nil, fmt.Errorf("typescript check failed: %s", formatDiagnostics(diagnostics))
	}

	code, err := emit(files, def.PackageRoot)
	if err != nil {
		return nil, err
	}

	rt, err := qjs.New(qjs.Option{
		Context:            ctx,
		CloseOnContextDone: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create qjs runtime: %w", err)
	}
	defer func() {
		if rt != nil {
			rt.Close()
		}
	}()

	if err := installHost(rt, host); err != nil {
		return nil, err
	}
	if len(args) > 0 {
		argsValue, err := jswire.DecodeQuickJS(rt.Context(), args)
		if err != nil {
			return nil, fmt.Errorf("decode tool args: %w", err)
		}
		defer argsValue.Free()
		rt.Context().Global().SetPropertyStr("__toolboxArgs", argsValue)
	}

	val, err := rt.Eval(runnerJSFile, qjs.Code(code), qjs.TypeModule())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("run %s: %w", def.Entry, err)
	}
	defer func() {
		if val != nil {
			val.Free()
		}
	}()

	return jswire.EncodeQuickJS(val)
}

// PrepareCheckSession creates or refreshes a reusable TypeScript checker
// session for the tool without executing the runtime.
func PrepareCheckSession(def tooldef.TSToolDef, session **toolbox.CheckSession) error {
	var checkSession *toolbox.CheckSession
	if session != nil {
		checkSession = *session
	}
	files, err := withRunner(def.Files, prepareRunnerSource(def.Entry))
	if err != nil {
		return err
	}

	checkCtx, cancelCheck := checkerContext(context.Background(), checkSession)
	defer cancelCheck()

	diagnostics, checkSession, err := toolbox.Check(checkCtx, toolbox.CheckInput{
		Files:            files,
		Entry:            runnerTSFile,
		CurrentDirectory: "/",
	}, checkSession)
	if session != nil {
		*session = checkSession
	}
	if err != nil {
		return fmt.Errorf("typescript check failed: %w", err)
	}
	if len(diagnostics) > 0 {
		return fmt.Errorf("typescript check failed: %s", formatDiagnostics(diagnostics))
	}
	return nil
}

func checkerContext(ctx context.Context, session *toolbox.CheckSession) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	base := context.WithoutCancel(ctx)
	if session == nil {
		// typescript-go stores the creation context on the session itself, so the
		// first checker session must not inherit a short-lived timeout/cancel path.
		return base, func() {}
	}
	return context.WithTimeout(base, checkSessionReuseTimeout)
}

func installHost(rt *qjs.Runtime, host Host) error {
	ctx := rt.Context()

	if host.Exec != nil {
		jsExec, err := qjs.FuncToJS(ctx, func(binary string, args []string) (string, error) {
			result, err := host.Exec(binary, args)
			if err != nil {
				return "", err
			}
			data, err := json.Marshal(result)
			if err != nil {
				return "", fmt.Errorf("marshal exec result: %w", err)
			}
			return string(data), nil
		})
		if err != nil {
			return fmt.Errorf("bind exec: %w", err)
		}
		ctx.Global().SetPropertyStr("__toolboxExec", jsExec)
		if _, err := rt.Eval("__toolbox_host.js", qjs.Code(`globalThis.exec = (binary, args) => JSON.parse(__toolboxExec(binary, args));`)); err != nil {
			return fmt.Errorf("load host imports: %w", err)
		}
	}

	if err := installBrowserCompat(rt, host); err != nil {
		return err
	}

	if host.ReadFile != nil || host.WriteFile != nil {
		if err := installFS(rt, host); err != nil {
			return err
		}
	}

	if host.Fetch != nil {
		if err := installFetch(rt, host); err != nil {
			return err
		}
	}

	if err := installConsole(rt, host); err != nil {
		return err
	}

	return nil
}

func installConsole(rt *qjs.Runtime, host Host) error {
	ctx := rt.Context()

	jsLog, err := qjs.FuncToJS(ctx, func(level string, args []string) {
		if host.Console != nil {
			host.Console(level, args)
		}
	})
	if err != nil {
		return fmt.Errorf("bind console: %w", err)
	}
	ctx.Global().SetPropertyStr("__toolboxConsole", jsLog)

	consoleJS := `
(function() {
  function makeLog(level) {
    return function(...args) {
      __toolboxConsole(level, args.map(a => {
        if (typeof a === 'string') return a;
        try { return JSON.stringify(a); } catch { return String(a); }
      }));
    };
  }
  globalThis.console = {
    log: makeLog('log'),
    warn: makeLog('warn'),
    error: makeLog('error'),
    info: makeLog('info'),
    debug: makeLog('debug'),
    trace: makeLog('trace'),
  };
})();
`
	if _, err := rt.Eval("__toolbox_console.js", qjs.Code(consoleJS)); err != nil {
		return fmt.Errorf("install console: %w", err)
	}
	return nil
}

func installFS(rt *qjs.Runtime, host Host) error {
	ctx := rt.Context()

	if host.ReadFile != nil {
		fn, err := qjs.FuncToJS(ctx, host.ReadFile)
		if err != nil {
			return fmt.Errorf("bind readFile: %w", err)
		}
		ctx.Global().SetPropertyStr("__toolboxReadFile", fn)
	}

	if host.WriteFile != nil {
		fn, err := qjs.FuncToJS(ctx, func(path string, data string) (int, error) {
			return 0, host.WriteFile(path, data)
		})
		if err != nil {
			return fmt.Errorf("bind writeFile: %w", err)
		}
		ctx.Global().SetPropertyStr("__toolboxWriteFile", fn)
	}

	fsShim := `globalThis.fs = {`
	if host.ReadFile != nil {
		fsShim += `readFileSync: (path, opts) => __toolboxReadFile(path),`
	}
	if host.WriteFile != nil {
		fsShim += `writeFileSync: (path, data) => { __toolboxWriteFile(path, typeof data === 'string' ? data : new TextDecoder().decode(data)); },`
	}
	fsShim += `};`

	if _, err := rt.Eval("__toolbox_fs.js", qjs.Code(fsShim)); err != nil {
		return fmt.Errorf("load fs shim: %w", err)
	}
	return nil
}

func emit(files fs.FS, packageRoot string) (string, error) {
	runner, err := fs.ReadFile(files, runnerTSFile)
	if err != nil {
		return "", fmt.Errorf("read runner: %w", err)
	}

	opts := api.BuildOptions{
		Bundle:   true,
		Write:    false,
		Format:   api.FormatESModule,
		Platform: api.PlatformBrowser,
		Target:   api.ES2023,
		LogLevel: api.LogLevelSilent,
		Stdin: &api.StdinOptions{
			Contents:   string(runner),
			ResolveDir: "/",
			Sourcefile: runnerTSFile,
			Loader:     api.LoaderTS,
		},
		Plugins:  []api.Plugin{memFSPlugin(files, packageRoot)},
		External: nodeBuiltins,
	}

	// When running from a source directory on disk, allow esbuild to resolve
	// bare imports (npm packages) from the package's node_modules.
	if packageRoot != "" {
		opts.NodePaths = []string{filepath.Join(packageRoot, "node_modules")}
	}

	result := api.Build(opts)
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("esbuild emit failed: %s", result.Errors[0].Text)
	}
	if len(result.OutputFiles) == 0 {
		return "", fmt.Errorf("esbuild emit produced no output")
	}

	return string(result.OutputFiles[0].Contents), nil
}

func withRunner(base fs.FS, source string) (fs.FS, error) {
	return fsoverlay.New(
		fstest.MapFS{
			runnerTSFile:      &fstest.MapFile{Data: []byte(source)},
			hostCompatDTSFile: &fstest.MapFile{Data: []byte(hostCompatDTS())},
		},
		base,
	), nil
}

func hostCompatDTS() string {
	return `// Intentionally empty for now. The plain typescript-sandbox runtime
// currently relies on the default DOM libs, and this checker/runtime shim
// surface deserves more design once we introduce additional compat layers.
// We may eventually want the checker to stop relying on the default DOM libs
// entirely and have this shim declare the full compat surface explicitly.
`
}

func runnerSource(entry string, args jswire.Value, sig *toolbox.FuncSignature) string {
	if sig == nil {
		return fmt.Sprintf("import tool from \"./%s\";\nconst __r = await tool((globalThis as any).__toolboxArgs ?? {}, {}); export default __r;\n", entry)
	}
	params := sig.Params()
	if len(params) == 0 {
		return fmt.Sprintf("import tool from \"./%s\";\nconst __r = await tool(); export default __r;\n", entry)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "import tool from \"./%s\";\n", entry)
	fmt.Fprintf(&sb, "const __toolboxArgs = ((globalThis as any).__toolboxArgs ?? {}) as %s;\n", wireArgsObjectType(args, params))
	sb.WriteString("const __r = await tool(")
	for i, p := range params {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "(__toolboxArgs[%q] satisfies Parameters<typeof tool>[%d])", p.Name(), i)
	}
	sb.WriteString(");\n")
	sb.WriteString("export default __r;")
	sb.WriteString("\n")
	return sb.String()
}

func wireArgsObjectType(raw jswire.Value, params []toolbox.FuncParam) string {
	values := map[string]any{}
	if len(raw) > 0 {
		if decoded, err := jswire.Decode(raw); err == nil {
			if obj, ok := decoded.(jswire.ObjectType); ok {
				values = map[string]any(obj)
			}
		}
	}

	parts := make([]string, 0, len(params))
	for _, p := range params {
		tsType := "undefined"
		if v, ok := values[p.Name()]; ok {
			tsType = wireValueTypeScriptType(v)
		}
		optional := ""
		if p.Optional() && tsType == "undefined" {
			optional = "?"
		}
		parts = append(parts, fmt.Sprintf("%s%s: %s", strconv.Quote(p.Name()), optional, tsType))
	}
	if len(parts) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(parts, "; ") + " }"
}

func wireValueTypeScriptType(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return strconv.Quote(v)
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "number"
	case jswire.BigIntType:
		return "bigint"
	case jswire.DateType:
		return "Date"
	case jswire.RegExpType:
		return "RegExp"
	case jswire.ArrayBufferType:
		return "ArrayBuffer"
	case jswire.Uint8ArrayType:
		return "Uint8Array"
	case jswire.Uint8ClampedArrayType:
		return "Uint8ClampedArray"
	case jswire.Int8ArrayType:
		return "Int8Array"
	case jswire.Uint16ArrayType:
		return "Uint16Array"
	case jswire.Int16ArrayType:
		return "Int16Array"
	case jswire.Uint32ArrayType:
		return "Uint32Array"
	case jswire.Int32ArrayType:
		return "Int32Array"
	case jswire.BigUint64ArrayType:
		return "BigUint64Array"
	case jswire.BigInt64ArrayType:
		return "BigInt64Array"
	case jswire.Float32ArrayType:
		return "Float32Array"
	case jswire.Float64ArrayType:
		return "Float64Array"
	case jswire.ArrayType:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, wireValueTypeScriptType(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case jswire.ObjectType:
		return wireObjectTypeScriptType(v)
	case jswire.MapType:
		if len(v) == 0 {
			return "Map<any, any>"
		}
		keys := make([]string, 0, len(v))
		values := make([]string, 0, len(v))
		for _, entry := range v {
			keys = append(keys, wireValueTypeScriptType(entry[0]))
			values = append(values, wireValueTypeScriptType(entry[1]))
		}
		return "Map<" + unionTypeScriptTypes(keys) + ", " + unionTypeScriptTypes(values) + ">"
	case jswire.SetType:
		if len(v) == 0 {
			return "Set<any>"
		}
		values := make([]string, 0, len(v))
		for _, item := range v {
			values = append(values, wireValueTypeScriptType(item))
		}
		return "Set<" + unionTypeScriptTypes(values) + ">"
	default:
		return "unknown"
	}
}

func wireObjectTypeScriptType(obj jswire.ObjectType) string {
	if len(obj) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", strconv.Quote(key), wireValueTypeScriptType(obj[key])))
	}
	return "{ " + strings.Join(parts, "; ") + " }"
}

func unionTypeScriptTypes(types []string) string {
	if len(types) == 0 {
		return "never"
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(types))
	for _, typ := range types {
		if seen[typ] {
			continue
		}
		seen[typ] = true
		unique = append(unique, typ)
	}
	sort.Strings(unique)
	return strings.Join(unique, " | ")
}

func approvalPresentationRunnerSource(entry string, args map[string]any, sig *toolbox.FuncSignature) string {
	var sb strings.Builder
	if sig == nil {
		fmt.Fprintf(&sb, `import * as mod from "./%s";`, entry)
		sb.WriteString("\n")
	} else {
		fmt.Fprintf(&sb, `import tool, * as mod from "./%s";`, entry)
		sb.WriteString("\n")
	}
	argsJSON, _ := json.Marshal(args)
	fmt.Fprintf(&sb, "const __toolboxApprovalArgs = %s;\n", string(argsJSON))
	sb.WriteString("(globalThis as any).__toolboxApprovalArgs = __toolboxApprovalArgs;\n")
	callArgs := approvalPresentationCallArgs(args, sig)
	if sig != nil {
		sb.WriteString(`type __ToolboxExactParams<A, B> = [A] extends [B] ? ([B] extends [A] ? true : never) : never;
type __ToolboxDisplayApprovalParams = typeof mod extends { displayApproval: (...args: infer P) => any } ? P : Parameters<typeof tool>;
const __toolboxDisplayApprovalParamsCheck: __ToolboxExactParams<__ToolboxDisplayApprovalParams, Parameters<typeof tool>> = true;
`)
	}
	fmt.Fprintf(&sb, `const __displayApproval = (mod as any).displayApproval;
const __r = typeof __displayApproval === "function" ? await __displayApproval(%s) : "";
export default (__r === undefined || __r === null) ? "" : (typeof __r === "string" ? __r : JSON.stringify(__r));
`, callArgs)
	return sb.String()
}

func approvalPresentationCallArgs(args map[string]any, sig *toolbox.FuncSignature) string {
	if sig == nil {
		argsJSON, _ := json.Marshal(args)
		return string(argsJSON)
	}
	params := sig.Params()
	if len(params) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, p := range params {
		if i > 0 {
			sb.WriteString(", ")
		}
		val, ok := args[p.Name()]
		if !ok {
			sb.WriteString("undefined")
			continue
		}
		valJSON, _ := json.Marshal(val)
		fmt.Fprintf(&sb, "(%s satisfies Parameters<typeof tool>[%d])", string(valJSON), i)
	}
	return sb.String()
}

func prepareRunnerSource(entry string) string {
	return fmt.Sprintf("import tool from \"./%s\";\nexport default typeof tool;\n", entry)
}

func formatDiagnostics(diagnostics []toolbox.Diagnostic) string {
	parts := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.File != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", diagnostic.File, diagnostic.Message))
			continue
		}
		parts = append(parts, diagnostic.Message)
	}
	return strings.Join(parts, "; ")
}

func memFSPlugin(files fs.FS, packageRoot string) api.Plugin {
	return api.Plugin{
		Name: "memfs",
		Setup: func(build api.PluginBuild) {
			// isMemFSResolve returns true if the import originates from our
			// virtual filesystem (memfs namespace) or from the stdin entry
			// (namespace "file" with virtual resolve dir "/").
			isMemFSResolve := func(args api.OnResolveArgs) bool {
				if args.Namespace == "memfs" {
					return true
				}
				// The stdin entry has namespace "file" with ResolveDir "/"
				if args.Namespace == "file" && args.ResolveDir == "/" {
					return true
				}
				return false
			}

			// Resolve relative imports from tool source files.
			build.OnResolve(api.OnResolveOptions{Filter: `^\.`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				if !isMemFSResolve(args) {
					return api.OnResolveResult{}, nil
				}
				return api.OnResolveResult{
					Path:      path.Clean(path.Join(args.ResolveDir, args.Path)),
					Namespace: "memfs",
				}, nil
			})

			// Resolve absolute imports from tool source files.
			build.OnResolve(api.OnResolveOptions{Filter: `^/`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				if !isMemFSResolve(args) {
					return api.OnResolveResult{}, nil
				}
				return api.OnResolveResult{
					Path:      path.Clean(args.Path),
					Namespace: "memfs",
				}, nil
			})

			// Bare imports (e.g. "zod", "octokit") from tool source files:
			// resolve using esbuild's native resolution from the package root.
			if packageRoot != "" {
				build.OnResolve(api.OnResolveOptions{Filter: ".*", Namespace: "memfs"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					// Only bare imports reach here (relative/absolute matched above).
					resolved := build.Resolve(args.Path, api.ResolveOptions{
						Kind:       api.ResolveJSImportStatement,
						ResolveDir: packageRoot,
						Namespace:  "file",
					})
					if len(resolved.Errors) > 0 {
						return api.OnResolveResult{}, fmt.Errorf("resolve %q: %s", args.Path, resolved.Errors[0].Text)
					}
					return api.OnResolveResult{
						Path:      resolved.Path,
						Namespace: "file",
					}, nil
				})
			}

			build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "memfs"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				name := strings.TrimPrefix(args.Path, "/")
				content, err := fs.ReadFile(files, name)
				if err != nil {
					return api.OnLoadResult{}, err
				}
				contents := string(content)
				resolveDir := path.Dir(args.Path)
				return api.OnLoadResult{
					Contents:   &contents,
					Loader:     loaderForPath(args.Path),
					ResolveDir: resolveDir,
				}, nil
			})
		},
	}
}

func loaderForPath(file string) api.Loader {
	switch path.Ext(file) {
	case ".ts", ".mts", ".cts":
		return api.LoaderTS
	case ".tsx":
		return api.LoaderTSX
	case ".jsx":
		return api.LoaderJSX
	default:
		return api.LoaderJS
	}
}

// RunnerSourceForTest exposes the generated runner source for narrow unit tests.
func RunnerSourceForTest(entry string, args map[string]any, sig *toolbox.FuncSignature) string {
	raw, _ := json.Marshal(args)
	wire, err := jswire.FromAnonJSObj(raw)
	if err != nil {
		panic(err)
	}
	return runnerSource(entry, wire, sig)
}

// ApprovalPresentationRunnerSourceForTest exposes the generated approval
// presentation runner source for narrow unit tests.
func ApprovalPresentationRunnerSourceForTest(entry string, args map[string]any, sig *toolbox.FuncSignature) string {
	return approvalPresentationRunnerSource(entry, args, sig)
}

// EmitBundle runs esbuild bundling on a tool definition and returns the bundled JS.
// Exported for testing that npm deps are correctly inlined.
func EmitBundle(def tooldef.TSToolDef) (string, error) {
	files, err := withRunner(def.Files, runnerSource(def.Entry, nil, nil))
	if err != nil {
		return "", err
	}
	return emit(files, def.PackageRoot)
}
