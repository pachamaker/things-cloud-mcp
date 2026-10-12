# ADR-0001: Fork identity and SDK strategy

## Status
Accepted (2026-10-11): option B. Spike verdict "conditional GO" — see
`docs/2026-10-10-upstream-sdk-spike.md`. Implemented on `main` by PR #6 (2026-10-10,
`15e5b6b`), including the phase 2 safety adapters (PR #4) and the phase 3 view
classifier (ADR-0005). Upstream PRs from the spike report §6 are still open items.
For `v2.0.0` (2026-10-12) the owner chose the soft-fork fallback of decision 2 if the
mirror-field PRs upstream are not released in time: `replace` to `pachamaker/things-cloud-sdk`
pinned to a commit or tag, back to upstream once released (`docs/roadmap.md`, Releases).

## Context
- Lineage: `nicolai86/things-cloud-sdk` (MIT, 2017, Raphael Randschau; dormant since
  2022) → `arthursoares/things-cloud-sdk` (SDK + sync engine + CLI, **actively
  maintained**: v0.6.0 on 2026-09-06, last push 2026-09-20) →
  `mattydsmith/things-cloud-mcp` (embedded copy of that SDK plus an MCP server;
  **archived 2026-09-27**, last commit 2026-03-16) → `pachamaker/things-cloud-mcp`.
- This repository is a frozen March-2026 copy of the SDK with the server inside it. Its
  module path is still `github.com/arthursoares/things-cloud-sdk`.
- Since March, upstream fixed problems this copy still has: `Task7` reading and a
  validated `Task7` write policy (KI-25), Base58 generation with fuzz tests (KI-26),
  backup + replay of caches after reader fixes, sync-progress validation, robustness
  and chaos tests, golangci config. Upstream has **no MCP server**.
- This copy has things upstream may not: pagination in `QueryOpts`, the `tir`
  carry-over fix, schema v4 migration, Anytime/Someday/Upcoming queries, stability
  fixes from March 2026. The APIs have diverged (e.g. upstream `QueryOpts` has no
  `Limit`/`Offset`).
- Active forks exist with overlapping fixes, notably
  [ZhehanZhang/things-cloud-mcp](https://github.com/ZhehanZhang/things-cloud-mcp)
  (Task7 ingest, timezone, write safety, Base58 redraw).

## Options

| Option | Pros | Cons |
|---|---|---|
| A. Keep the embedded SDK copy, port fixes by hand | No dependency; full control | We re-do protocol work upstream already did; drift grows; we own Task7 forever |
| B. **Depend on `arthursoares/things-cloud-sdk` as a Go module; this repo keeps only the MCP server and server-specific read model** | Protocol fixes (Task7, Base58, replay) arrive with `go get -u`; we focus on MCP, views, auth, deployment; import paths in `server/` stay the same | Upstream API differs from our copy (adapter work); upstream could stall or break us; some of our sync features must be upstreamed or rebuilt locally |
| C. Rebase on the ZhehanZhang fork | Many server fixes at once | Another single-maintainer fork; still the frozen embedded SDK underneath |

## Decision
Option B, gated by a one-day compatibility spike.

1. **Spike** (branch `spike-upstream-sdk`): point `go.mod` at
   `github.com/arthursoares/things-cloud-sdk v0.6.x`, delete the embedded SDK, `sync/`,
   `state/`, `syncutil/`, `cmd/` from this module, and make `server/` compile. Record
   every API gap. Accept if the gaps can be closed with a thin local read-model layer
   (view classifier per ADR-0005, pagination) in under ~2 days of work.
2. If accepted:
   - Rename this module to `github.com/pachamaker/things-cloud-mcp`.
   - Layout: `cmd/things-mcp` (server main), `internal/...` (transport, engine,
     domain, read model), no exported Go API.
   - Upstream generic fixes (pagination, `tir` carry-over, view rules) as PRs to
     arthursoares; keep MCP-specific logic here.
   - Pin an exact upstream version; upgrades go through CI plus the conformance suite.
3. If rejected: fall back to A, but immediately port upstream `Task7` reading,
   `base58.go`, and replay as discrete commits with attribution, and track upstream
   releases in the roadmap.
4. Regardless of the option:
   - Versioning: semantic versions from `v2.0.0`; version injected with `-ldflags -X`
     and reported in MCP `serverInfo`.
   - Licence: keep MIT and the original copyright line; add the fork's line for new
     work; credit upstream in the README.
   - Naming and description make clear the project is unofficial and not affiliated
     with Cultured Code (`docs/legal-and-risk.md`).

## Consequences
- Easier: protocol changes by Cultured Code (like `Task7`) are absorbed by a maintained
  library; this repo becomes small and focused on what the owner needs (remote MCP,
  correct views, safety, auth).
- Harder: an external dependency on a single maintainer; divergence work during the
  spike; the ~14 CLI tools leave this repo (upstream has its own `things-cli`).
- Open PRs #1 and #2 touch files that move under option B; merge them first, then
  carry their logic into the new layout.
