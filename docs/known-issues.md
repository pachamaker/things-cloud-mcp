# Known Issues Register

Single source of truth for open defects, their root causes, and the fix direction.
Every issue has a stable ID (`KI-NN`). Reference it in branch names, commits, PRs
and tests (e.g. `TestTasksInToday_KI02_StaleStartDate`).

Status values: `open` · `in-progress` · `fixed (commit)` · `wontfix`.
Evidence values: `confirmed` (reproduced or visible in code) · `likely` (strong
signal, needs verification against a real Things client) · `hypothesis`.

Last full review: 2026-10-10 (baseline commit `3164e8a`). Updated 2026-10-11 for the
upstream SDK migration (ADR-0001): file references now point to `server/` or to the
`arthursoares/things-cloud-sdk` v0.6.0 dependency ("SDK `file.go`").

## How issues map to the operator workarounds

The maintainer's assistant instructions currently carry workarounds for this server.
Each one is a symptom of an issue below. When an issue is fixed, the matching
workaround can be deleted.

| Workaround the assistant follows today | Root issue |
|---|---|
| Never pass `repeat` — it corrupts Things on macOS/iOS (incident 2026-08-18) | KI-01 |
| `things_list_today` is incomplete; always also call `things_list_upcoming` | KI-25 (fixed by the SDK migration), KI-02, KI-03 |
| Between 00:00 and 03:00 MSK the server's "today" is still yesterday | KI-03 (fixed; delete once the deployment sets `THINGS_TIMEZONE=Europe/Moscow`) |
| `deadline = 4001-01-01` means "no deadline" | KI-04 (fixed; delete once the deployment runs this version) |
| Don't trust `status` in lists; filter `status == "open"` yourself | KI-05 |
| Create → list → edit one by one; batch update fails on the first unsynced ID | KI-06 |
| Use only existing tags | KI-12 |

## Recently merged changes

| PR | Merged | Effect on this register |
|---|---|---|
| [#1](https://github.com/pachamaker/things-cloud-mcp/pull/1) Today, Someday, Upcoming per Things rules | 2026-10-10 (`f79b05e`) | KI-02 core rules (things.py three-set Today, incl. `st=2` with start date ≤ today), KI-04 (sentinel hidden in output), KI-05 (canceled excluded). Still UTC (KI-03); no `dds`, evening, projects in Today, template hiding |
| [#2](https://github.com/pachamaker/things-cloud-mcp/pull/2) Reject every `repeat` value | 2026-10-10 (`7aa1009`) | KI-01 mitigated on all write surfaces, incl. `repeat: "none"`; details in [`known-issues/repeat-disabled.md`](known-issues/repeat-disabled.md) |
| [#3](https://github.com/pachamaker/things-cloud-mcp/pull/3) 22-character Base58 IDs | 2026-10-10 (`c622fdb`) | KI-26 fixed on `main` |
| [#4](https://github.com/pachamaker/things-cloud-mcp/pull/4) Safety adapters over upstream SDK | 2026-10-10, into `spike-upstream-sdk`; on `main` with #6 | Upstream migration (ADR-0001 option B): sync gate + 429 backoff (KI-27), ID redraw (KI-26), no HTTP dumps from `DEBUG` (KI-09), cache under `DATA_DIR` (KI-16) |
| [#5](https://github.com/pachamaker/things-cloud-mcp/pull/5) Agent-facing documentation | 2026-10-10 (`8a0a7fc`) | This register, ADRs, `AGENTS.md`, architecture, protocol, roadmap |
| [#6](https://github.com/pachamaker/things-cloud-mcp/pull/6) Upstream SDK migration | 2026-10-10 (`15e5b6b`) | KI-25 fixed (SDK reads `Task7`), KI-02/KI-05 rules ported to `server/views.go`, KI-21 fixed for syncs, KI-23 resolved (CLI tools removed); `today_index_ref` no longer in tool output. Spike report: `docs/2026-10-10-upstream-sdk-spike.md` |
| [#7](https://github.com/pachamaker/things-cloud-mcp/pull/7) CI on GitHub Actions | 2026-10-10, into `spike-upstream-sdk`; on `main` with #8 | KI-15 fixed |
| [#8](https://github.com/pachamaker/things-cloud-mcp/pull/8) Go 1.26.9 toolchain, `govulncheck` in CI | 2026-10-10 (`fe8c3e2`) | KI-32 fixed; also brings #7 (KI-15) to `main` |
| [#9](https://github.com/pachamaker/things-cloud-mcp/pull/9) Sandbox e2e harness and workflow | 2026-10-10 (`c1cca5e`) | KI-34 harness and `e2e.yml` |
| [#10](https://github.com/pachamaker/things-cloud-mcp/pull/10) Hide the history key in transport errors | 2026-10-10 (`4eb6c4a`) | KI-35 fixed |

---

## P0 — Data safety and security

### KI-25 · `Task7` items are silently dropped (Things ≥ 3.23)
- **Status:** fixed by the upstream SDK migration (ADR-0001): the SDK reads `Task7` like
  `Task6`, rejects a batch with any other unknown `Task*` kind before touching the cache
  (the sync fails with `incomplete sync: unsupported task kind …`), and replays an old
  cache after the reader fix. `Settings5` and `Command*` still pass as unknown changes.
  The text below describes the embedded SDK · **Evidence:** confirmed (no `Task7` in the embedded SDK; upstream
  [arthursoares/things-cloud-sdk#29](https://github.com/arthursoares/things-cloud-sdk/issues/29),
  [wbopan/things-cloud-mcp#23](https://github.com/wbopan/things-cloud-mcp/issues/23))
- **Symptom:** tasks created or edited in current Things apps never appear in the
  mirror: Today/Inbox look incomplete, "sync lag" that never resolves, edits through
  the server fail or act on stale state, duplicates get created.
- **Root cause:** since Things 3.23 (released 19–21 Aug 2026 with the repeating to-dos
  overhaul) the apps write task events as entity kind `Task7`. `sync/process.go:63-80`
  only knows `Task6` and older; any other kind becomes an `UnknownChange` and the
  cursor advances past it, so the data is lost from the mirror for good (until a full
  resync with a fixed reader). A fresh account synced from a current iPhone was
  reported to contain only `Task7` items.
- **Also unhandled:** `Settings5`, `Command`/`Command3` (Mail to Things queue),
  legacy `Task2`/`Tag2`.
- **Fix direction:** read `Task7` (upstream treats its payload as Task6-compatible for
  reads), bump the schema to force a full resync, fail loudly (stop advancing the
  cursor) on truly unknown kinds, and explicitly skip known non-task kinds. Writing
  `Task7` stays out of scope until ADR-0004 evidence exists; upstream's
  [task7-write-policy.md](https://github.com/arthursoares/things-cloud-sdk/blob/main/docs/task7-write-policy.md)
  is the reference. Preferred route: adopt the upstream SDK (ADR-0001).

### KI-26 · ~1 in 256 created IDs is not decodable by Things
- **Status:** fixed: PR #3 on `main`; since the upstream SDK migration by the redraw
  adapter `newIDFromReader` in `server/write.go` (PR #4, byte 0 ≠ 0 and 22 characters),
  which replaced PR #3's `base58.go`; the SDK's `History.Write` also refuses
  non-canonical IDs · **Evidence:** confirmed in code (pre-fix `server/write.go:187-202`); failure
  mode documented upstream (arthursoares README "Base58 encoder bug corrupted about 1 in
  256 creates", [wbopan/things-cloud-mcp#30](https://github.com/wbopan/things-cloud-mcp/issues/30))
- **Symptom:** a create poisons the account history; every Things app crashes while
  decoding that item, on every launch.
- **Root cause:** `generateUUID` encodes the 16 random bytes as a big integer and drops
  leading zero bytes, so when the first byte is `0x00` the identifier decodes to fewer
  than 16 bytes. Padding with `1` (standard Base58) was also reported to crash the app
  ([ZhehanZhang fork](https://github.com/ZhehanZhang/things-cloud-mcp/commit/fad02af61c8006a0874b9207258ad17a5e982a94)).
- **Fix direction:** redraw until the natural encoding represents all 16 bytes (first
  byte ≠ 0; upstream/ZhehanZhang go further and redraw until exactly 22 characters);
  port upstream `base58.go` with its fuzz tests. Tighten `isBase58UUID` to the same rule.

### KI-01 · Recurrence (`repeat`) writes can corrupt Things clients
- **Status:** mitigated — every `repeat` value, incl. `none`, is rejected (PR #2,
  `7aa1009`); storing `rr`/`rt` and refusing writes to templates/instances still open ·
  **Evidence:** confirmed (incident 2026-08-18, earlier crash 2026-02-16)
- **Symptom:** after creating/editing a task with `repeat`, Things on macOS and iOS
  misbehaves irreversibly.
- **Root cause:** `buildRepeatRule` (`server/write.go:360`) hand-assembles the `rr`
  object from guesses. Nothing in the repo is backed by a capture of the official
  client creating a repeating to-do. Known gaps:
  - Things models repetition as a hidden *template* (`rr` set) plus client-generated
    *instances* (`rt: [template]`). The server writes `rr` onto an ordinary visible
    task, and `editTask` (`server/write.go:746`) can bolt `rr` onto an existing task
    that already has instances, history, and device metadata.
  - `icsd` (instance creation start date), `icc`, `rt`, `ato` are never set
    consistently with `rr`.
  - Detail encoding is unverified: weekly `wd` uses Go's `time.Weekday` (Sunday = 0),
    monthly/yearly `dy` is written 0-based (`server/write.go:451-454`).
  - The mirror never stores `rr` or `rt` (SDK `sync/store.go`; spike report §4 B4), so the server
    cannot even tell a template from an instance after the fact.
  - Things 3.23 (Aug 2026) overhauled repeating to-dos (early completion, exceptions,
    rule updates) and moved to `Task7` (KI-25), so the encoder targets an obsolete model.
  - Elsewhere, completing a recurring task through the cloud protocol destroyed it
    ([wbopan#5](https://github.com/wbopan/things-cloud-mcp/issues/5)); the official URL
    scheme also refuses to change when/deadline/completion of repeating to-dos.
- **Fix direction:** ADR-0004. Disable `repeat` on all surfaces (reject with a clear
  error, including `repeat: "none"`) until a captured, sanitized payload from the
  official client exists as a fixture and a sandbox-account round trip passes on
  Mac + iOS. Persist `rr`/`rt` read-only first; refuse complete/move/schedule/edit on
  both templates and instances, as upstream does.

### KI-07 · `/mcp` has no authentication
- **Status:** fixed in code (`server/auth.go`) — not yet verified end to end with Pocket ID,
  Claude and ChatGPT (roadmap Phase 2) · **Evidence:** confirmed (`server/main.go:559-560`
  before the fix)
- **Symptom:** anyone who learns the URL can read and modify the whole task database.
- **Root cause:** OAuth was never implemented; the original Fly.io setup relied on an
  obscure hostname.
- **Fix:** ADR-0002 option B. The server is an OAuth 2.1 resource server: RFC 9728
  metadata at `/.well-known/oauth-protected-resource/mcp`, `401` with
  `WWW-Authenticate: Bearer resource_metadata=…` on `/mcp` and `/api/*`, RFC 9068 JWT
  checks against the issuer's JWKS (`typ at+jwt`, `iss`, `aud` = `MCP_PUBLIC_URL`,
  `exp`/`nbf`, `sub` = `OAUTH_ALLOWED_SUBJECT`, optional `client_id` allowlist). Scopes:
  `things.read` for any request, `things.write` for write tools and REST writes;
  `ENABLE_WRITES` stays the server-wide ceiling. Without OAuth settings the server starts
  only with `INSECURE_NO_AUTH_LOCAL_ONLY=true` and then listens on `127.0.0.1` and refuses
  non-loopback `Host`/`Origin` (DNS rebinding). The REST API is off by default and
  `API_KEY` is gone. Tests: `server/auth_test.go`, `server/auth_mcp_test.go` (local test
  issuer, no network).

### KI-08 · Destructive debug endpoints ship in the production binary
- **Status:** fixed (phase 0) — every `/api/debug/*` route and the `DEBUG` variable are
  gone from the server; there is no account-surgery tool, the sandbox e2e cleanup covers
  test data · **Evidence:** confirmed (`server/main.go:463-557` before the fix)
- **Symptom:** with `DEBUG=true` and an `API_KEY`, one POST deletes the Things Cloud
  history (`/api/debug/delete-history`) or the whole Things account
  (`/api/debug/nuke-account`).
- **Fix direction:** remove from the server; keep account surgery in a separate CLI
  that requires interactive confirmation and a sandbox account.

### KI-09 · `DEBUG=true` logs the Things password
- **Status:** fixed for the server — it never sets the SDK's `Client.Debug` (PR #4) and
  has no debug mode since KI-08; `TestNewThingsClient_KI09_DebugEnvDoesNotDumpPassword`
  guards it · **Evidence:** confirmed (SDK `client.go:135-146`
  dumps full requests incl. `Authorization: Password <plaintext>` when `Debug` is set)
- **Remaining:** the SDK logs a full response dump (no request headers) when a commit is
  rejected (`histories.go:281-282`).
- **Fix direction:** upstream PR: redact `Authorization` and `password` fields in dumps,
  plus an injectable `http.Client`; a test that the secret never appears in output.

### KI-35 · Tool errors can echo the history key
- **Status:** fixed (PR #10, `4eb6c4a`) — tool results,
  REST error bodies and log lines now read `write failed: Post
  "https://cloud.culturedcode.com/<redacted>": …` · **Evidence:** confirmed (sandbox
  harness offline self-test, 2026-10-11: a refused commit surfaced as the tool error below)
- **Symptom:** when a Things Cloud request fails below HTTP (timeout, DNS, refused
  connection), the tool result reads `write failed: Post
  "https://cloud.culturedcode.com/version/1/history/<key>/commit?…": …`, and reads give
  `pre-read sync failed: Get "…/history/<key>/items…"`. The same text goes to the log.
  History endpoints take no `Authorization` header (`docs/protocol/things-cloud.md` §1),
  so the key is a bearer capability for the whole task database, and the error lands in
  the LLM client's transcript.
- **Root cause:** the SDK's `http.Client` returns a `*url.Error` carrying the full URL;
  `historyWrite` and `syncForRead` (`server/write.go`) wrap it with `%w`, and the MCP and
  REST handlers return `err.Error()`.
- **Fix:** `redactRequestURL` (`server/redact.go`) replaces the path
  and query of a `*url.Error` with `<redacted>`, keeping `Op`, the host and the cause,
  where SDK errors enter the server: the sync gate (all reads, `/api/sync`, the initial
  sync), `historyWrite` (pull, commit, re-sync after a 409) and the history pull in
  `connectThings`. `TestHistoryWrite_KI35_ErrorHidesHistoryKey` and its siblings in
  `server/redact_test.go` drive the MCP and REST handlers through a refusing
  `http.DefaultTransport` and check the tool result, the REST body and the log. The
  sandbox harness keeps its own redaction as a second layer.
- **Remaining:** errors of `client.Verify`/`OwnHistory`
  outside the sync gate (`/api/verify`, `failed to get history` at startup) can carry the
  account e-mail address, not the key.

### KI-10 · Writes are accepted for UUIDs that do not exist (or have the wrong type)
- **Status:** fixed except templates (phase 0) — every write syncs and resolves its
  target and references in the mirror first (`server/preconditions.go`): the target
  exists and is a to-do (trash/untrash also accept projects and headings, since `tr`
  is verified for every type); trashed to-dos are refused except by untrash; complete
  needs an open to-do, uncomplete a completed or canceled one; project, parent to-do,
  area, tags, heading project and checklist items must exist (a trashed project or
  parent is refused). A refused write sends nothing and returns an invalid-input error
  (REST 400). Side effect: `project: "none"` used to write `pr=["none"]` and is now
  refused · **Evidence:** confirmed (before the fix `server/write.go:650-852` validated
  only the Base58 shape)
- **Remaining:** repeating templates and instances cannot be recognised: the SDK
  mirror does not store `rr`/`rt` (spike report §4 B4; upstream fix proposed in
  [arthursoares/things-cloud-sdk#40](https://github.com/arthursoares/things-cloud-sdk/pull/40)). Checklist writes find the item
  by scanning every to-do's checklist (the SDK has no lookup by item UUID).
- **Symptom:** a hallucinated or mistyped UUID produces a modify-item for an unknown
  object in the cloud history. Clients receive a partial entity with no creation
  payload. `things_edit_task` can also modify a project, heading, or repeating
  template as if it were a plain task.
- **Fix direction:** ADR-0004 preconditions — every write first resolves the target in
  the mirror (exists, not deleted, expected `type`, not a template) and fails fast
  otherwise.

### KI-11 · `things_smoke_test` is exposed to the LLM
- **Status:** fixed (phase 0) — the tool is gone; the sandbox e2e harness (KI-34) is the
  write check, run only against the sandbox account · **Evidence:** confirmed (`server/mcp.go:1333` before the fix)
- **Symptom:** the model can run a create/edit/complete/trash cycle against the real
  account at any time; leaves trashed `[smoke-test]` tasks behind.
- **Fix direction:** move to an admin CLI / CI job against the sandbox account.

### KI-32 · The server is built with Go 1.25, which no longer gets security fixes
- **Status:** fixed (PR #8, `fe8c3e2`): `go.mod` keeps `go 1.25.0` as
  the minimum and adds `toolchain go1.26.9`, which `setup-go` installs in CI and a local
  `go` older than 1.26.9 switches to (`GOTOOLCHAIN=auto`); the `Dockerfile` builder is
  `golang:1.26.9-alpine`; the CI job `govulncheck` runs govulncheck v1.8.0 (which itself
  needs Go ≥ 1.26) and fails on reachable vulnerabilities (checked: exit 3 on go1.25.14). GO-2026-4514 stays: `buger/jsonparser` v1.1.2 is not a one-line fix
  but bundles parser rewrites (integer parsing, key lookup, `null` handling) that
  `mcp-go` v0.44.0 is not tested with, `Delete` is not reachable, and `mcp-go` v1.x
  (ADR-0002) no longer depends on `invopop/jsonschema` or `jsonparser` ·
  **Evidence:** confirmed (`govulncheck` v1.8.0, 2026-10-11)
- **Symptom:** with the newest Go 1.25 patch (1.25.14) `govulncheck ./...` reports 9
  standard-library vulnerabilities the server's code reaches (`net/http` HTTP/1 and
  HTTP/2 server and client, `net/textproto`, `crypto/tls`; GO-2026-6603 … GO-2026-6617),
  fixed only in Go 1.26.9. With Go 1.26.9 the scan reports none; the only remaining
  entry is GO-2026-4514 in `github.com/buger/jsonparser` v1.1.1 (indirect via
  `mcp-go` → `invopop/jsonschema`), imported but not called.
- **Root cause:** Go supports the two newest major releases (1.27 and 1.26 since
  August 2026). `Dockerfile` builds with `golang:1.25-alpine`; `go.mod` says `go 1.25.0`
  without a `toolchain` line, so CI (`setup-go` with `go-version-file: go.mod`) tests
  on Go 1.25.0.
- **Fix direction:** build and test with a supported release: a `toolchain go1.26.x`
  line in `go.mod` (`setup-go` reads it) and the matching `Dockerfile` base image; keep
  `go 1.25.0` as the minimum while the SDK allows it. Then add the `govulncheck` CI job
  (ADR-0007).

---

## P1 — Read correctness (the "Today is wrong" family)

### KI-02 · View classification does not match Things
- **Status:** in-progress — PR #1 (`f79b05e`) implements the things.py rules in SQL on
  `main`; the classifier compares civil dates in the owner's timezone (KI-03);
  remaining: `dds`, This Evening, projects in Today, template hiding, conformance harness. Since the upstream SDK migration the rules live
  in `server/views.go` over SDK `State.AllTasks` (SDK `TasksIn*` are not used, spike
  report §4 B3); `tir` is not mirrored, so a to-do placed in Today only through `tir` is
  listed in Anytime, and a deferred one with a future `tir` and no start date in Someday ·
  **Evidence:** confirmed in code; exact Things rules `likely` (to be verified with the
  conformance harness, ADR-0005)
- The table below describes the baseline `3164e8a`, before PR #1.
- **Symptom:** `things_list_today` misses tasks the Things app shows in Today; they show
  up in Upcoming, Someday, or Anytime instead.
- **Root cause:** `sync/state.go:163-257` implements a narrower definition than Things:

  | Case | Things app (as modelled by things.py over the local DB) | This server today |
  |---|---|---|
  | `st=1`, start date = today | Today | Today |
  | `st=1`, start date in the past (put in Today yesterday, not done) | Today | **Anytime** |
  | `st=2`, start date ≤ today (scheduled date arrived, not yet "confirmed" by a client) | Today | **Someday** (after 00:00 UTC) or **Upcoming** (before) |
  | no start date, deadline ≤ today, deadline not suppressed (`dds` null) | Today | Anytime/Inbox |
  | project with `st=1` and today's date | Today | never (only `type = 0` is queried) |
  | `sb=1` ("This Evening") | Today, Evening section | Today, no evening flag |
  | repeating template (`rr` set) | hidden | listed as a normal task (see KI-05) |

  Upcoming/Someday also compare a date-only value (`sr` = midnight UTC) against the
  current instant (`now`) instead of comparing calendar dates.
- **Fix direction:** ADR-0005. One `ViewClassifier` in the domain layer operating on
  civil dates (KI-03), used by both SQL queries and change detection
  (`sync/detect.go:138-170`), verified against snapshots taken from a real Things
  database.

### KI-03 · "Today" is computed in UTC
- **Status:** fixed (branch `KI-03-owner-timezone`) for deployments that set
  `THINGS_TIMEZONE`; unset, the server keeps the UTC day and warns at startup ·
  **Evidence:** confirmed
- **Symptom:** for a user in Moscow (UTC+3), between 00:00 and 03:00 local time the
  server still thinks it is yesterday: Today shows yesterday's list, `when: "today"`
  writes yesterday's date, `deadline` validation rejects "today" as past.
- **Root cause:** Things stores day-precision fields (`sr`, `tir`, `dd`) as *midnight
  UTC of the user's local calendar date*. The server derives that calendar date from
  `time.Now().UTC()` instead of the user's timezone: `todayMidnightUTC` in
  `server/write.go` (also used by the view classifier in `server/readmodel.go`),
  `server/mcp.go` date parsing, `buildRepeatRule`'s `time.Now()`. Before the SDK
  migration also `sync/state.go` and `sync/detect.go`.
- **Fix direction:** ADR-0003. Configurable IANA timezone, a `Clock` interface, and a
  civil-date type for every day-precision value.
- **Fix:** `THINGS_TIMEZONE` (IANA name, checked at startup, zone data built in) and
  `domain/civil` (`Date`, `Clock`, `Today`, wire encoding as midnight UTC).
  `ownerToday()` (`server/today.go`) is taken once per request: the view classifier
  compares `sr`/`dd` as civil dates, `when` (`today`, `YYYY-MM-DD`), `things_move_to_today`
  and the deadline check use the owner's day, and `completed_after`/`completed_before`
  dates start at the owner's midnight. Tests: `TestToday_KI03`, `TestParseWhen_KI03_OwnerDay`,
  `TestParseDeadline_KI03_OwnerDay`, `TestViews_KI03_OwnerDay`. The sandbox harness reads
  `THINGS_TIMEZONE` the same way: its midnight skip window and the day the wire check
  expects follow the zone (branch `KI-34-harness-owner-timezone`; `TestSandboxNearMidnight`,
  `TestItemCivilToday`, `TestWriteFirewall_KI34_OwnerZone`).
- **Not done (ADR-0003 notes):** list responses do not report `today` yet (output shape,
  ADR-0006); the CI sandbox run does not set `THINGS_TIMEZONE`, so the owner's-zone paths
  run end to end only locally (`docs/testing/sandbox-e2e.md`, "CI"); `buildRepeatRule`
  is unwired and untouched (KI-01).

### KI-04 · Year-4001 sentinel leaks out as a real date
- **Status:** fixed ([#21](https://github.com/pachamaker/things-cloud-mcp/pull/21), `a53bb30`) · **Evidence:** confirmed for
  `rr.ed` (SDK `repeat.go`, `IsNeverending`); observed by the operator in `deadline`
- **Symptom:** tools return `deadline: "4001-01-01"`.
- **Root cause:** Things uses 4001-01-01 (`64092211200`) as "never". The mirror stores
  it verbatim; PR #1 hid it only in `formatTask`'s `deadline`, so REST responses,
  `scheduled_for` and the view rules still saw it (a deferred to-do with a sentinel
  start date was listed in Upcoming instead of Someday).
- **Fix:** `server/readmodel.go` clears every start date and deadline with year ≥ 4000
  on each task it reads from the mirror (`clearSentinels`), before view classification,
  pagination or output: every list and view, `things_list_completed`, `things_get_task`
  and the REST endpoints. Completion dates are instants and stay as they are;
  `formatTask` applies the same rule to both day fields. Tests:
  `TestClearSentinels_KI04`, `TestReadModel_KI04_NoSentinelInAnyList`,
  `TestReadModel_KI04_NeverStartDateIsSomeday`,
  `TestReadModel_KI04_CompletedKeepsCompletionInstant`, `TestFormatTask_KI04_NoSentinelDates`.
- **Not done:** the SDK still stores the sentinel; write preconditions read the raw
  mirror, but check no dates. ADR-0003 decision 7 (`civil.FromWire`) waits for the
  server's own domain model.

### KI-05 · Lists contain non-actionable items; `status` is unreliable
- **Status:** partially fixed — canceled items excluded from open views (PR #1) and,
  since the SDK migration, from every open list in `server/readmodel.go`; templates and
  the empty-status case remain · **Evidence:** confirmed
- **Symptom:** list tools return items with `status: "canceled"` or an empty status, and
  occasionally repeating templates.
- **Root cause (embedded SDK):** every list query filtered `status != 3` only (`sync/state.go`), so
  canceled (`ss = 2`) items pass. `formatTask` leaves `status` empty for unknown values
  (`server/mcp.go:65-72`). Templates are indistinguishable because `rr` is not stored
  (KI-01).
- **Fix direction:** open views mean `status = 0 AND in_trash = 0 AND NOT template`;
  canceled items appear only in Logbook queries; unknown statuses become an explicit
  `"unknown"` and are logged.
- **Upstream:** [arthursoares/things-cloud-sdk#40](https://github.com/arthursoares/things-cloud-sdk/pull/40) (issue [#39](https://github.com/arthursoares/things-cloud-sdk/issues/39)) stores `rr`/`rt` in the
  SDK mirror and adds `Task.IsRepeatingTemplate()` / `IsRepeatingInstance()`. Once it is
  released, bump the SDK and drop templates from open lists here. The sandbox seed checks
  confirm the gap on real data: both repeating seed templates are listed in Someday.

### KI-13 · Tasks under headings are missing from project listings
- **Status:** fixed ([#21](https://github.com/pachamaker/things-cloud-mcp/pull/21), `a53bb30`) · **Evidence:** confirmed on
  the sandbox account: before the fix, e2e run 38139217523 on `main` logged
  `SEED under heading` as missing from its project; with it, a local
  `TestSandboxSeedChecks` run (2026-10-11, run `e2e-20261011-1246-fb3f`) lists it.
  The seed check fails if the to-do goes missing again
- **Root cause:** SDK `State.TasksInProject` filters on `project_uuid`. In
  Things, a task placed under a heading carries the heading in `agr` and usually no
  `pr`; its project is the heading's project.
- **Fix:** `readModel.TasksInProject` (`server/readmodel.go`) lists the to-dos directly
  in the project (SDK `TasksInProject`), then, for each of the project's headings in
  heading order (SDK `HeadingsInProject`), its to-dos (SDK `TasksUnderHeading`); each
  group keeps the SDK's index order. A to-do with both `pr` and `agr` is listed once,
  under its heading. Open-list filtering (KI-05) and `limit`/`offset` apply to the
  merged list. Every task output now carries `heading_id`; `things_list_project_tasks`
  adds `heading_title`. Tests: `TestReadModel_KI13_ProjectIncludesHeadings`,
  `TestReadModel_KI13_ProjectHeadings`, `TestProjectTasksResult_KI13_HeadingFields`;
  the offline harness self-test checks `SEED under heading` against the SDK's SQLite
  mirror.
- **SDK limitations:** `HeadingsInProject` applies the to-do status/trash filters to
  the headings themselves, so the server asks for all headings and lets each to-do's
  own status decide (an archived heading does not hide an open to-do). Whether Things
  also sets `tr` on the to-dos of a trashed heading is not verified; the server treats
  them as trashed with it. One query per heading, on top of the SDK's query per row.
- **Not done:** `project_id` stays the to-do's own `pr`, so it is empty for a to-do
  under a heading (name-resolved project and heading: ADR-0006); `things_list_area_tasks`
  and the view classifier do not resolve projects through headings.

### KI-14 · Output hides what the model needs; IDs instead of names
- **Status:** open (`heading_id` in every task output and `heading_title` in project
  listings since KI-13) · **Evidence:** confirmed (`server/mcp.go:22-100`)
- **Symptom:** the model must make extra calls to turn `project_id`/`area_id`/tag UUIDs
  into names; it cannot see which view a task is in, whether it is in This Evening,
  under which heading, whether it repeats, or how many checklist items it has.
  `project_id` silently means "parent task" for subtasks.
- **Fix direction:** ADR-0006 tool surface v2 (enriched, name-resolved output).

---

## P1 — Consistency and workflow

### KI-06 · No atomic batch, no read-after-write contract
- **Status:** open · **Evidence:** confirmed for this server; the "does not exist" error
  string does not occur anywhere in this repo — it most likely comes from a second,
  Mac-local connector (e.g. `hald/things-mcp`, which writes through the Things URL
  scheme: its `json` bulk command needs Things.app to have synced the target first).
  KI-25 makes it worse: items written by current Things apps never reach this server's
  mirror
- **Symptom:** creating several tasks and then editing them is slow (each write is a
  pre-sync + commit + post-sync, `server/write.go:484-511`) and the operator falls back to
  a local connector whose JSON batch is all-or-nothing and depends on Things.app
  having synced.
- **Root cause:** one-item-per-commit writes; write tools return only a UUID; no batch
  tool with per-item results; two independent writers (cloud MCP and Things.app via
  URL scheme) with different consistency models.
- **Fix direction:** ADR-0006 — `things_batch` that commits N items in one history
  commit (the protocol already supports multi-item commits, see `things-cli batch`),
  validates all preconditions first, and returns per-item results plus the resulting
  entities. Make the remote MCP the only writer.

### KI-12 · Tags are addressed only by UUID; the model invents tags
- **Status:** open · **Evidence:** confirmed (`server/write.go:150-167`)
- **Fix direction:** accept tag *titles*, resolve against existing tags
  (case-insensitive), reject unknown names with a list of close matches. Tag creation
  stays a separate, explicit tool.

### KI-15 · CI never runs
- **Status:** fixed (PR #7, on `main` via PR #8, `fe8c3e2`): `.github/workflows/ci.yml` runs
  `go mod tidy -diff`, `go build -o /dev/null ./...`, `go vet ./...` and
  `go test -race ./...` on every pull request and every push to `main`, with the Go
  version from `go.mod`; `.github/Workflows/` (old `go.yml`, Fly.io deploy, nested
  duplicate) is deleted. `govulncheck` runs since KI-32, `staticcheck` since KI-33
  (phase 0) · **Evidence:** confirmed
- **Root cause:** workflows live in `.github/Workflows/` (capital `W`) — GitHub only
  reads `.github/workflows/`. There is also a nested duplicate
  `.github/Workflows/.github/Workflows/deploy.yml`. `go.yml` pins Go 1.23 while
  `go.mod` requires 1.25 (the SDK's minimum). The previously failing `TestIntegration/query_change_log`
  passes since PR #1.
- **Fix direction:** ADR-0007.

### KI-16 · Hard-coded runtime assumptions from the Fly.io era
- **Status:** fixed — cache at `$DATA_DIR/things-upstream.db` (PR #4); module
  renamed to `github.com/pachamaker/things-cloud-mcp` (SDK migration); the version comes
  from `-ldflags "-X main.version=…"` (Dockerfile `ARG VERSION`, default `dev`) and is
  reported in MCP `serverInfo` and on `/` (phase 0); release image (phase 2): static
  `CGO_ENABLED=0` build with `-trimpath` on `distroless/static-debian13:nonroot`, both
  base images pinned by digest, uid `65532` with `/data` (`0700`) owned by it, image
  `HEALTHCHECK` through `things-server healthcheck` (the public `/` on loopback; no shell
  in the image), and `release.yml` pushing linux/amd64 + linux/arm64 to GHCR on `v*`
  tags after the CI checks · **Evidence:** confirmed
- **Verified (2026-10-11, locally):** both platforms build; the binary is static and
  carries the version; offline (`--network none`) the image exits with the auth or
  credentials error, and with dummy credentials creates the cache in a fresh named
  volume as uid `65532`; two `--no-cache` multi-arch builds with `SOURCE_DATE_EPOCH` and
  `rewrite-timestamp` give identical digests. **Not verified:** the release workflow
  itself (runs on the first tag) and pulling the image on the VPS.
- **Still open elsewhere:** `/readyz` and structured logs (roadmap Phase 2).
- **Fix direction:** ADR-0001 (fork identity) and ADR-0002 (deployment).

### KI-34 · The write path has no automated end-to-end check on a real account
- **Status:** in-progress — harness and workflow on `main` (PR #9); the sandbox account
  and the `things-sandbox` environment exist. First CI run 2026-10-11
  ([run 38093792463](https://github.com/pachamaker/things-cloud-mcp/actions/runs/38093792463)):
  `TestSandboxE2E` passed all 12 subtests against Things Cloud (placement, edit,
  lifecycle, checklist, `repeat` rejection, reads, 409 retry, observer mirror, wire
  check, cleanup of 13 objects); the next two steps, `TestSandboxSeedChecks` and
  `TestSandboxCleanupLeftovers`, failed with `429 Too Many Requests` (KI-27): each starts
  a fresh server with a full initial sync right after the previous one. Runs
  [38139037091](https://github.com/pachamaker/things-cloud-mcp/actions/runs/38139037091)
  and [38139217523](https://github.com/pachamaker/things-cloud-mcp/actions/runs/38139217523)
  (2026-10-11, `main` `d4f20fc`) reached the seed checks and failed only on `SEED tagged`:
  its `tg` held `DB1Y39WquEtbCvrGVFH8qg`, not the one live tag titled `SEED tag`. The
  harness reads `tg` exactly as the SDK does, so this is seed data, not a read bug; the
  seed check now says what the to-do carries (a deleted tag, a tag with another title,
  an unknown UUID) and how to fix it in the app (`docs/testing/sandbox-e2e.md`, "Fixing
  seeds"), and looks seeds up by kind, so a checklist item or area named like a seed no
  longer counts (branch `KI-34-seed-tag-report`). A local run of that branch at
  13:02 UTC the same day passed every seed check: `SEED tagged` carries `SEED tag`, and
  the KI-05 templates are logged as known issues. History had one change more than at
  the failed run, so the seed was most likely corrected in the app in between ·
  **Evidence:** confirmed
- **Symptom:** the only end-to-end checks were the shell scripts in `tests/`. They write
  into whatever account the server uses, cover part of the tools, check nothing on the
  wire, and leave areas and tags behind. A payload regression of the KI-01/KI-26/KI-31
  kind would first show up as broken Things apps.
- **Fix direction (done on the branch):** Go tests behind build tag `e2e`: the server
  in-process on a temp `DATA_DIR`, a real MCP client on `/mcp`, credentials only from
  `THINGS_SANDBOX_*` or `THINGS_SANDBOX_ENV_FILE`, an account guard (area
  `MCP E2E SANDBOX`, username ≠ `THINGS_USERNAME`), a write firewall that refuses any
  commit outside the verified write set or outside the run's own objects before it is
  sent, a deterministic 409 retry test, an independent observer client that checks the
  stored history and its own mirror, run-prefix titles with cleanup (also on failure)
  and a leftover cleanup test, a 1 request/s throttle, and read-only checks of a manual
  seed list (`Task7`, repeating to-dos). Offline self-test against an in-memory fake:
  `go test -tags 'e2e fakecloud' -run '^TestSandbox' ./server`.
- **Remaining:** a green seed-check run in CI (green locally on 2026-10-11), then a Mac +
  iOS round trip (ADR-0004). Since
  phase 0 the harness retries a GET after a 429 (30 s, 1 min, 2 min, or `Retry-After`;
  commits are never retried) and the workflow gives the seed and cleanup steps room for
  it; not yet confirmed against Things Cloud. The second CI run (2026-10-10, 23:44 UTC)
  was green but skipped every test inside the midnight window; since then CI sets
  `THINGS_E2E_REQUIRE_RUN=1`, which turns a skip into a failure. The midnight window and
  the wire check's day follow `THINGS_TIMEZONE` (branch `KI-34-harness-owner-timezone`);
  the workflow does not set it, so CI runs on the UTC day. `things_edit_task` with `when: "none"`
  writes `sr`/`tir` without `st`, which is not a verified placement: the firewall refuses
  it, so the harness does not exercise it; capture what the official client sends, or
  write `st` explicitly.

### KI-17 · Global mutable state in the server package
- **Status:** open · **Evidence:** confirmed (`server/main.go:20-24`, `server/write.go:21`)
- **Impact:** handlers cannot be tested in isolation; two `History` objects (the
  syncer's and the writer's) keep separate cursors.
- **Fix direction:** introduce an `Engine` struct owning client, syncer, writer, clock
  and config (ADR-0006, target architecture in `docs/architecture.md`).

---

## P2 — Quality

| ID | Issue | Where |
|---|---|---|
| KI-18 | N+1 queries: every list loads each task + tags separately; since the SDK migration every view loads all open to-dos | SDK `sync/state.go` (`scanTaskUUIDs`), `server/readmodel.go` |
| KI-19 | `things_search_tasks` has no limit and scans all tasks in Go | `server/mcp.go:666` |
| KI-20 | Change detection calls `time.Now()` directly (non-deterministic tests) | SDK `sync/detect.go`; not used by the server |
| KI-21 | Retry classification by substring of the error text — fixed for syncs by the SDK (typed `*HTTPError`); write 409 detection is a suffix match until the SDK types `History.Write` errors (spike report §4 B2) | `server/write.go` `isConflictError` |
| KI-22 | `Host` header set manually and misleadingly | SDK `client.go:117` |
| KI-23 | 14 CLI tools without tests; several duplicated server write logic with a divergent wire format — resolved: removed with the SDK migration (upstream has its own `things-cli`) | — |
| KI-24 | Write tools have no MCP annotations beyond defaults (`destructiveHint` defaults to true, so every write asks for confirmation; no `idempotentHint`) | `server/mcp.go:1095-1329` |
| KI-33 | ~~`staticcheck` (2026.2.1) fails with U1000: `authHandlerMiddleware` is never used~~ — fixed (phase 0): the dead middleware is deleted (`/mcp` auth stays with KI-07/ADR-0002) and `staticcheck -tags 'e2e fakecloud'` runs in CI | `.github/workflows/ci.yml` |

## P1 — Resilience against Things Cloud behaviour (reported by other implementations)

| ID | Issue | Evidence | Fix direction |
|---|---|---|---|
| KI-27 | `429 AbusePrevention` on reads: every tool call syncs, bursts of calls trip it | [wbopan#32](https://github.com/wbopan/things-cloud-mcp/issues/32) | Coalesce/throttle syncs (freshness window), backoff on 429 — done for syncs in `server/syncgate.go` (PR #4); writes are not gated. The sandbox harness retries GETs after a 429 (phase 0): consecutive CI steps hit it on the first run (KI-34) |
| KI-28 | History key change or `404 AccountIssue` after an account reset leaves a stale cursor | [wbopan#27](https://github.com/wbopan/things-cloud-mcp/issues/27), [#35](https://github.com/wbopan/things-cloud-mcp/issues/35) | Detect key change / 404, rebuild the mirror |
| KI-29 | One malformed historical tombstone can block processing | [arthursoares#37](https://github.com/arthursoares/things-cloud-sdk/issues/37) | Quarantine bad items with a log entry instead of aborting or skipping silently |
| KI-30 | Client identity is a Things 3.22-era build (`ThingsMac/32209501`; 3.23 is `32300536`) | SDK `client.go` (`DefaultClientInfo`, `ThingsUserAgent`) | Hypothesis: the cloud may adapt responses to the client version. Decide deliberately (see `legal-and-risk.md`); do not change blindly |
| KI-31 | Negative `ix` crashed Things on Mac and iOS at launch in another implementation | [wbopan#16](https://github.com/wbopan/things-cloud-mcp/issues/16) | Never write negative indexes; verify what the official client sends for new items |
