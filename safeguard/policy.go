package safeguard

import (
	"context"
	"fmt"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

const SchemaVersion = 1

// PackageGuard is the execution-time seam used by registry resolution and
// prepared toolsets. Implementations should retain a last-known-good policy
// when the policy service is temporarily unavailable.
type PackageGuard interface {
	CheckPackage(ctx context.Context, module tooldef.ModulePath, version tooldef.Version) error
}

// Policy is the public revocation document served by the tool registry.
// Entries are exact matches: releases and package versions are immutable, so
// a fixed build is published under a new version instead of mutating a tag.
type Policy struct {
	SchemaVersion int                 `json:"schema_version"`
	GeneratedAt   string              `json:"generated_at"`
	Toolbox       []ToolboxRevocation `json:"toolbox"`
	Packages      []PackageRevocation `json:"packages"`
}

type ToolboxRevocation struct {
	Version     tooldef.Version `json:"version"`
	Reason      string          `json:"reason"`
	AdvisoryURL string          `json:"advisory_url,omitempty"`
}

type PackageRevocation struct {
	Module      tooldef.ModulePath `json:"module"`
	Version     tooldef.Version    `json:"version"`
	Reason      string             `json:"reason"`
	AdvisoryURL string             `json:"advisory_url,omitempty"`
}

type BlockedError struct {
	Subject     string
	Reason      string
	AdvisoryURL string
}

func (e *BlockedError) Error() string {
	if e == nil {
		return "security policy blocked this operation"
	}
	message := fmt.Sprintf("security policy blocked %s", e.Subject)
	if reason := strings.TrimSpace(e.Reason); reason != "" {
		message += ": " + reason
	}
	if advisoryURL := strings.TrimSpace(e.AdvisoryURL); advisoryURL != "" {
		message += " (" + advisoryURL + ")"
	}
	return message
}

func (p Policy) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported security policy schema_version %d", p.SchemaVersion)
	}
	for i, entry := range p.Toolbox {
		if _, err := tooldef.ParseVersion(entry.Version.String()); err != nil {
			return fmt.Errorf("toolbox[%d].version: %w", i, err)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			return fmt.Errorf("toolbox[%d].reason is required", i)
		}
	}
	for i, entry := range p.Packages {
		if _, err := tooldef.ParseModulePath(entry.Module.String()); err != nil {
			return fmt.Errorf("packages[%d].module: %w", i, err)
		}
		if _, err := tooldef.ParseVersion(entry.Version.String()); err != nil {
			return fmt.Errorf("packages[%d].version: %w", i, err)
		}
		if strings.TrimSpace(entry.Reason) == "" {
			return fmt.Errorf("packages[%d].reason is required", i)
		}
	}
	return nil
}

func (p Policy) CheckToolbox(version tooldef.Version) error {
	for _, entry := range p.Toolbox {
		if version != entry.Version {
			continue
		}
		return &BlockedError{
			Subject:     "toolbox@" + version.String(),
			Reason:      entry.Reason,
			AdvisoryURL: entry.AdvisoryURL,
		}
	}
	return nil
}

func (p Policy) CheckPackage(module tooldef.ModulePath, version tooldef.Version) error {
	for _, entry := range p.Packages {
		if !strings.EqualFold(module.String(), entry.Module.String()) || version != entry.Version {
			continue
		}
		return &BlockedError{
			Subject:     module.String() + "@" + version.String(),
			Reason:      entry.Reason,
			AdvisoryURL: entry.AdvisoryURL,
		}
	}

	return nil
}
