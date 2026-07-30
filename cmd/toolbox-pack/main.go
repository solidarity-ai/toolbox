package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/solidarity-ai/toolbox/internal/buildinfo"
	"github.com/solidarity-ai/toolbox/packaging"
)

func main() {
	outDir := flag.String("out", ".", "output directory for archive and manifest")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: toolbox-pack [-out DIR] <package-dir>")
		os.Exit(1)
	}
	srcDir := flag.Arg(0)

	packerVersion, ok := buildinfo.ReleaseVersion()
	if !ok {
		fmt.Fprintln(os.Stderr, "error: toolbox-pack must be installed from a released Toolbox version")
		os.Exit(1)
	}
	result, err := packaging.Pack(srcDir, *outDir, packerVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("archive:  %s\n", result.ArchivePath)
	fmt.Printf("manifest: %s\n", result.ManifestPath)
}
