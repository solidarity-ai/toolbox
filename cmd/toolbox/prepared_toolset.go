package main

import (
	"context"

	"github.com/solidarity-ai/toolbox/toolset"
	"github.com/solidarity-ai/toolbox/toolsetfile"
)

func loadPreparedToolset(ctx context.Context, toolsetPath string) (toolset.PreparedToolset, error) {
	resolver, err := newResolver()
	if err != nil {
		return toolset.PreparedToolset{}, err
	}

	ts, err := toolsetfile.Load(toolsetPath)
	if err != nil {
		return toolset.PreparedToolset{}, err
	}

	return ts.Prepare(ctx, resolver, toolset.Config{
		CredentialPolicySource: newCredentialPolicySource(),
	})
}
