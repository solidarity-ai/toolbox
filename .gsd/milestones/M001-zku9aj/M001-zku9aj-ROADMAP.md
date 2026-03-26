# M001-zku9aj: M001-zku9aj: M001-zku9aj: Tool Registry, FQN, and Auto-Download — Context

## Vision
M001-zku9aj: M001-zku9aj: Tool Registry, FQN, and Auto-Download — Context

## Slice Overview
| ID | Slice | Risk | Depends | Done | After this |
|----|-------|------|---------|------|------------|
| S01 | Emulate lifecycle + happy-path fixtures | high | — | ⬜ | TBD |
| S02 | Failure scenario test builders | medium | S01 | ⬜ | TBD |
| S03 | FQN types and parsing | low | — | ⬜ | TBD |
| S04 | Local cache layout | low | S03 | ⬜ | TBD |
| S05 | GitHub Releases source + PackageSource interface | high | S01, S02, S03, S04 | ⬜ | TBD |
| S06 | Git-source fallback resolver | high | S01, S02, S03, S04 | ⬜ | TBD |
| S07 | Pseudo-version resolution | medium | S03, S06 | ⬜ | TBD |
| S08 | Resolver orchestration + Builder.AddFromRegistry | medium | S05, S06 | ⬜ | TBD |
| S09 | Toolset file parsing | medium | S08 | ⬜ | TBD |
| S10 | Lockfile generation and verification | medium | S09 | ⬜ | TBD |
| S11 | Replace directives | low | S09 | ⬜ | TBD |
| S12 | CLI commands + end-to-end UAT | low | S10, S11 | ⬜ | TBD |
