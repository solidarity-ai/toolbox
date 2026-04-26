package main

import (
	"path/filepath"
	"strings"
)

type approvalConsoleClientHost struct {
	Host          string
	ParentPID     int
	ParentCommand string
}

func classifyApprovalClientHost(command string) string {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if normalized == "" {
		return ""
	}
	base := normalized
	if fields := strings.Fields(base); len(fields) > 0 {
		base = fields[0]
	}
	base = strings.TrimSuffix(filepath.Base(base), ".exe")

	switch {
	case strings.Contains(normalized, "claude"):
		return "Claude Code"
	case strings.Contains(normalized, "codex"):
		return "Codex"
	case strings.Contains(normalized, "cursor"):
		return "Cursor"
	case strings.Contains(normalized, "windsurf"):
		return "Windsurf"
	case strings.Contains(normalized, "opencode"):
		return "OpenCode"
	case base == "code" || strings.Contains(normalized, "visual studio code"):
		return "VS Code"
	case strings.Contains(normalized, "zed"):
		return "Zed"
	default:
		return ""
	}
}

func cleanApprovalProcessCommand(command string) string {
	command = strings.ReplaceAll(command, "\x00", " ")
	command = strings.Join(strings.Fields(command), " ")
	if len(command) > 160 {
		return strings.TrimSpace(command[:157]) + "..."
	}
	return command
}
