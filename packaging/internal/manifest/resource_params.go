package manifest

import (
	"path/filepath"
	"strings"

	tooldef "github.com/solidarity-ai/toolbox/tool"
)

// isCollectionMethod returns true for verbs that operate on a collection
// (and therefore do NOT need the deepest resource ID).
// All other verbs (get, update, delete, etc.) are member operations
// that require the deepest resource ID. This default is safer since
// unknown verbs will include all resource IDs.
func isCollectionMethod(verb string) bool {
	switch verb {
	case "list", "create", "add", "search", "find", "new", "send", "post":
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
// Collection methods (list, create, search, etc.) exclude the deepest resource ID
// since they operate on the collection. All other methods (get, update, delete,
// etc.) include it since they operate on a specific member.
func InferResourceParams(entryTS string) []tooldef.ResourceParam {
	base := filepath.Base(entryTS)
	base = strings.TrimSuffix(base, filepath.Ext(base))

	parts := strings.Split(base, ".")
	if len(parts) < 3 {
		return nil
	}

	verb := parts[len(parts)-1]
	resources := parts[:len(parts)-1]

	// Default: include all resource IDs (member operation).
	// Collection methods exclude the deepest resource ID.
	count := len(resources)
	if isCollectionMethod(verb) {
		count = len(resources) - 1
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
