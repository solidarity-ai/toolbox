package buildinfo

import (
	"runtime/debug"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const toolboxModule = "github.com/solidarity-ai/toolbox"

// releaseVersion is populated by the release workflow with -ldflags -X and is
// the only signal that enables released-binary revocation checks.
var releaseVersion string

func ReleasedBinary() (tooldef.Version, bool) {
	version, err := tooldef.ParseVersion(strings.TrimSpace(releaseVersion))
	return version, err == nil && version.IsRelease()
}

// ReleaseVersion returns the Toolbox release represented by this build.
func ReleaseVersion() (tooldef.Version, bool) {
	if version, ok := ReleasedBinary(); ok {
		return version, true
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path != toolboxModule {
		return "", false
	}
	version, err := tooldef.ParseVersion(info.Main.Version)
	return version, err == nil && version.IsRelease()
}

func DisplayVersion() string {
	if version, ok := ReleasedBinary(); ok {
		return version.String()
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
