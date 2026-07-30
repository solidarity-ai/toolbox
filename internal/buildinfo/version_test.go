package buildinfo

import "testing"

func TestReleasedBinaryRequiresValidLinkerInjectedVersion(t *testing.T) {
	previous := releaseVersion
	t.Cleanup(func() { releaseVersion = previous })

	releaseVersion = ""
	if _, ok := ReleasedBinary(); ok {
		t.Fatal("ReleasedBinary() succeeded without linker-injected version")
	}

	releaseVersion = "v1.2.3"
	if got, ok := ReleasedBinary(); !ok || got != "v1.2.3" {
		t.Fatalf("ReleasedBinary() = %q, %v; want v1.2.3, true", got, ok)
	}
}
