# CLAUDE.md

@AGENTS.md

## Claude Code specifics

- `AGENTS.md` is the shared contract for all agents; keep project rules there, not here.
- The maintainer's Claude sessions may have a `things` MCP server connected that points
  at a **real** deployment of this server. Never call its write tools while developing
  or testing; use a local server on the sandbox account instead.
- For non-trivial changes, start from the KI/ADR, propose a plan, and wait for approval
  before editing code.
- Large refactors from the roadmap (ADR-0003 time model, ADR-0005 classifier, ADR-0006
  tool surface) should run in a separate git worktree and land as one PR per KI.
