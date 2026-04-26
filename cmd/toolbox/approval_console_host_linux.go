//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func approvalConsoleClientHostForPID(pid int) approvalConsoleClientHost {
	parentPID := linuxParentPID(pid)
	if parentPID <= 0 {
		return approvalConsoleClientHost{}
	}
	command := linuxProcessCommand(parentPID)
	return approvalConsoleClientHost{
		Host:          classifyApprovalClientHost(command),
		ParentPID:     parentPID,
		ParentCommand: command,
	}
}

func linuxParentPID(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	stat := string(data)
	commEnd := strings.LastIndexByte(stat, ')')
	if commEnd < 0 || commEnd+2 >= len(stat) {
		return 0
	}
	fields := strings.Fields(stat[commEnd+2:])
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

func linuxProcessCommand(pid int) string {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		if command := cleanApprovalProcessCommand(string(data)); command != "" {
			return command
		}
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		return cleanApprovalProcessCommand(string(data))
	}
	return ""
}
