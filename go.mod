module github.com/solidarity-ai/toolbox

go 1.26.1

require (
	filippo.io/age v1.3.1
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/dop251/goja v0.0.0-20260311135729-065cd970411c
	github.com/evanw/esbuild v0.27.4
	github.com/fastschema/qjs v0.0.6
	github.com/google/cel-go v0.27.0
	github.com/google/go-cmp v0.7.0
	github.com/google/jsonschema-go v0.4.2
	github.com/jinzhu/inflection v1.0.0
	github.com/klauspost/compress v1.18.5
	github.com/mark3labs/mcp-go v0.45.0
	github.com/microsoft/typescript-go v0.0.0-20260318224110-7abde1895437
	github.com/solidarity-ai/repl v0.0.0
	github.com/vmihailenco/msgpack/v5 v5.4.1
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.20.0
)

require (
	cel.dev/expr v0.25.1 // indirect
	filippo.io/hpke v0.4.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/go-json-experiment/json v0.0.0-20260214004413-d219187c3433 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/invopop/jsonschema v0.13.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/tetratelabs/wazero v1.9.0 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260316172706-e463d84ca32d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260311181403-84a4fc48630c // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/fastschema/qjs => github.com/mackross/qjs v0.0.7-0.20260409233706-931ab4cd91ce

replace github.com/microsoft/typescript-go => github.com/mackross/typescript-go v0.0.0-20260411003622-6a0db70e7185

replace github.com/solidarity-ai/repl => ../include-tools/repljs
