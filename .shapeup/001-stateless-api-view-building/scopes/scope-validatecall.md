# Scope: ValidateCall

## Hill Position
✓ Done

## Must-Haves
- [x] ValidateCall method on ResolvedToolset in toolset/validate.go
- [x] Value binding evaluation (inject hidden params)
- [x] Check expression evaluation (policy enforcement)
- [x] Wire into invoke.Run() and RunWithVFS()
- [x] End-to-end tests: hidden param injection (10+5=15), check blocking (a > max)

## Notes
- ValidateCall returns full params (hidden + visible) for runtime dispatch.
- Check failure returns clear error: "check failed for param X on tool Y".
