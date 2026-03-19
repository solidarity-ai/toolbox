package quickts

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing/fstest"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/fastschema/qjs"
	"github.com/microsoft/typescript-go/toolboxapi"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const (
	runnerTSFile = "__toolbox_run.ts"
	runnerJSFile = "__toolbox_run.js"
)

// Run is the minimal TS-tool runtime seam. For now it assumes the tool entry is
// already runnable as a JS module and hides QuickJS behind this package.
func Run(def tooldef.TSToolDef, args map[string]any) (string, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("marshal args: %w", err)
	}

	files, err := withRunner(def.Files, runnerSource(def.Entry, string(argsJSON)))
	if err != nil {
		return "", err
	}

	diagnostics, err := toolboxapi.Check(context.Background(), toolboxapi.CheckInput{
		Files:            files,
		Entry:            runnerTSFile,
		CurrentDirectory: "/",
	})
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

	code, err := emit(files)
	if err != nil {
		return "", err
	}

	rt, err := qjs.New()
	if err != nil {
		return "", fmt.Errorf("create qjs runtime: %w", err)
	}
	defer rt.Close()

	val, err := rt.Eval(runnerJSFile, qjs.Code(code), qjs.TypeModule())
	if err != nil {
		return "", fmt.Errorf("run %s: %w", def.Entry, err)
	}
	defer val.Free()

	return val.String(), nil
}

func emit(files fs.FS) (string, error) {
	runner, err := fs.ReadFile(files, runnerTSFile)
	if err != nil {
		return "", fmt.Errorf("read runner: %w", err)
	}

	result := api.Build(api.BuildOptions{
		Bundle:   true,
		Write:    false,
		Format:   api.FormatESModule,
		Platform: api.PlatformNeutral,
		Target:   api.ES2023,
		LogLevel: api.LogLevelSilent,
		Stdin: &api.StdinOptions{
			Contents:   string(runner),
			ResolveDir: "/",
			Sourcefile: runnerTSFile,
			Loader:     api.LoaderTS,
		},
		Plugins: []api.Plugin{memFSPlugin(files)},
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("esbuild emit failed: %s", result.Errors[0].Text)
	}
	if len(result.OutputFiles) == 0 {
		return "", fmt.Errorf("esbuild emit produced no output")
	}

	return string(result.OutputFiles[0].Contents), nil
}

func withRunner(base fs.FS, source string) (fs.FS, error) {
	files, ok := base.(fstest.MapFS)
	if !ok {
		return nil, fmt.Errorf("unsupported tool fs type %T", base)
	}

	out := make(fstest.MapFS, len(files)+1)
	for name, file := range files {
		out[name] = file
	}
	out[runnerTSFile] = &fstest.MapFile{Data: []byte(source)}

	return out, nil
}

func runnerSource(entry string, argsJSON string) string {
	return fmt.Sprintf(`import { execute } from "./%s";
export default await execute(%s, {});
`, entry, argsJSON)
}

func formatDiagnostics(diagnostics []toolboxapi.Diagnostic) string {
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

func memFSPlugin(files fs.FS) api.Plugin {
	return api.Plugin{
		Name: "memfs",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
				switch {
				case strings.HasPrefix(args.Path, "."):
					return api.OnResolveResult{
						Path:      path.Clean(path.Join(args.ResolveDir, args.Path)),
						Namespace: "memfs",
					}, nil
				case strings.HasPrefix(args.Path, "/"):
					return api.OnResolveResult{
						Path:      path.Clean(args.Path),
						Namespace: "memfs",
					}, nil
				default:
					return api.OnResolveResult{}, fmt.Errorf("unsupported import %q", args.Path)
				}
			})

			build.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "memfs"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				name := strings.TrimPrefix(args.Path, "/")
				content, err := fs.ReadFile(files, name)
				if err != nil {
					return api.OnLoadResult{}, err
				}
				contents := string(content)
				return api.OnLoadResult{
					Contents: &contents,
					Loader:   loaderForPath(args.Path),
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
func RunnerSourceForTest(entry string, argsJSON string) string {
	return runnerSource(entry, argsJSON)
}
