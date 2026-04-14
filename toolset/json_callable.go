package toolset

import (
	"strings"

	"github.com/microsoft/typescript-go/toolbox"
)

func jsonCallableForTool(tool PreparedTool) (bool, string) {
	paramsType := preparedParamsType(tool)
	if paramsType == nil {
		return true, ""
	}
	reasons := paramsType.CannotJSON()
	if len(reasons) == 0 {
		return true, ""
	}
	return false, strings.Join(reasons, "; ")
}

func preparedParamsType(tool PreparedTool) *toolbox.TSType {
	sig := preparedParamSignature(tool)
	if sig == nil {
		return nil
	}
	pt := sig.ParamsAsObject()
	if pt == nil {
		return nil
	}
	if len(tool.hiddenParams) > 0 {
		names := make([]string, 0, len(tool.hiddenParams))
		for name := range tool.hiddenParams {
			names = append(names, name)
		}
		pt = pt.RemoveProperties(names...)
	}
	for name, value := range tool.boundLiterals() {
		pt = pt.SetPropertyLiteral(name, value)
	}
	return pt
}

func preparedParamSignature(tool PreparedTool) *toolbox.FuncSignature {
	if tool.Sig == nil {
		return nil
	}
	sig := tool.Sig
	for _, ap := range tool.accountParams {
		sig = sig.AddParam(ap.ParamName, toolbox.NewStringLiteralUnion(ap.Accounts), ap.Description, false)
	}
	return sig
}
