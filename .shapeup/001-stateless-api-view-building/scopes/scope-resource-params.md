# Scope: Resource Param Inference

## Hill Position
✓ Done

## Must-Haves
- [x] InferResourceParams in manifest.go (convention: strip 's' + '_id')
- [x] ResourceParam type on PackageTool in tool.go
- [x] ResourceBindings field on DevManifestTool for manifest overrides
- [x] Compile() wires inference and applies overrides
- [x] Resolve() merges resource-level bindings via two-tier model
- [x] Tests: inference rules, manifest overrides, binding propagation

## Notes
- Singularization is naive (strip trailing 's'). Manifest overrides handle edge cases.
- Two-tier model: Package maps param name -> canonical name; toolset maps canonical -> CEL binding.
