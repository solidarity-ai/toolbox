package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/solidarity-ai/toolbox/internal/buildinfo"
)

func runVersion(cmd versionCmd, stdout io.Writer) error {
	version := buildinfo.DisplayVersion()
	if !cmd.JSON {
		fmt.Fprintln(stdout, version)
		return nil
	}
	data, err := json.Marshal(struct {
		Version string `json:"version"`
	}{Version: version})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, string(data))
	return nil
}
