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
| PR `spike-upstream-sdk` → `main`; sandbox-account smoke test from an empty `DATA_DIR` | AGENTS.md rule 1 | ✅ PR #6 merged; Docker image builds (linux/arm64); sandbox run: see the KI-34 row in Phase 0 |

## Phase 0 — Make it safe to touch (≈ 1 week)

Goal: nothing the server does can damage the owner's data or leak access, and every
change is checked automatically.

| Item | Refs | Size |
|---|---|---|
| ~~Adopt upstream SDK per spike result, or port its fixes~~ (done, migration PR) | ADR-0001 | M |
| ~~Move workflows to `.github/workflows/`, drop Fly.io deploy and nested duplicate, Go version from `go.mod`, add `-race`~~ (done, `ci.yml`); ~~add `govulncheck`~~ (done with KI-32); ~~add `staticcheck` once KI-33 is fixed~~ (done, phase 0) | KI-15, ADR-0007 | S |
| ~~Build and test on a supported Go release (Go 1.25 is out of support; 9 reachable stdlib vulnerabilities)~~ (done: `toolchain go1.26.10`, `golang:1.26.10-alpine`, `govulncheck` job) | KI-32 | S |
| ~~Reject `repeat` on all surfaces; remove it from tool schemas~~ (done, PR #2) | KI-01, ADR-0004 | S |
| ~~Remove debug endpoints (`delete-history`, `nuke-account`, `confirm-account`) from the server~~ (done, phase 0: all of `/api/debug/*` and `DEBUG`) | KI-08 | S |
| ~~Redact `Authorization`/passwords in debug dumps + test~~ (done for the server: no SDK dumps, no debug mode; the SDK's own dump on a rejected commit needs an upstream PR) | KI-09 | S |
| ~~Remove `things_smoke_test` from MCP; keep as sandbox-only CLI~~ (done, phase 0; the sandbox e2e harness replaces it) | KI-11 | S |
| ~~Write preconditions: target exists, right type, not trashed~~ (done, phase 0); not template (needs `rr`/`rt` in the mirror) | KI-10, ADR-0004 | M |
| ~~`ENABLE_WRITES` switch (write tools not registered when off; default off for new installs)~~ (done, phase 0) | ADR-0004, `legal-and-risk.md` | S |
| ~~Sandbox Things account + `.env.sandbox` convention; mark `tests/*.sh` sandbox-only~~ (done: account and `things-sandbox` environment by the owner, `.env.sandbox` ignored, `THINGS_SANDBOX_ENV_FILE` for the harness; writing scripts check the marker area, phase 0) | ADR-0007 | S |
| Sandbox e2e harness: every MCP tool, account guard, pre-send write firewall, 409 test, wire check by an independent client, cleanup, seed list (`docs/testing/sandbox-e2e.md`); first run by the owner, then at most daily via `.github/workflows/e2e.yml` | KI-34, ADR-0007 | M — first run 2026-10-11: main suite green; seed checks and leftover cleanup hit 429 (KI-27); GET retry after 429 added in phase 0, next run pending |
| ~~Module rename to `github.com/pachamaker/things-cloud-mcp`~~ (done, migration PR); ~~version via ldflags~~ (done, phase 0) | ADR-0001, KI-16 | S |

**Exit:** CI green on `main`; no route can delete history/account; `repeat` rejected;
writes to unknown UUIDs fail with a clear error.

## Phase 1 — Correct reads (≈ 1–2 weeks)

Goal: `things_list_today` matches the Things app at any hour; the assistant needs no
workarounds.

| Item | Refs | Size |
|---|---|---|
| ~~`THINGS_TIMEZONE`, `domain/civil` (Date, Clock), migrate all date logic~~ (done on `KI-03-owner-timezone`, the sandbox harness follows the zone on `KI-34-harness-owner-timezone`; `today` in list output remains, ADR-0003 notes) | KI-03, ADR-0003 | M |
| ~~Normalise year ≥ 4000 sentinels to null at ingestion~~ (done where the server reads the mirror, `server/readmodel.go`) | KI-04 | S |
| ~~Persist `rr`, `rt`, `sb`, `dds`, `tir` in the mirror~~ (done through the SDK fork: `go.mod` replaces the SDK with the combined fork commit `4d63f48`, which carries [arthursoares/things-cloud-sdk#40](https://github.com/arthursoares/things-cloud-sdk/pull/40) (`rr`/`rt`, issue [#39](https://github.com/arthursoares/things-cloud-sdk/issues/39)) and [#41](https://github.com/arthursoares/things-cloud-sdk/pull/41) (`sb`/`dds`/`tir`), both awaiting review; drop the `replace` once an upstream release carries them. The server uses `rr`/`rt` with PR #25 and `sb`/`dds`/`tir` with the classifier row below) | KI-01, KI-02, ADR-0001 | M |
| Move the classifier from `server/views.go` to `domain/view`; extend it once B4 fields exist (evening, `dds`, templates, projects in Today) | KI-02, KI-05, ADR-0005 | M |
| ~~Resolve project membership through headings~~ (done for `things_list_project_tasks`, with `heading_id`/`heading_title`) | KI-13 | S |
| Conformance harness (built: `docs/testing/conformance.md`) + first snapshots (incl. post-midnight and "Mac closed for a day") | ADR-0005 | M |
| ~~Freshness-bounded, coalesced sync on read; backoff on 429~~ (done: `server/syncgate.go`, PR #4) | architecture §7, KI-27 | S |
| ~~Rebuild mirror on history-key change / 404 AccountIssue~~ (done: `server/mirror.go`, history checks in the sync gate); ~~quarantine malformed items~~ (done for tombstones: the fork commit carries [arthursoares#42](https://github.com/arthursoares/things-cloud-sdk/pull/42), the sync gate logs every skipped item); whole-page decode is KI-37 | KI-28, KI-29 | S |

**Exit:** conformance suite passes on ≥ 3 snapshots taken at different times of day;
the operator's Today/UTC/status/4001 workarounds are deleted from assistant
instructions.

## Phase 2 — Remote access from any device (≈ 1 week)

Goal: Claude (web, desktop, iOS) and ChatGPT reach the server on the VPS securely.

| Item | Refs | Size |
|---|---|---|
| ~~Upgrade to Go ≥ 1.25 (Dockerfile base image too)~~ (done with the SDK migration); `mcp-go` v1.x | ADR-0002 | S |
| ~~Resource-server auth on `/mcp`: RFC 9728 metadata, 401 + `WWW-Authenticate`, JWT validation against Pocket ID, scopes `things.read`/`things.write`~~ (done in code: `server/auth.go`; REST behind the same tokens, `API_KEY` removed) | KI-07, ADR-0002 | M |
| End-to-end OAuth test with Claude (web + iOS) and ChatGPT; fall back to built-in AS if Pocket ID fails | ADR-0002 | S–M |
| ~~`DATA_DIR`~~ (done, PR #4); `/healthz` + `/readyz`, `log/slog` structured logs | KI-16 | S |
| ~~Docker image (distroless/static, pure-Go SQLite, tzdata), GHCR release on tag~~ (done: `Dockerfile`, `release.yml`, image health check via `things-server healthcheck`; first tag pending) | ADR-0002, ADR-0007, KI-16 | S |
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
