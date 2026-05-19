//go:build !windows

package platform

import (
	"fmt"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestParseRunningDaemonPIDs(t *testing.T) {
	output := `
  101 /Applications/Toolbox/bin/toolbox _daemon serve
  102 toolbox daemon stop
  103 /Applications/Toolbox Dev/bin/toolbox _daemon serve --foreground
  104 toolbox _daemon serve-stdio
  105 /usr/bin/nano _daemon serve
  101 /Applications/Toolbox/bin/toolbox _daemon serve
`

	got := parseRunningDaemonPIDs(output)
	want := []int{101, 103}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseRunningDaemonPIDs() = %#v, want %#v", got, want)
	}
}

func TestSplitPIDField(t *testing.T) {
	pid, rest, ok := splitPIDField("54983 /Applications/Toolbox Dev/bin/toolbox _daemon serve")
	if !ok {
		t.Fatal("splitPIDField() ok = false, want true")
	}
	if pid != "54983" {
		t.Fatalf("pid = %q, want %q", pid, "54983")
	}
	if rest != "/Applications/Toolbox Dev/bin/toolbox _daemon serve" {
		t.Fatalf("rest = %q", rest)
	}
}

func TestStopRunningDaemonPIDsEscalatesToSIGKILL(t *testing.T) {
	alive := map[int]bool{101: true, 103: true}
	var signals []string
	var logs []string

	err := stopRunningDaemonPIDs(
		[]int{101, 103},
		func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
		0,
		0,
		func(pid int, sig syscall.Signal) error {
			signals = append(signals, fmt.Sprintf("%d:%d", pid, sig))
			if sig == syscall.SIGKILL {
				alive[pid] = false
			}
			return nil
		},
		func(pid int) bool {
			return alive[pid]
		},
		func(time.Duration) {},
	)
	if err != nil {
		t.Fatalf("stopRunningDaemonPIDs() error: %v", err)
	}

	wantSignals := []string{
		fmt.Sprintf("101:%d", syscall.SIGTERM),
		fmt.Sprintf("103:%d", syscall.SIGTERM),
		fmt.Sprintf("101:%d", syscall.SIGKILL),
		fmt.Sprintf("103:%d", syscall.SIGKILL),
	}
	if !reflect.DeepEqual(signals, wantSignals) {
		t.Fatalf("signals = %#v, want %#v", signals, wantSignals)
	}

	wantLogs := []string{
		"sent SIGTERM to toolbox daemons: 101 103; waiting up to 0s before SIGKILL",
		"toolbox daemons still running after 0s: 101 103; sending SIGKILL",
	}
	if !reflect.DeepEqual(logs, wantLogs) {
		t.Fatalf("logs = %#v, want %#v", logs, wantLogs)
	}
}
