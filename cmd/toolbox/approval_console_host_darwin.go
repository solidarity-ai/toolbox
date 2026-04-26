//go:build darwin

package main

import (
	"bytes"
	"encoding/binary"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func approvalConsoleClientHostForPID(pid int) approvalConsoleClientHost {
	if pid <= 0 {
		return approvalConsoleClientHost{}
	}
	child, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || child == nil || child.Eproc.Ppid <= 0 {
		return approvalConsoleClientHost{}
	}
	parentPID := int(child.Eproc.Ppid)
	parent, err := unix.SysctlKinfoProc("kern.proc.pid", parentPID)
	if err != nil || parent == nil {
		return approvalConsoleClientHost{ParentPID: parentPID}
	}
	command := darwinProcessCommand(parentPID)
	if command == "" {
		command = cleanApprovalProcessCommand(unix.ByteSliceToString(parent.Proc.P_comm[:]))
	}
	return approvalConsoleClientHost{
		Host:          classifyApprovalClientHost(command),
		ParentPID:     parentPID,
		ParentCommand: command,
	}
}

func darwinProcessCommand(pid int) string {
	executable, args := darwinProcessArgs(pid)
	command := cleanApprovalProcessCommand(strings.Join(args, " "))
	if classifyApprovalClientHost(command) != "" {
		return command
	}
	if classifyApprovalClientHost(executable) != "" {
		label := filepath.Base(executable)
		if len(args) > 1 {
			var flags []string
			for _, arg := range args[1:] {
				if strings.HasPrefix(arg, "-") {
					flags = append(flags, arg)
				}
			}
			if len(flags) > 0 {
				label += " " + strings.Join(flags, " ")
			}
		}
		return cleanApprovalProcessCommand(label)
	}
	if command != "" {
		return command
	}
	if executable != "" {
		return cleanApprovalProcessCommand(filepath.Base(executable))
	}
	return ""
}

func darwinProcessArgs(pid int) (string, []string) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) <= 4 {
		return "", nil
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	if argc < 0 {
		argc = 0
	}
	raw = raw[4:]

	idx := bytes.IndexByte(raw, 0)
	if idx < 0 {
		return cleanApprovalProcessCommand(string(raw)), nil
	}
	executable := cleanApprovalProcessCommand(string(raw[:idx]))
	raw = raw[idx+1:]
	for len(raw) > 0 && raw[0] == 0 {
		raw = raw[1:]
	}

	args := make([]string, 0, argc)
	for len(raw) > 0 && (argc == 0 || len(args) < argc) {
		idx = bytes.IndexByte(raw, 0)
		if idx < 0 {
			idx = len(raw)
		}
		if idx > 0 {
			args = append(args, string(raw[:idx]))
		}
		if idx >= len(raw) {
			break
		}
		raw = raw[idx+1:]
	}
	return executable, args
}
