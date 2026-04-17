module github.com/solidarity-ai/toolbox

go 1.26.1

require (
	filippo.io/age v1.3.1
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/dop251/goja v0.0.0-20260311135729-065cd970411c
	github.com/evanw/esbuild v0.27.7
	github.com/fastschema/qjs v0.0.6
	github.com/google/cel-go v0.27.0
	github.com/google/go-cmp v0.7.0
	github.com/google/jsonschema-go v0.4.2
	github.com/jinzhu/inflection v1.0.0
	github.com/klauspost/compress v1.18.5
	github.com/mackross/repljs v0.0.0-20260413021201-84857fea19b4
	github.com/mark3labs/mcp-go v0.45.0
	github.com/microsoft/typescript-go v0.0.0-20260318224110-7abde1895437
	github.com/vmihailenco/msgpack/v5 v5.4.1
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.20.0
)

require (
	cel.dev/expr v0.25.1 // indirect
	connectrpc.com/connect v1.19.1 // indirect
	filippo.io/hpke v0.4.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/dop251/goja_nodejs v0.0.0-20211022123610-8dd9abb0616d // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-json-experiment/json v0.0.0-20260214004413-d219187c3433 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20250317173921-a4b03ec1a45e // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/invopop/jsonschema v0.13.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mus-format/common-go v0.0.0-20260324174526-3d8f1741b5a2 // indirect
	github.com/mus-format/mus-go v0.9.1 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/tetratelabs/wazero v1.9.0 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/wk8/go-ordered-map/v2 v2.1.8 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/exp v0.0.0-20251023183803-a4bb9ffd2546 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260316172706-e463d84ca32d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260311181403-84a4fc48630c // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.70.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.48.1 // indirect
)

replace github.com/fastschema/qjs => github.com/mackross/qjs v0.0.7-0.20260409233706-931ab4cd91ce

replace github.com/mackross/repljs => ./third_party/repljs

replace github.com/microsoft/typescript-go => github.com/mackross/typescript-go v0.0.0-20260414161116-99aced908a7c
