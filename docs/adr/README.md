# Architecture Decision Records

Format: Status · Context · Options (when there is a real choice) · Decision ·
Consequences. Files are numbered and never renumbered; superseded ADRs stay with a
pointer to their successor.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-fork-identity.md) | Fork identity and SDK strategy (depend on upstream SDK) | Accepted |
| [0002](0002-remote-deployment-and-auth.md) | Remote deployment on a VPS with OAuth 2.1 | Proposed |
| [0003](0003-time-model.md) | Civil dates in the owner's timezone | Accepted |
| [0004](0004-write-safety.md) | Write safety: allowlisted mutations, preconditions, audit | Proposed |
| [0005](0005-view-semantics.md) | View semantics owned by one classifier, verified against real Things data | Proposed |
| [0006](0006-tool-surface-v2.md) | MCP tool surface v2 | Proposed |
| [0007](0007-engineering-workflow.md) | Engineering workflow | Proposed |

To propose a decision: copy the structure of an existing ADR, use the next number, set
status `Proposed`, and link it from the KI or roadmap item it serves.
