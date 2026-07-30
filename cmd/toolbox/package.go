package main

import (
	"fmt"
	"io"

	"github.com/solidarity-ai/toolbox/internal/buildinfo"
	"github.com/solidarity-ai/toolbox/packaging"
	tooldef "github.com/solidarity-ai/toolbox/tool"
)

func runPackagePack(cmd packagePackCmd, stdout io.Writer) error {
	version, released := buildinfo.ReleaseVersion()
	return packPackage(cmd, version, released, stdout)
}

func packPackage(cmd packagePackCmd, version tooldef.Version, released bool, stdout io.Writer) error {
	if !released {
		return fmt.Errorf("package packing requires a released Toolbox build")
	}
	result, err := packaging.Pack(cmd.Directory, cmd.Out, version)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "archive:  %s\nmanifest: %s\n", result.ArchivePath, result.ManifestPath)
	return nil
}
