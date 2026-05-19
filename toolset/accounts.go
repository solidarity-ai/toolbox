package toolset

import (
	"fmt"
	"sort"
)

// buildAccountParams validates one tool's credential accounts against existing
// tool params and returns the list of account params that need to be added to
// schemas. Only credentials with 2+ accounts produce a param.
func buildAccountParams(tool PreparedTool, credAccounts map[string][]string) ([]AccountParam, error) {
	if len(credAccounts) == 0 {
		return nil, nil
	}

	// Collect credentials that need an account param (2+ accounts).
	var params []AccountParam
	for credName, accounts := range credAccounts {
		if len(accounts) < 2 {
			continue
		}
		sorted := make([]string, len(accounts))
		copy(sorted, accounts)
		sort.Strings(sorted)
		params = append(params, AccountParam{
			ParamName:   credName + "_account",
			CredName:    credName,
			Accounts:    sorted,
			Description: fmt.Sprintf("Account for %s credential", credName),
		})
	}
	if len(params) == 0 {
		return nil, nil
	}

	// Sort for deterministic ordering.
	sort.Slice(params, func(i, j int) bool {
		return params[i].ParamName < params[j].ParamName
	})

	// Check for collisions with existing tool params.
	schema := paramsSchema(tool)
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return params, nil
	}
	for _, ap := range params {
		if _, exists := props[ap.ParamName]; exists {
			return nil, fmt.Errorf(
				"tool %q already declares param %q which collides with auto-generated credential account param",
				tool.Name, ap.ParamName,
			)
		}
	}

	return params, nil
}

func paramsSchema(tool PreparedTool) map[string]any {
	if tool.Sig == nil {
		return nil
	}
	pt := tool.Sig.ParamsAsObject()
	if pt == nil {
		return nil
	}
	return pt.ToJSONSchema()
}

func extractAccountParams(fullParams map[string]any, credNames []string) map[string]string {
	accounts := make(map[string]string)
	for _, name := range credNames {
		paramName := name + "_account"
		if acct, ok := fullParams[paramName].(string); ok {
			accounts[name] = acct
		}
	}
	return accounts
}
