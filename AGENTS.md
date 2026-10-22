# AGENTS.md

Operating contract for AI coding agents (Claude Code, Codex, others) and humans working
on this repository. Read this file fully before changing code. `CLAUDE.md` imports it.

## 1. What this project is

An **unofficial, single-user MCP server for Things 3**. It talks directly to the private
Things Cloud sync protocol (reverse-engineered, no official docs), mirrors the owner's
data into SQLite, and exposes MCP tools so Claude and ChatGPT can read and change tasks
from any device.

**North star:** the owner runs it on their own VPS and manages Things from iPhone, iPad,
Mac or web through Claude/ChatGPT — no Mac needs to be online, and the official Things
apps never break because of something this server wrote.

**Lineage:** `nicolai86/things-cloud-sdk` → `arthursoares/things-cloud-sdk` →
`mattydsmith/things-cloud-mcp` (archived) → this fork (`pachamaker/things-cloud-mcp`).
The protocol layer is the maintained `arthursoares/things-cloud-sdk`, used as a pinned Go
dependency; this repo owns the MCP server, its read model and its safety rules. See
`docs/adr/0001-fork-identity.md`.

## 2. Non-negotiable rules

These protect the owner's real task database. Violating one is worse than leaving a
feature unimplemented.

1. **Never write to the owner's real Things account from tests, scripts or
   experiments.** Use the sandbox account (`THINGS_SANDBOX_*` credentials, never
   committed). If you are unsure which account a command targets, stop and ask.
2. **No guessing on the wire.** Do not add or change any field or value the server
   *writes* to Things Cloud unless it is marked ✅ in
   `docs/protocol/things-cloud.md` or you add a capture fixture from the official
   client following §8 of that document. Things Cloud accepts anything; the Things
   apps crash on bad data, sometimes irreversibly.
3. **Recurrence writes are forbidden.** Do not send `rr` (not even `null`), do not
   modify repeating templates or instances, do not re-enable the `repeat` parameter.
   Incident 2026-08-18 (KI-01, ADR-0004).
4. **IDs must be safe Base58.** Never emit an identifier whose natural encoding drops a
   leading zero byte (KI-26): generate IDs only with `generateUUID` (`server/write.go`),
   never with the SDK's `NewUUID`. Never write `Task7` items until a validated encoder
   exists.
5. **Never weaken authentication.** No unauthenticated route may read or write task
   data on a publicly reachable deployment.
6. **Never log secrets or personal data:** Things password, `Authorization` headers,
   OAuth tokens, API keys, full task notes. Debug dumps must redact them.
7. **Calendar dates are civil dates in the owner's timezone.** Do not call
   `time.Now()` or `.UTC()` to decide "today": take `ownerToday()` (`server/today.go`)
   once per request and pass the `civil.Date` down; parse, compare and encode day
   values with `domain/civil` (ADR-0003). Pure date logic takes `today` or a
   `civil.Clock` as a parameter, never the wall clock.
8. **Do not copy or patch SDK code in this repo.** The SDK is the
   `github.com/arthursoares/things-cloud-sdk` dependency, pinned in `go.mod`; generic
   fixes go upstream as PRs, or through a `replace` soft fork (ADR-0001). Upgrades go
   through a PR with the full test suite. Until an upstream release carries the fork's
   fixes, `go.mod` has exactly one `replace`: the head of the fork branch
   `pachamaker/things-cloud-sdk` `fork-integration`. A new SDK fix is merged into that
   branch and the `replace` moves to its new head, never to a commit of another fork branch.
9. **Do not edit `.env` files, deployment secrets, or anything under `/migrations`**
   without an explicit instruction.

**Current top risks** (read before touching sync or writes): KI-01 (recurrence). KI-03
("today" is the UTC day) is fixed when `THINGS_TIMEZONE` is set; unset, the server keeps
the UTC day. KI-25 (`Task7`) is fixed by the upstream SDK; an unknown task kind now
stops the sync with an error instead of being skipped. KI-07 (no auth on `/mcp`) is
fixed in code: every data route requires an OAuth access token (`server/auth.go`), but
the end-to-end sign-in through Pocket ID from Claude and ChatGPT is not verified yet.

## 3. Before you start a task

1. Find the task's issue in `docs/known-issues.md` (`KI-NN`) or the ADR it belongs to.
   If neither exists and the change is non-trivial, write the KI entry or a Proposed
   ADR first and get it approved.
2. Read the files the KI points to, and the relevant section of
   `docs/architecture.md` and `docs/protocol/things-cloud.md`.
3. State the plan in 2–5 bullets: what changes, why, how you will test it.
4. If the task is ambiguous, ask **one** clarifying question.

## 4. Commands

```bash
go build -o /dev/null ./...          # build everything (plain `go build ./...` collides with the server/ directory)
go vet ./...                         # static checks
go test ./...                        # unit tests (no network)
go test -race ./...                  # includes the concurrent-sync gate tests
go test -run TestName ./server       # single test
go mod tidy -diff                    # go.mod/go.sum must be tidy; prints the needed change
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...   # vulnerabilities in reachable code, as in CI
go build -o things-server ./server   # build the server binary (version "dev")
go build -ldflags "-X main.version=v2.0.0" -o things-server ./server   # release build
docker build --build-arg VERSION=v2.0.0 -t things-cloud-mcp:v2.0.0 .     # image (host platform)
docker run --rm --network none things-cloud-mcp:v2.0.0   # offline smoke: exits with the auth configuration error
```

If the default Go proxy is unreachable (e.g. a corporate proxy), prefix commands with
`GOPROXY=https://proxy.golang.org,direct`.

`go.mod` keeps `go 1.25.0` as the minimum and pins `toolchain go1.26.9` (KI-32): a local
`go` older than that downloads and uses Go 1.26.9 (`GOTOOLCHAIN=auto`), a newer one
builds with itself. CI and the Docker builder (`golang:1.26.9-alpine`, pinned by digest)
use 1.26.9; bump the `toolchain` line and the `Dockerfile` tag and digest together.

Run the server locally **against the sandbox account only**:

```bash
export THINGS_USERNAME="$THINGS_SANDBOX_USERNAME" THINGS_PASSWORD="$THINGS_SANDBOX_PASSWORD"
export DATA_DIR="$PWD/.data"            # empty directory; the cache is $DATA_DIR/things-upstream.db
export ENABLE_WRITES=true               # writes are off by default
export INSECURE_NO_AUTH_LOCAL_ONLY=true # no OAuth; the server listens on 127.0.0.1 only
export THINGS_TIMEZONE=Europe/Moscow    # "today" is the UTC day if unset
go run ./server
```

Sandbox end-to-end tests (KI-34, `docs/testing/sandbox-e2e.md`): opt-in Go tests behind
the `e2e` build tag that start the server in-process on a temp `DATA_DIR` and call every
MCP tool. Credentials come only from `THINGS_SANDBOX_ENV_FILE` (a file outside the repo)
or `THINGS_SANDBOX_USERNAME`/`THINGS_SANDBOX_PASSWORD`; without them the tests skip. An
account guard (area `MCP E2E SANDBOX`, username ≠ `THINGS_USERNAME`) and a write firewall
(verified writes on the run's own objects only) gate every commit. At most one run a day:

```bash
go test -tags e2e -run XXX ./...                                              # compile only (no network)
go test -tags 'e2e fakecloud' -run '^TestSandbox' -count=1 ./server           # offline harness self-test
go test -tags e2e -run '^TestSandboxE2E$' -count=1 -v -timeout 30m ./server   # writes! sandbox only
go test -tags e2e -run '^TestSandboxSeedChecks$' -count=1 -v ./server         # read-only seed checks
go test -tags e2e -run '^TestSandboxCleanupLeftovers$' -count=1 -v ./server   # trash leftovers of killed runs
go test -tags e2e -run '^TestSandboxRecurrenceCapture$' -count=1 -v ./server  # read-only: sanitized capture of the repeating seeds
```

CI: `.github/workflows/e2e.yml` (manual and nightly, never on push or PRs) runs the same
tests in the GitHub environment `things-sandbox`, which the owner sets up once: required
reviewer, deployment branch `main` only, and the environment secrets, entered
interactively:

```bash
gh secret set THINGS_SANDBOX_USERNAME --env things-sandbox --repo pachamaker/things-cloud-mcp
gh secret set THINGS_SANDBOX_PASSWORD --env things-sandbox --repo pachamaker/things-cloud-mcp
```

First runs by hand via `workflow_dispatch`. CI logs are public: sandbox test output may
carry IDs and statuses, never e-mails, titles or notes read from the account
(`docs/testing/sandbox-e2e.md`, "CI").

Agents run the sandbox tests against an account only when the owner asks for that run:
the credentials are the owner's, and so is the day's run. The compile check and the
offline self-test are always fine.

Conformance snapshots (ADR-0005, KI-02, `docs/testing/conformance.md`): on a Mac whose
Things app is signed in to the sandbox, `scripts/conformance/snapshot.sh <label>` exports
the app's views from its database read-only with things.py (`expected.json`), dumps the
server's mirror of the same account (`input.json`, `TestSandboxConformanceDump`) and
compares them; the snapshot lands in `testdata/conformance/`. `TestConformance_Snapshots`
replays every committed snapshot in `go test ./...`. Taking a snapshot reads the owner's
Things database and the sandbox account: agents do it only when the owner asks. The
exporter's own tests need no Things and no network beyond installing things.py:

```bash
~/.venvs/things-conformance/bin/python -m unittest discover -s scripts/conformance -v
```

Black-box scripts in `tests/` call a running server and **create real items** in the
account it is connected to. The writing ones (`test-mcp.sh`, `test-smoke.sh`,
`test-api.sh`) first check for the sandbox marker area `MCP E2E SANDBOX`
(`tests/sandbox-guard.sh`) and refuse to write without it:

```bash
BASE_URL=http://localhost:8080 ./tests/test-mcp-protocol.sh   # read-only protocol checks
BASE_URL=http://localhost:8080 ./tests/test-mcp.sh 001        # writes! sandbox only
```

Configuration (environment variables):

| Variable | Required | Meaning |
|---|---|---|
| `THINGS_USERNAME` / `THINGS_PASSWORD` | yes | Things Cloud account the server syncs |
| `OAUTH_ISSUER` | yes¹ | Authorization server (Pocket ID) URL, `https`, exactly the `iss` of its tokens. Metadata and signing keys are discovered from it |
| `MCP_PUBLIC_URL` | yes¹ | Public URL of the MCP endpoint, `https://…/mcp`: the RFC 9728 `resource` and the audience every token must carry |
| `OAUTH_ALLOWED_SUBJECT` | yes¹ | The owner's user id (`sub`) at the issuer; tokens of any other user are rejected |
| `OAUTH_ALLOWED_CLIENTS` | no | Comma-separated `client_id` allowlist; unset accepts any client the issuer grants this API |
| `INSECURE_NO_AUTH_LOCAL_ONLY` | yes¹ | `true` serves without authentication, listens on `127.0.0.1` only and rejects requests whose `Host` or `Origin` is not loopback (local development, sandbox scripts). Cannot be combined with the `OAUTH_*` settings |
| `PORT` | no | Listen port, default `8080` |
| `ENABLE_WRITES` | no | `true` registers the write tools and REST write endpoints. Unset or `false`: read-only, the write tools are not even listed (ADR-0004). Any other value stops the server at startup |
| `ENABLE_REST_API` | no | `true` serves `/api/*` behind the same tokens as `/mcp`; off by default (ADR-0002) |
| `DATA_DIR` | no | Directory of the SQLite cache `things-upstream.db`, default `/data` (created with mode 0700). The cache is disposable: delete the file to force a full resync. A rebuild after an account reset leaves the old mirror as `things-upstream.db.before-rebuild` (KI-28) |
| `THINGS_TIMEZONE` | no (set it) | Owner's IANA zone, e.g. `Europe/Moscow`: decides "today" for views, `when`, deadlines and `completed_*` dates (ADR-0003). Unset: the UTC day, with a startup warning. An unknown name or `Local` stops the server at startup. Zone data is built in |

¹ Either the three OAuth settings or `INSECURE_NO_AUTH_LOCAL_ONLY=true`; without either the
server refuses to start. Tokens need the scope `things.read` for any request and
`things.write` for write tools and REST write endpoints. `API_KEY` is gone: the server
refuses to start while it is set (ADR-0002).

Baseline: CI (`.github/workflows/ci.yml`) runs `go mod tidy -diff`, build, vet,
`go test -race ./...` and `govulncheck` on Go 1.26.9 for every pull request and push to
`main`; all pass. `staticcheck -tags 'e2e fakecloud'` is gated too (KI-33). Do not add
failures.

Releases: pushing a `v*` tag runs `.github/workflows/release.yml`, which repeats the CI
checks and pushes a linux/amd64 + linux/arm64 image to `ghcr.io/pachamaker/things-cloud-mcp`
(`docs/deployment/vps.md`, KI-16). Tags and releases are the owner's: agents never create
or push tags and never publish images.

## 5. Repository map

| Path | What | Status |
|---|---|---|
| `server/main.go` | Process wiring, router, REST handlers, `DATA_DIR` | live |
| `server/auth.go` | OAuth resource server: config, JWKS cache, RFC 9068 token checks, RFC 9728 metadata, scopes per tool (KI-07) | live |
| `server/mcp.go` | MCP tool registry, read handlers, output formatting | live |
| `server/write.go` | Write commands, wire payload builders, ID generator, `historyWrite` | live |
| `server/readmodel.go` | Read model over the SDK `State`: pagination, open-list filtering, completed range | live |
| `server/views.go` | View classifier (Inbox/Today/Anytime/Someday/Upcoming, This Evening), ADR-0005 | live |
| `server/today.go` | `THINGS_TIMEZONE`, `ownerToday()` (ADR-0003) | live |
| `domain/civil` | Civil date, clock, wire encoding of day values, zone loading (ADR-0003) | live |
| `server/syncgate.go` | Serialises and coalesces syncs, 429 backoff, history checks and background mirror rebuilds (KI-28) | live |
| `server/mirror.go` | Mirror file and Syncer, history check against the account, rebuild with one backup (KI-28) | live |
| `server/sandbox_*_test.go` | Sandbox e2e harness (tag `e2e`) and its pure rules (untagged, unit-tested) | sandbox only |
| `testdata/captures/` | Sanitized payloads the official Things apps wrote (protocol §8 evidence), checked by `server/recurrence_capture_test.go` | evidence |
| `server/conformance_*test.go` | Conformance suite over `testdata/conformance/` (untagged) and the snapshot dump (tag `e2e`), ADR-0005 | live |
| `scripts/conformance/` | Dev-only exporter of the Things app's views (things.py) and the snapshot script | dev only |
| `testdata/conformance/` | Snapshots: the Things app's views and the server's mirror at one moment, titles hashed | evidence |
| `tests/` | Shell e2e scripts against a running server (superseded by the Go harness) | sandbox only |
| `.github/workflows/ci.yml` | PR and `main` checks: tidy, build, vet, race tests, `govulncheck`; no secrets, no network to Things | live |
| `.github/workflows/release.yml` | On `v*` tags: the CI checks, then the multi-arch image to GHCR; only job with `packages: write` | live |
| `Dockerfile` | Static build on distroless `nonroot`, base images pinned by digest, `HEALTHCHECK` via `things-server healthcheck` (`server/healthcheck.go`) | live |
| `docs/` | See `docs/README.md` for the index | |

The module is `github.com/pachamaker/things-cloud-mcp`. The SDK packages are imported
as `github.com/arthursoares/things-cloud-sdk` (`things`/`thingscloud`) and
`github.com/arthursoares/things-cloud-sdk/sync`. What the SDK version differs in from
the old embedded copy: `docs/2026-10-10-upstream-sdk-spike.md` §3–§4. Until
[arthursoares/things-cloud-sdk#40](https://github.com/arthursoares/things-cloud-sdk/pull/40)
and [#41](https://github.com/arthursoares/things-cloud-sdk/pull/41) are released, `go.mod`
replaces the SDK with the fork `github.com/pachamaker/things-cloud-sdk` at `9d70eb8`, which
stores `rr`/`rt` (`Task.Repeater`, `RecurrenceIDs`) and `sb`/`dds`/`tir` (`StartBucket`,
`DeadlineSuppressionDate`, `TodayIndexReferenceDate`); point it back at an upstream release,
or keep it as a deliberate soft fork (ADR-0001), before merging.

## 6. Architecture in one minute

- **Read path:** tool call → sync gate (`server/syncgate.go`: one sync at a time, reuse
  of a sync younger than 2 s, backoff on 429) → SDK `Syncer.Sync()` (incremental pull)
  → SDK `State.AllTasks` → classifier in `server/views.go` → JSON. Views (Today,
  Upcoming, …) are *derived locally*; Things Cloud has no view endpoints, and the SDK's
  own `TasksIn*` queries are not used.
- **Write path:** tool call → input validation → Things wire payload in
  `server/write.go` → `history.Write()` commit with `ancestor-index` (retry once on
  409) → post-write sync.
- **Source of truth** is the Things Cloud history; SQLite (`$DATA_DIR/things-upstream.db`)
  is a disposable cache. The SDK replays it from scratch after reader fixes, keeping a
  backup next to it. The mirror belongs to one history: when the account's history key
  changes (account reset) or the stored cursor is ahead of the head, the server sets it
  aside as `things-upstream.db.before-rebuild` and rebuilds it (`server/mirror.go`, KI-28).
- **Concurrency:** the sync gate serialises syncs (the SDK `Syncer` has no lock);
  `historyMu` serialises writes. Package globals (`client`, `mirrorDB`, `mirror`,
  `history`) are a known smell (KI-17); read the mirror through `mirrorState()`, never
  keep a Syncer, since a rebuild replaces it.

Details and target architecture: `docs/architecture.md`.

## 7. Things domain cheat sheet

| Concept | Wire | Notes |
|---|---|---|
| To-do / Project / Heading | `Task6`/`Task7` with `tp` = 0 / 1 / 2 | the server reads both and writes `Task6` |
| Status | `ss`: 0 open, 2 canceled, 3 completed | `sp` = stop instant, never `0` |
| Start state | `st`: 0 Inbox, 1 started, 2 deferred | |
| Start date ("when") | `sr` (+ `tir` when placed in Today) | civil date as midnight UTC (`civil.Date.Wire`) |
| This Evening | `sb = 1` | read: `evening: true` in `things_list_today`; not written |
| Deadline | `dd`; suppressed overdue: `dds` | year 4001 = "never" (KI-04) |
| Project / subtask parent | `pr` | heading membership in `agr` (KI-13) |
| Area / Tags | `ar` / `tg` | arrays of UUIDs |
| Recurrence | `rr` on hidden template, `rt` on instances | read-only, ⛔ write |
| IDs | 21–22 char Base58 (generated: 22) | non-canonical IDs crash Things (KI-26) |

Verified placement writes: Inbox `st0,sr∅,tir∅` · Today `st1,sr=today,tir=today` ·
Anytime `st1,∅,∅` · Someday `st2,∅,∅` · Upcoming `st2,sr=D>today,tir∅`.
Never write `st=2` with today's date.

## 8. Write capability status

All write tools exist only when the server runs with `ENABLE_WRITES=true` (ADR-0004),
and only a token with the `things.write` scope sees and calls them (KI-07).
Every write first checks its target and references against a freshly synced mirror
(`server/preconditions.go`, KI-10) and sends nothing when a check fails. A repeating
template or instance is never changed: every write to one, or to its checklist, is
refused (KI-01), and no list shows templates (KI-05). This needs the SDK to store
`rr`/`rt` (see §5).

| Capability | Tool(s) | Status |
|---|---|---|
| Create to-do (title, notes, when, deadline, project, parent, tags) | `things_create_task` | ✅ verified |
| Edit title / notes / when / deadline / project / area / tags | `things_edit_task` | ✅ verified; to-dos only, target and references checked first (KI-10) |
| Complete / reopen / trash / untrash to-do | `things_*_task` | ✅ verified |
| Move to Today / Anytime / Someday / Inbox | `things_move_to_*` | ✅ verified (Today is the owner's day, `THINGS_TIMEZONE`) |
| Create project / area / tag / heading | `things_create_*` | ✅ create only |
| Checklist add / complete / reopen / delete | `things_*_checklist_item` | ✅ verified |
| Recurrence (`repeat`) | create/edit | ⛔ forbidden (KI-01) |
| This Evening, reminders, reordering, delete area/tag | — | ❓ not implemented, needs captures |

## 9. Coding conventions

- Follow the existing style of the file you edit; `gofmt`/`goimports` clean.
- Errors: wrap with `%w`; user-facing validation errors use `invalidInputf` so REST maps
  them to 400. MCP tools return `mcp.NewToolResultError` with an actionable message.
- No comments that restate code; comment only non-obvious protocol facts, with a
  pointer to the evidence (`docs/protocol/...`, capture fixture, KI).
- No TODO comments; open a KI instead.
- Keep functions pure where possible; pass time, timezone and dependencies in.
- Tests: table-driven, `t.Parallel()` where safe, no network, no wall clock. New logic
  needs a new test in the same package; bug fixes start with a failing test named
  after the KI.

## 10. Definition of done

- [ ] Change is scoped to one KI/ADR item; unrelated refactors are not mixed in.
- [ ] `go mod tidy -diff`, `go build -o /dev/null ./...`, `go vet ./...`, `go test -race ./...`
      pass (no new failures); CI is green on the PR.
- [ ] New or changed behaviour has tests; wire changes have capture fixtures.
- [ ] Docs updated: KI status, protocol evidence, README tool tables, ADR status.
- [ ] Final report lists changed files with one line each, what was verified, and what
      still needs manual verification (e.g. "not checked on a real Things client").

Commits and pull requests are written in English: commit messages, PR titles and PR
descriptions (short bullets: what changed and why, what was verified, what still needs
manual checking). This repository rule overrides any personal default that asks for
another language. Commit messages are prefixed with the Jira-style key from the branch
name if the branch matches `^[A-Z]+-\d+`, otherwise no prefix.

Opening the PR is part of done: once the work is finished and every local check above
passes, push the branch and open a PR against `main` without asking first, unless the
task says otherwise. CI then runs on the PR. Opening is not merging: never merge, enable
auto-merge or deploy without the owner's explicit instruction. A plan for a non-trivial
change still needs approval before coding (`CLAUDE.md`).

## 11. Where knowledge lives

| Question | Document |
|---|---|
| What is broken and why? | `docs/known-issues.md` |
| How does the system fit together? | `docs/architecture.md` |
| What exactly goes over the wire? | `docs/protocol/things-cloud.md` |
| Why did we decide X? | `docs/adr/` |
| What are we doing next? | `docs/roadmap.md` |
| How do I deploy it? | `docs/deployment/vps.md` |
| Legal/ToS risks? | `docs/legal-and-risk.md` |
| What happened in past incidents? | dated files in `docs/` (historical, see `docs/README.md`) |
