package manifest

import (
	"path/filepath"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// methodNeedsDeepestID returns true if the verb requires the deepest resource ID.
func methodNeedsDeepestID(verb string) bool {
	switch verb {
	case "get", "update", "delete", "remove", "set", "put", "patch", "replace", "edit":
		return true
	default:
		return false
	}
}

// InferResourceParams derives resource parameters from the tool entry filename.
//
// Convention: "account.tickets.list.ts" -> resources are ["account", "tickets"],
// verb is "list". Each resource (except possibly the last) gets a param named
// by singularizing (strip trailing 's') and appending '_id'.
//
// For list/create/add/search methods, the deepest resource ID is NOT included
// (the verb operates on the collection). For get/update/delete, the deepest
// resource ID IS included (the verb operates on a specific item).
func InferResourceParams(entryTS string) []tooldef.ResourceParam {
	base := filepath.Base(entryTS)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	parts := strings.Split(base, ".")
	if len(parts) < 3 {
		return nil
	}

	verb := parts[len(parts)-1]
	resources := parts[:len(parts)-1]

	count := len(resources) - 1
	if methodNeedsDeepestID(verb) {
		count = len(resources)
	}

	if count <= 0 {
		return nil
	}

	params := make([]tooldef.ResourceParam, count)
	for i := 0; i < count; i++ {
		name := singularize(resources[i]) + "_id"
		params[i] = tooldef.ResourceParam{Name: name, BindingName: name}
	}
	return params
}

// singularize naively strips a trailing 's' from a word.
func singularize(s string) string {
	if strings.HasSuffix(s, "s") && len(s) > 1 {
		return s[:len(s)-1]
	}
	return s
}
