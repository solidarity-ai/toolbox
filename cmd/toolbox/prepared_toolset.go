package main

import (
	"context"
	"fmt"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetctl"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

func loadPreparedToolset(ctx context.Context, toolsetPath, effects string, opts secretStoreOptions) (toolset.PreparedToolset, error) {
	resolver, err := newResolver()
	if err != nil {
		return toolset.PreparedToolset{}, err
	}

	ts, err := toolsetfile.Load(toolsetPath)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}

	prepared, err := ts.Prepare(ctx, resolver, toolset.Config{
		CredentialPolicySource: newCredentialPolicySource(opts),
	})
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	return filterPreparedToolsetByEffects(prepared, effects)
}

func newFileToolsetBackend(ctx context.Context, toolsetPath, effects string, opts secretStoreOptions, consumer toolsetctl.PreparedToolConsumer) (*toolsetctl.FileBackend, error) {
	resolver, err := newResolver()
	if err != nil {
		return nil, err
	}
	allowedEffects, err := parseEffectFilter(effects)
	if err != nil {
		return nil, err
	}

	repo := newCredentialRepository(opts)
	return toolsetctl.NewFileBackend(ctx, toolsetctl.FileBackendOptions{
		ToolsetPath:          toolsetPath,
		Resolver:             resolver,
		Config:               toolset.Config{CredentialPolicySource: repo},
		SearchClientFactory:  func() (toolsetctl.SearchClient, error) { return newToolRegistrySearchClient() },
		CredentialRepository: repo,
		Consumer:             consumer,
		AllowedEffects:       allowedEffects,
	})
}

func filterPreparedToolsetByEffects(prepared toolset.PreparedToolset, effects string) (toolset.PreparedToolset, error) {
	allowed, err := parseEffectFilter(effects)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}
	if len(allowed) == 0 {
		return prepared, nil
	}
	return prepared.FilterTools(func(tool toolset.PreparedTool) bool {
		return allowed[tool.Effect]
	}), nil
}

func parseEffectFilter(raw string) (map[tooldef.Effect]bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	allowed := make(map[tooldef.Effect]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		effect, err := parseEffectName(part)
		if err != nil {
			return nil, err
		}
		allowed[effect] = true
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("effects=%q did not include any effect names; use readonly,reversible,irreversible", raw)
	}
	return allowed, nil
}

func parseEffectName(raw string) (tooldef.Effect, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "readonly":
		return tooldef.EffectReadOnly, nil
	case "reversible":
		return tooldef.EffectReversible, nil
	case "irreversible":
		return tooldef.EffectIrreversible, nil
	default:
		return "", fmt.Errorf("effects=%q is not supported; use readonly,reversible,irreversible", raw)
	}
}
