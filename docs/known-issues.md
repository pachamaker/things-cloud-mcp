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
| Between 00:00 and 03:00 MSK the server's "today" is still yesterday | KI-03 |
| `deadline = 4001-01-01` means "no deadline" | KI-04 |
| Don't trust `status` in lists; filter `status == "open"` yourself | KI-05 |
| Create → list → edit one by one; batch update fails on the first unsynced ID | KI-06 |
| Use only existing tags | KI-12 |

## Recently merged changes

| PR | Merged | Effect on this register |
|---|---|---|
| [#1](https://github.com/pachamaker/things-cloud-mcp/pull/1) Today, Someday, Upcoming per Things rules | 2026-10-10 (`f79b05e`) | KI-02 core rules (things.py three-set Today, incl. `st=2` with start date ≤ today), KI-04 (sentinel hidden in output), KI-05 (canceled excluded). Still UTC (KI-03); no `dds`, evening, projects in Today, template hiding |
| [#2](https://github.com/pachamaker/things-cloud-mcp/pull/2) Reject every `repeat` value | 2026-10-10 (`7aa1009`) | KI-01 mitigated on all write surfaces, incl. `repeat: "none"`; details in [`known-issues/repeat-disabled.md`](known-issues/repeat-disabled.md) |
| [#3](https://github.com/pachamaker/things-cloud-mcp/pull/3) 22-character Base58 IDs | 2026-10-10 (`c622fdb`) | KI-26 fixed on `main` |
| [#4](https://github.com/pachamaker/things-cloud-mcp/pull/4) Safety adapters over upstream SDK | 2026-10-10, into `spike-upstream-sdk` | Upstream migration (ADR-0001 option B): sync gate + 429 backoff (KI-27), ID redraw (KI-26), no HTTP dumps from `DEBUG` (KI-09), cache under `DATA_DIR` (KI-16). Reaches `main` with the migration PR |
| Upstream SDK migration (`spike-upstream-sdk` → `main`) | pending review | KI-25 fixed (SDK reads `Task7`), KI-02/KI-05 rules ported to `server/views.go`, KI-21 fixed for syncs, KI-23 resolved (CLI tools removed); `today_index_ref` no longer in tool output. Spike report: `docs/2026-10-10-upstream-sdk-spike.md` |

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
- **Status:** open · **Evidence:** confirmed (`server/main.go:559-560`)
- **Symptom:** anyone who learns the URL can read and modify the whole task database.
- **Root cause:** OAuth was never implemented; the original Fly.io setup relied on an
  obscure hostname.
- **Fix direction:** ADR-0002. OAuth 2.1 (single user) in front of `/mcp`. Until then,
  do not expose the server on a public address.

### KI-08 · Destructive debug endpoints ship in the production binary
- **Status:** open · **Evidence:** confirmed (`server/main.go:463-557`)
- **Symptom:** with `DEBUG=true` and an `API_KEY`, one POST deletes the Things Cloud
  history (`/api/debug/delete-history`) or the whole Things account
  (`/api/debug/nuke-account`).
- **Fix direction:** remove from the server; keep account surgery in a separate CLI
  that requires interactive confirmation and a sandbox account.

### KI-09 · `DEBUG=true` logs the Things password
- **Status:** mitigated — the server never sets the SDK's `Client.Debug`; `DEBUG` only
  gates the debug endpoints (PR #4) · **Evidence:** confirmed (SDK `client.go:135-146`
  dumps full requests incl. `Authorization: Password <plaintext>` when `Debug` is set)
- **Remaining:** the SDK logs a full response dump (no request headers) when a commit is
  rejected (`histories.go:281-282`).
- **Fix direction:** upstream PR: redact `Authorization` and `password` fields in dumps,
  plus an injectable `http.Client`; a test that the secret never appears in output.

### KI-10 · Writes are accepted for UUIDs that do not exist (or have the wrong type)
- **Status:** open · **Evidence:** confirmed (`server/write.go:650-852` validate only the
  Base58 shape)
- **Symptom:** a hallucinated or mistyped UUID produces a modify-item for an unknown
  object in the cloud history. Clients receive a partial entity with no creation
  payload. `things_edit_task` can also modify a project, heading, or repeating
  template as if it were a plain task.
- **Fix direction:** ADR-0004 preconditions — every write first resolves the target in
  the mirror (exists, not deleted, expected `type`, not a template) and fails fast
  otherwise.

### KI-11 · `things_smoke_test` is exposed to the LLM
- **Status:** open · **Evidence:** confirmed (`server/mcp.go:1333`)
- **Symptom:** the model can run a create/edit/complete/trash cycle against the real
  account at any time; leaves trashed `[smoke-test]` tasks behind.
- **Fix direction:** move to an admin CLI / CI job against the sandbox account.

---

## P1 — Read correctness (the "Today is wrong" family)

### KI-02 · View classification does not match Things
- **Status:** in-progress — PR #1 (`f79b05e`) implements the things.py rules in SQL on
  `main`; remaining: owner timezone (KI-03), `dds`, This Evening, projects in Today,
  template hiding, conformance harness. Since the upstream SDK migration the rules live
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
- **Status:** open · **Evidence:** confirmed
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

### KI-04 · Year-4001 sentinel leaks out as a real date
- **Status:** fixed in tool output (PR #1); normalising at ingestion still recommended ·
  **Evidence:** confirmed for `rr.ed` (SDK `repeat.go`, `IsNeverending`); observed by the
  operator in `deadline`
- **Symptom:** tools return `deadline: "4001-01-01"`.
- **Root cause:** Things uses 4001-01-01 (`64092211200`) as "never". The mirror stores
  it verbatim and `formatTask` (`server/mcp.go:81-83`) prints it.
- **Fix direction:** normalise any day-precision value with year ≥ 4000 to null where
  the server reads the SDK mirror (`server/readmodel.go`), or upstream at ingestion,
  with a test.

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

### KI-13 · Tasks under headings are missing from project listings
- **Status:** open · **Evidence:** likely
- **Root cause:** SDK `State.TasksInProject` filters on `project_uuid`. In
  Things, a task placed under a heading carries the heading in `agr` and usually no
  `pr`; its project is the heading's project.
- **Fix direction:** resolve the project through the heading (`heading_uuid →
  tasks.project_uuid`); return heading grouping in the output. The SDK already offers
  `HeadingsInProject` and `TasksUnderHeading`.

### KI-14 · Output hides what the model needs; IDs instead of names
- **Status:** open · **Evidence:** confirmed (`server/mcp.go:22-100`)
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
- **Status:** open · **Evidence:** confirmed
- **Root cause:** workflows live in `.github/Workflows/` (capital `W`) — GitHub only
  reads `.github/workflows/`. There is also a nested duplicate
  `.github/Workflows/.github/Workflows/deploy.yml`. `go.yml` pins Go 1.23 while
  `go.mod` requires 1.25 (the SDK's minimum). The previously failing `TestIntegration/query_change_log`
  passes since PR #1.
- **Fix direction:** ADR-0007.

### KI-16 · Hard-coded runtime assumptions from the Fly.io era
- **Status:** partially fixed — cache at `$DATA_DIR/things-upstream.db` (PR #4); module
  renamed to `github.com/pachamaker/things-cloud-mcp` (SDK migration) · **Evidence:** confirmed
- **Remaining:** deploy workflow targets Fly.io; server version string `1.1.0`
  hard-coded in `server/mcp.go`; Dockerfile uses `alpine:latest` and `CGO_ENABLED=1`
  although `modernc.org/sqlite` is pure Go.
- **Fix direction:** ADR-0001 (fork identity) and ADR-0002 (deployment).

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

## P1 — Resilience against Things Cloud behaviour (reported by other implementations)

| ID | Issue | Evidence | Fix direction |
|---|---|---|---|
| KI-27 | `429 AbusePrevention` on reads: every tool call syncs, bursts of calls trip it | [wbopan#32](https://github.com/wbopan/things-cloud-mcp/issues/32) | Coalesce/throttle syncs (freshness window), backoff on 429 — done for syncs in `server/syncgate.go` (PR #4); writes are not gated |
| KI-28 | History key change or `404 AccountIssue` after an account reset leaves a stale cursor | [wbopan#27](https://github.com/wbopan/things-cloud-mcp/issues/27), [#35](https://github.com/wbopan/things-cloud-mcp/issues/35) | Detect key change / 404, rebuild the mirror |
| KI-29 | One malformed historical tombstone can block processing | [arthursoares#37](https://github.com/arthursoares/things-cloud-sdk/issues/37) | Quarantine bad items with a log entry instead of aborting or skipping silently |
| KI-30 | Client identity is a Things 3.22-era build (`ThingsMac/32209501`; 3.23 is `32300536`) | SDK `client.go` (`DefaultClientInfo`, `ThingsUserAgent`) | Hypothesis: the cloud may adapt responses to the client version. Decide deliberately (see `legal-and-risk.md`); do not change blindly |
| KI-31 | Negative `ix` crashed Things on Mac and iOS at launch in another implementation | [wbopan#16](https://github.com/wbopan/things-cloud-mcp/issues/16) | Never write negative indexes; verify what the official client sends for new items |
