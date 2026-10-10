# Roadmap

Phased plan from the inherited codebase (baseline `3164e8a`) to a safe, correct,
remotely usable single-user server. Each phase has exit criteria; do not start a phase
before the previous one's P0 items are done. IDs refer to `known-issues.md` and `adr/`.

## Phase −1 — Hotfixes before any other use (1–2 days)

These two defects affect every user of a current Things app and can permanently
damage data; fix them before relying on the server again.

| Item | Refs | Size | Status |
|---|---|---|---|
| Base58 ID generation: never emit an ID that loses a leading zero byte (redraw), tighten validation, fuzz test | KI-26 | S | ✅ PR #3 |
| Read `Task7` (+ skip `Settings5`, `Command*`), fail loudly on unknown task kinds | KI-25 | S–M | ✅ via upstream SDK (migration PR) |
| Merge PR #2 (reject `repeat`), extend to `repeat: "none"` | KI-01 | S | ✅ PR #2 |
| Merge PR #1 (Today/canceled/sentinel) as an interim improvement | KI-02, KI-04, KI-05 | S | ✅ PR #1 |
| Upstream SDK spike (go/no-go for ADR-0001 option B) | ADR-0001 | S | ✅ GO; phase 2 adapters merged into the spike branch (PR #4) |

### Upstream migration, phase 3 (before any deployment)

| Item | Refs | Status |
|---|---|---|
| Merge `main` (PRs #1–#3, these docs) into `spike-upstream-sdk`; keep PR #2 behaviour; prefer the PR #4 ID adapter over PR #3's local `base58.go` | ADR-0001 | ✅ |
| View classifier over upstream `State.AllTasks` carrying PR #1's rules (upstream `TasksIn*` lose two KI-02 cases) | ADR-0005, spike report §4 B3 | ✅ `server/views.go`; two `tir`-only cases differ (KI-02) |
| Update docs that still describe the embedded SDK (spike report §8 item 4) | AGENTS.md, architecture.md, KI entries | ✅ (CI workflow stays with KI-15) |
| PR `spike-upstream-sdk` → `main`; sandbox-account smoke test from an empty `DATA_DIR` | AGENTS.md rule 1 | PR #6 open; Docker image builds (linux/arm64); sandbox smoke test pending |

## Phase 0 — Make it safe to touch (≈ 1 week)

Goal: nothing the server does can damage the owner's data or leak access, and every
change is checked automatically.

| Item | Refs | Size |
|---|---|---|
| ~~Adopt upstream SDK per spike result, or port its fixes~~ (done, migration PR) | ADR-0001 | M |
| ~~Move workflows to `.github/workflows/`, drop Fly.io deploy and nested duplicate, Go version from `go.mod`, add `-race`~~ (done, `ci.yml`); add `staticcheck` and `govulncheck` once KI-33 and KI-32 are fixed | KI-15, ADR-0007 | S |
| Build and test on a supported Go release (Go 1.25 is out of support; 9 reachable stdlib vulnerabilities) | KI-32 | S |
| ~~Reject `repeat` on all surfaces; remove it from tool schemas~~ (done, PR #2) | KI-01, ADR-0004 | S |
| Remove debug endpoints (`delete-history`, `nuke-account`, `confirm-account`) from the server | KI-08 | S |
| Redact `Authorization`/passwords in debug dumps + test | KI-09 | S |
| Remove `things_smoke_test` from MCP; keep as sandbox-only CLI | KI-11 | S |
| Write preconditions: target exists, right type, not template, not trashed | KI-10, ADR-0004 | M |
| `ENABLE_WRITES` switch (write tools not registered when off; default off for new installs) | ADR-0004, `legal-and-risk.md` | S |
| Sandbox Things account + `.env.sandbox` convention; mark `tests/*.sh` sandbox-only | ADR-0007 | S |
| ~~Module rename to `github.com/pachamaker/things-cloud-mcp`~~ (done, migration PR); version via ldflags | ADR-0001, KI-16 | S |

**Exit:** CI green on `main`; no route can delete history/account; `repeat` rejected;
writes to unknown UUIDs fail with a clear error.

## Phase 1 — Correct reads (≈ 1–2 weeks)

Goal: `things_list_today` matches the Things app at any hour; the assistant needs no
workarounds.

| Item | Refs | Size |
|---|---|---|
| `THINGS_TIMEZONE`, `domain/civil` (Date, Clock), migrate all date logic | KI-03, ADR-0003 | M |
| Normalise year ≥ 4000 sentinels to null at ingestion | KI-04 | S |
| Persist `rr`, `rt`, `sb`, `dds`, `tir` in the mirror: upstream SDK PR with a `TaskReplayVersion` bump, or a `replace` soft fork (spike report §4 B4) | KI-01, KI-02, ADR-0001 | M |
| Move the classifier from `server/views.go` to `domain/view`; extend it once B4 fields exist (evening, `dds`, templates, projects in Today) | KI-02, KI-05, ADR-0005 | M |
| Resolve project membership through headings | KI-13 | S |
| Conformance harness + first snapshots (incl. post-midnight and "Mac closed for a day") | ADR-0005 | M |
| Freshness-bounded, coalesced sync on read; backoff on 429 | architecture §7, KI-27 | S |
| Rebuild mirror on history-key change / 404 AccountIssue; quarantine malformed items | KI-28, KI-29 | S |

**Exit:** conformance suite passes on ≥ 3 snapshots taken at different times of day;
the operator's Today/UTC/status/4001 workarounds are deleted from assistant
instructions.

## Phase 2 — Remote access from any device (≈ 1 week)

Goal: Claude (web, desktop, iOS) and ChatGPT reach the server on the VPS securely.

| Item | Refs | Size |
|---|---|---|
| ~~Upgrade to Go ≥ 1.25 (Dockerfile base image too)~~ (done with the SDK migration); `mcp-go` v1.x | ADR-0002 | S |
| Resource-server auth on `/mcp`: RFC 9728 metadata, 401 + `WWW-Authenticate`, JWT validation against Pocket ID, scopes `things.read`/`things.write` | KI-07, ADR-0002 | M |
| End-to-end OAuth test with Claude (web + iOS) and ChatGPT; fall back to built-in AS if Pocket ID fails | ADR-0002 | S–M |
| `DATA_DIR`, `/healthz` + `/readyz`, `log/slog` structured logs | KI-16 | S |
| Docker image (distroless/static, pure-Go SQLite, tzdata), GHCR release on tag | ADR-0002, ADR-0007 | S |
| VPS stack: Docker Compose + Caddy (auto-TLS), volume, nightly backup, uptime check | `deployment/vps.md` | S |
| Connect Claude custom connector and ChatGPT connector; document both | `deployment/vps.md` | S |

**Exit:** from an iPhone on mobile data, Claude and ChatGPT can list Today and create a
task; an unauthenticated request to `/mcp` gets 401 with `WWW-Authenticate` pointing
to resource metadata.

## Phase 3 — Tool surface v2 (≈ 2 weeks)

Goal: typical requests take 1–2 tool calls and never require UUID lookups.

| Item | Refs | Size |
|---|---|---|
| `Engine` struct; remove package globals | KI-17 | M |
| Enriched structured task output with names, view, evening, checklist progress | KI-14, ADR-0006 | M |
| Names-or-UUIDs for project/area/heading/tags; no tag auto-creation | KI-12 | S |
| `things_list(view=…)`, `things_get`, `things_search`, `things_catalog` | ADR-0006 | M |
| `things_batch` with one commit and per-op results | KI-06 | M |
| Correct annotations; MCP `instructions` field | KI-24 | S |
| Write audit log (`audit.jsonl`) | ADR-0004 | S |
| N+1 query fix; search limit | KI-18, KI-19 | S |

**Exit:** a morning review prompt completes in ≤ 2 calls; batch of 10 edits commits
once; the old tool names still work as deprecated aliases.

## Phase 4 — Careful feature growth (ongoing)

Each item requires a capture fixture and a sandbox round trip (ADR-0004).

- This Evening (`sb`), reminders (`ato`), reordering (`ix`, `ti`).
- Logbook views and weekly review helpers.
- Delete tags/areas via tombstones.
- Recurrence: read-side first (show "repeats weekly" on templates; virtual instances in
  Upcoming), then — only with a new ADR — create repeating to-dos.

## Out of scope

- Multi-user hosting or offering this as a service to others (see
  `legal-and-risk.md`).
- Replacing Things apps' local logic (instance generation, notifications).
