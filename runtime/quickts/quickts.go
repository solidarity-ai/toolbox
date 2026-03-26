package quickts

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"testing/fstest"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/fastschema/qjs"
	"github.com/microsoft/typescript-go/toolbox"
	"github.com/solidarity-ai/toolbox/fsoverlay"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const (
	runnerTSFile = "__toolbox_run.ts"
	runnerJSFile = "__toolbox_run.js"
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
	// Console receives JS console output. If nil, console calls are no-ops.
	Console func(level string, args []string)
}

// Run is the minimal TS-tool runtime seam.
func Run(def tooldef.TSToolDef, args map[string]any, sig *toolbox.FuncSig) (string, error) {
	return RunWithHost(def, args, Host{}, nil, sig)
}

// RunWithSession is like Run but accepts a session pointer for caching.
func RunWithSession(def tooldef.TSToolDef, args map[string]any, session **toolbox.CheckSession, sig *toolbox.FuncSig) (string, error) {
	return RunWithHost(def, args, Host{}, session, sig)
}

// RunWithHost is the same minimal runtime seam with optional host imports.
// If *session is non-nil the TypeScript checker reuses it for incremental
// checking. On first call the created session is written back through the pointer.
// If sig is non-nil, args are spread as individual function params in order;
// otherwise they are passed as a single object (legacy style).
func RunWithHost(def tooldef.TSToolDef, args map[string]any, host Host, session **toolbox.CheckSession, sig *toolbox.FuncSig) (string, error) {
	var checkSession *toolbox.CheckSession
	if session != nil {
		checkSession = *session
	}
	files, err := withRunner(def.Files, runnerSource(def.Entry, args, sig))
	if err != nil {
		return "", err
	}

	diagnostics, checkSession, err := toolbox.Check(context.Background(), toolbox.CheckInput{
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
		return "", fmt.Errorf("typescript check failed: %w", err)
	}
	if len(diagnostics) > 0 {
		// TODO: Normalize checker/runtime errors into an LLM-friendly shape instead
		// of returning raw compiler/library text.
		return "", fmt.Errorf("typescript check failed: %s", formatDiagnostics(diagnostics))
	}

	code, err := emit(files, def.PackageRoot)
	if err != nil {
		return "", err
	}

	rt, err := qjs.New()
	if err != nil {
		return "", fmt.Errorf("create qjs runtime: %w", err)
	}
	defer rt.Close()

	if err := installHost(rt, host); err != nil {
		return "", err
	}

	val, err := rt.Eval(runnerJSFile, qjs.Code(code), qjs.TypeModule())
	if err != nil {
		return "", fmt.Errorf("run %s: %w", def.Entry, err)
	}
	defer val.Free()

	return val.String(), nil
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
			runnerTSFile: &fstest.MapFile{Data: []byte(source)},
		},
		base,
	), nil
}

func runnerSource(entry string, args map[string]any, sig *toolbox.FuncSig) string {
	if sig == nil {
		// Legacy single-object style: tool(args, ctx)
		argsJSON, _ := json.Marshal(args)
		return fmt.Sprintf("import tool from \"./%s\";\nexport default await tool(%s, {});\n", entry, argsJSON)
	}
	params := sig.Params()
	if len(params) == 0 {
		return fmt.Sprintf("import tool from \"./%s\";\nexport default await tool();\n", entry)
	}
	// Multi-param style: inline each arg with a type assertion against the
	// function's parameter types so the TS checker validates arg types.
	var sb strings.Builder
	fmt.Fprintf(&sb, "import tool from \"./%s\";\n", entry)
	sb.WriteString("export default await tool(")
	for i, p := range params {
		if i > 0 {
			sb.WriteString(", ")
		}
		val, ok := args[p.Name()]
		if !ok {
			sb.WriteString("undefined")
		} else {
			valJSON, _ := json.Marshal(val)
			fmt.Fprintf(&sb, "(%s satisfies Parameters<typeof tool>[%d])", string(valJSON), i)
		}
	}
	sb.WriteString(");\n")
	return sb.String()
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
func RunnerSourceForTest(entry string, args map[string]any, sig *toolbox.FuncSig) string {
	return runnerSource(entry, args, sig)
}

// EmitBundle runs esbuild bundling on a tool definition and returns the bundled JS.
// Exported for testing that npm deps are correctly inlined.
func EmitBundle(def tooldef.TSToolDef) (string, error) {
	files, err := withRunner(def.Files, runnerSource(def.Entry, "{}"))
	if err != nil {
		return "", err
	}
	return emit(files, def.PackageRoot)
}
