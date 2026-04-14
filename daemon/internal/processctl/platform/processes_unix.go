//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	daemonStopTimeout      = 15 * time.Second
	daemonForceStopTimeout = 5 * time.Second
	daemonStopPollInterval = 50 * time.Millisecond
)

type processSignalFunc func(pid int, sig syscall.Signal) error
type processAliveFunc func(pid int) bool

func StopAllRunningServers(logf func(string, ...any)) ([]int, error) {
	pids, err := listRunningDaemonPIDs()
	if err != nil {
		return nil, err
	}
	if len(pids) == 0 {
		return nil, nil
	}

	if err := stopRunningDaemonPIDs(
		pids,
		logf,
		daemonStopTimeout,
		daemonForceStopTimeout,
		syscall.Kill,
		IsProcessAlive,
		time.Sleep,
	); err != nil {
		return pids, err
	}
	return pids, nil
}

func stopRunningDaemonPIDs(
	pids []int,
	logf func(string, ...any),
	gracefulTimeout time.Duration,
	forceTimeout time.Duration,
	signalProcess processSignalFunc,
	isAlive processAliveFunc,
	sleep func(time.Duration),
) error {
	waiting := make([]int, 0, len(pids))
	var errs []string
	for _, pid := range pids {
		if err := signalProcess(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, fmt.Sprintf("signal pid %d with SIGTERM: %v", pid, err))
			continue
		}
		waiting = append(waiting, pid)
	}
	if len(waiting) == 0 {
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}

	if logf != nil {
		logf("sent SIGTERM to toolbox daemons: %s; waiting up to %s before SIGKILL", joinPIDs(waiting), gracefulTimeout)
	}
	waiting = waitForPIDsToExit(waiting, gracefulTimeout, isAlive, sleep)
	if len(waiting) == 0 {
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}

	if logf != nil {
		logf("toolbox daemons still running after %s: %s; sending SIGKILL", gracefulTimeout, joinPIDs(waiting))
	}

	forced := waiting[:0]
	for _, pid := range waiting {
		if err := signalProcess(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, fmt.Sprintf("signal pid %d with SIGKILL: %v", pid, err))
			continue
		}
		forced = append(forced, pid)
	}
	waiting = waitForPIDsToExit(forced, forceTimeout, isAlive, sleep)
	if len(waiting) > 0 {
		errs = append(errs, fmt.Sprintf("timed out waiting for daemon pids to exit after SIGKILL: %s", joinPIDs(waiting)))
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func waitForPIDsToExit(pids []int, timeout time.Duration, isAlive processAliveFunc, sleep func(time.Duration)) []int {
	remaining := append([]int(nil), pids...)
	if len(remaining) == 0 {
		return nil
	}

	deadline := time.Now().Add(timeout)
	for {
		next := remaining[:0]
		for _, pid := range remaining {
			if isAlive(pid) {
				next = append(next, pid)
			}
		}
		remaining = next
		if len(remaining) == 0 {
			return nil
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			return append([]int(nil), remaining...)
		}
		sleep(daemonStopPollInterval)
	}
}

func listRunningDaemonPIDs() ([]int, error) {
	output, err := exec.Command("ps", "-Ao", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	return parseRunningDaemonPIDs(string(output)), nil
}

func parseRunningDaemonPIDs(output string) []int {
	seen := make(map[int]struct{})
	var pids []int
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		pidField, rest, ok := splitPIDField(line)
		if !ok {
			continue
		}

		pid, err := strconv.Atoi(pidField)
		if err != nil || pid <= 0 {
			continue
		}
		if !isDaemonServeCommand(rest) {
			continue
		}
		if _, exists := seen[pid]; exists {
			continue
		}
		seen[pid] = struct{}{}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}

func splitPIDField(line string) (pidField string, rest string, ok bool) {
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' || line[i] == '\t' {
			pidField = line[:i]
			rest = strings.TrimSpace(line[i+1:])
			return pidField, rest, pidField != "" && rest != ""
		}
	}
	return "", "", false
}

func isDaemonServeCommand(command string) bool {
	idx := strings.Index(command, " _daemon serve")
	if idx < 0 {
		return false
	}
	tail := command[idx+len(" _daemon serve"):]
	return tail == "" || tail[0] == ' ' || tail[0] == '\t'
}

func joinPIDs(pids []int) string {
	parts := make([]string, 0, len(pids))
	for _, pid := range pids {
		parts = append(parts, strconv.Itoa(pid))
	}
	return strings.Join(parts, " ")
}
