# Documentation index

Start with [`../AGENTS.md`](../AGENTS.md) if you are about to change code.

## Living documents (kept current)

| Document | Purpose |
|---|---|
| [`architecture.md`](architecture.md) | Context, containers, code map, runtime flows, domain model, target architecture |
| [`protocol/things-cloud.md`](protocol/things-cloud.md) | Things Cloud wire protocol with evidence levels; procedure for adding a write |
| [`known-issues.md`](known-issues.md) | Defect register (`KI-NN`) with root causes and fix direction |
| [`roadmap.md`](roadmap.md) | Phased plan with exit criteria |
| [`adr/`](adr/README.md) | Architecture decision records |
| [`deployment/vps.md`](deployment/vps.md) | Self-hosting on a VPS for Claude and ChatGPT |
| [`testing/sandbox-e2e.md`](testing/sandbox-e2e.md) | Sandbox end-to-end tests: safety model, setup, seed list, cadence |
| [`../testdata/captures/`](../testdata/captures/README.md) | Sanitized payloads the official Things apps wrote: evidence for the protocol notes |
| [`legal-and-risk.md`](legal-and-risk.md) | Legal and Terms-of-Service risk notes (not legal advice) |
| [`installation.md`](installation.md) | Legacy Fly.io installation guide (superseded by `deployment/vps.md`) |
| [`skills.md`](skills.md) | Writing Claude Skills on top of the tools |
| [`sdk.md`](sdk.md) | SDK and sync engine usage, written for the embedded copy; the SDK is now the [`arthursoares/things-cloud-sdk`](https://github.com/arthursoares/things-cloud-sdk) dependency, whose README is authoritative |
| [`endpoints-and-things-cloud.md`](endpoints-and-things-cloud.md) | How REST/MCP endpoints map to Things Cloud calls |

## Historical records (do not update; read for context)

| Document | What it records |
|---|---|
| [`client-side-bugs.md`](client-side-bugs.md) | Early SDK bugs that crashed Things: `st` values, Base58 IDs, timestamps |
| [`crash-investigation.md`](crash-investigation.md) | 2026-02-16 crash after server writes; unreproduced root cause |
| [`2026-02-17-audit.md`](2026-02-17-audit.md) | Code audit, 30 findings |
| [`2026-02-17-code-review.md`](2026-02-17-code-review.md) | Code review |
| [`2026-02-17-fix-plan-20260217T003951Z.md`](2026-02-17-fix-plan-20260217T003951Z.md) | Fix plan for the audit |
| [`2026-02-23-api-capabilities-review.md`](2026-02-23-api-capabilities-review.md) | Capability vs SDK coverage, wire field inventory |
| [`2026-03-05-mcp-today-mismatch-incident.md`](2026-03-05-mcp-today-mismatch-incident.md) | Today mismatch: cursor bug, sync race |
| [`2026-03-14-code-review.md`](2026-03-14-code-review.md) | Full code review |
| [`2026-03-15-stability-wrap-up.md`](2026-03-15-stability-wrap-up.md) | Stability pass summary |
| [`2026-10-10-upstream-sdk-spike.md`](2026-10-10-upstream-sdk-spike.md) | Upstream SDK spike for ADR-0001: API and behaviour gaps (C1–C9, B1–B14), phases 1–3 of the migration |
| [`plans/`](plans/) | Persistent sync engine design and implementation plan (Feb 2026) |
| [`BACKLOG.md`](BACKLOG.md) | Upstream backlog (superseded by `roadmap.md` and `known-issues.md`) |

New investigations: `docs/YYYY-MM-DD-<topic>.md`, ending with links to the KI/ADR they
produced.
