# Decisions Register

<!-- Append-only. Never edit or remove existing rows.
     To reverse a decision, add a new row that supersedes it.
     Read this file at the start of any planning or research phase. -->

| # | When | Scope | Decision | Choice | Rationale | Revisable? | Made By |
|---|------|-------|----------|--------|-----------|------------|---------|
| D001 | M001-zku9aj | library | GitHub API testing strategy | vercel-labs/emulate started as Go exec.Command subprocess | Production-fidelity GitHub API emulation without mocked HTTP. Real API responses, stateful repos/releases. User specifically requested emulate. | No | collaborative |
| D002 | M001-zku9aj | arch | FQN type location | In the tool package alongside Package and ResolvedTool | FQN types are identity types that Package will eventually carry. tool package is tiny (62 lines) and the natural home for static identity types. | No | collaborative |
| D003 | M001-zku9aj | arch | When to define the PackageSource abstraction | PackageSource interface defined in S05 alongside first implementation (GitHubReleaseSource) | Defining the interface alongside its first real implementation ensures the contract is grounded. S06 implements the same interface. S08 composes both trivially because the contract is already proven. | No | agent |
| D004 | M001-zku9aj | scope | Toolset file scope for M001 | Minimal: packages map + tools list only. No bindings, credentials, context, resource_bindings. | Bindings and credentials are orthogonal to registry resolution. Including them would bloat the milestone without proving the registry works. Deferred to R023. | Yes — when bindings milestone starts | collaborative |
| D005 | M001-zku9aj | convention | Design authority for registry implementation | docs/rfc-tool-registry.md is the authoritative design document. All context files and research artifacts must reference it. | RFC contains all design decisions, open questions, alternative analysis, and detailed specs. Prevents agents from reinventing answers already decided in the RFC. | No | collaborative |
| D006 | M001-zku9aj/S01/T01 | test-infrastructure | How emulate-based test fixtures create GitHub repositories | Use POST /user/repos and treat repository ownership as the auto-created admin user in emulate v0.3.0 instead of relying on org creation endpoints. | A live probe against emulate v0.3.0 showed /admin/orgs and /orgs/:owner/repos returning 404, while /user/repos succeeds and returns full_name admin/<repo>. Aligning the seed helper with that real behavior keeps the lifecycle tests stable and gives downstream registry slices a consistent owner model. | Yes | agent |
