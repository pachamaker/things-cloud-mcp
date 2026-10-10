# Upstream SDK spike (2026-10-10)

Compatibility spike for [ADR-0001](adr/0001-fork-identity.md) option B: depend on
`github.com/arthursoares/things-cloud-sdk` as a Go module and keep only the MCP server
in this repository.

| | |
|---|---|
| Branch | `spike-upstream-sdk` (from `3164e8a`) |
| Upstream | `v0.6.0`, commit `6919edc`, `go 1.25.0` (latest tag on proxy.golang.org) |
| Module path on the branch | `github.com/pachamaker/things-cloud-mcp` |
| Verdict | **Conditional GO** (see [Recommendation](#recommendation)) |
| Status | Branch compiles against upstream; `go vet` and `go test ./...` pass (2026-10-11) |

## 1. What was done

1. 2026-10-10, static pass. Upstream `v0.6.0` was downloaded into the module cache and
   compared with the embedded copy and with every SDK symbol `server/` uses. Adding the
   dependency was held back by the agent's permission policy until the owner approved
   it, so §3 and §4 were first written from reading code.
2. 2026-10-11, after the owner's approval:
   - `go get github.com/arthursoares/things-cloud-sdk@v0.6.0` + `go mod tidy`
     (`go` directive 1.24 → 1.25, `golang.org/x/sys` → v0.44.0, `modernc.org/sqlite`
     becomes indirect).
   - Module renamed to `github.com/pachamaker/things-cloud-mcp`. Imports in `server/`
     are unchanged (`github.com/arthursoares/things-cloud-sdk[/sync]`).
   - Removed from the module: the embedded SDK (root package `thingscloud`, incl.
     `itemaction_string.go`), `sync/`, `state/`, `syncutil/`, `cmd/`, `tapes/`, and
     `example/` (an SDK example with its own copy of the KI-26 encoder; upstream ships
     an updated one). 62 files.
   - The compiler reported 16 errors in `server/` non-test code, all of them C1–C8 from
     §3 (no surprises), plus test-only fallout of C5, C6, C7. They are closed with the
     minimal changes listed in §3, mostly in a new thin layer `server/readmodel.go`.
   - Only phase 1 of §6 is done. The phase 2 safety adapters (B1, B2 follow-up, B5,
     B6, B7) and the phase 3 view classifier are **not** implemented: the branch must
     not be deployed as is.

## 2. Build, vet and test results

| Tree | `go build ./...` | `go vet ./...` | `go test ./...` |
|---|---|---|---|
| Baseline `3164e8a` (clean export, run in a scratch directory) | ok | ok | 1 known failure: `sync: TestIntegration/query_change_log` (KI-15). `server`, root SDK, `state/memory`: ok |
| `spike-upstream-sdk` | ok | ok | ok (`server` is the only package; `-race` on `server`: ok) |

Notes:

- With a single `main` package, `go build ./...` tries to write a binary named
  `server` over the directory of the same name; use `go build -o /dev/null ./...` or
  `go build -o things-server ./server`.
- The `server` tests do not exercise concurrent syncs, so `-race` passing says nothing
  about B1.
- No upstream test suite was run (Task7, replay and fuzz tests were read, not
  executed). The Docker image was not built (C9 is a one-line change). Nothing was run
  against a Things account.

## 3. Compile-level gaps (`server/` against upstream `v0.6.0`)

Every other SDK symbol used by `server/` exists upstream with the same signature:
`New`, `APIEndpoint`, `Client.{Debug,EMail,Accounts,Verify,OwnHistory,HistoryWithID,Histories}`,
`History.{Sync,Write,Delete,ID,LatestServerIndex}`, `AccountService.{Delete,SignUp,Confirm,AcceptSLA}`,
`Identifiable`, `Task`, `Area`, `Tag`, `CheckListItem`, `TaskStatus*`, `TaskType*`,
`TaskScheduleInbox`, `sync.{Open,Syncer,Change}`, `Syncer.{Sync,Close,State}`,
`State.{Task,Area,Tag,AllTasks,AllProjects,TasksInInbox,TasksInToday,TasksInAnytime,TasksInSomeday,TasksInUpcoming,TasksInProject,TasksInArea,ChecklistItems}`.
`writeEnvelope` (`server/write.go:39-46`) still satisfies `Identifiable`.

| # | Used by server | Where | Upstream `v0.6.0` | Local fix | Effort |
|---|---|---|---|---|---|
| C1 | `sync.QueryOpts.Limit` / `.Offset` | `server/main.go:65,72`, `server/mcp.go:186-197` | `QueryOpts` has only `IncludeCompleted`, `IncludeTrashed` (`sync/state.go:22-25`) | Generic `paginate([]T, limit, offset)` in the local read model, applied to slices returned by `State` | 1 h |
| C2 | `State.AllAreasWithOpts` | `server/main.go:238`, `server/mcp.go:301` | only `AllAreas()` | `AllAreas()` + `paginate` | 15 min |
| C3 | `State.AllTagsWithOpts` | `server/main.go:261`, `server/mcp.go:316` | only `AllTags()` | `AllTags()` + `paginate` | 15 min |
| C4 | `State.CompletedTasksInRange(limit, after, before)` | `server/mcp.go:393` | no completed/logbook query at all | Read model: `AllTasks(IncludeCompleted)`, keep `status = 3`, filter by completion window, sort desc, limit | 1–2 h |
| C5 | `things.Task.TodayIndexReference` | `server/mcp.go:90-91`, `server/mcp_format_test.go:25` | field absent; upstream never persists `tir` | Drop `today_index_ref` from tool output (re-add if upstream persists it, B4) | 15 min |
| C6 | `thingscloud.HTTPStatusError` | `server/write.go:514`, `server/write_conflict_test.go` | type absent; upstream has `*HTTPError`, but `History.Write` returns an untyped `fmt.Errorf("Write failed: %d")` (`histories.go:283`) | See B2 | 30 min + upstream PR |
| C7 | `thingscloud.NeverendingRepeatYear` | `server/write.go:436`, `server/write_repeat_test.go:117` | absent (literal `4001` inside `IsNeverending`) | Only used by the repeat writer, which ADR-0004/KI-01 removes; otherwise a local constant | 5 min |
| C8 | `History.RawItems()` | `server/main.go:436` (`/api/debug/history`) | absent; only `History.Items(ItemsOptions)` | Remove the endpoint (KI-08 removes debug endpoints anyway) | 15 min |
| C9 | Toolchain | `go.mod` (`go 1.24.0`), `Dockerfile` (`golang:1.24-alpine`) | upstream requires `go 1.25.0` | `go mod tidy` bumps the directive; bump the builder image | 5 min |

Dependency overlap is benign: upstream pins the same `google/uuid v1.6.0` and
`modernc.org/sqlite v1.45.0`; `golang.org/x/sys` resolves to upstream's `v0.44.0` by MVS.

### 3.1 How C1–C9 are closed on the branch

About 330 added and 76 removed lines in 11 files, about half of them tests.

| # | Change | Behaviour vs baseline |
|---|---|---|
| C1–C3 | `server/readmodel.go`: `listOpts` (= `sync.QueryOpts` + `Limit`/`Offset`), generic `paginate`, and `readModel`, which embeds `*sync.State` and shadows the list methods with paginated versions. Handlers call `readState()` instead of `syncer.State()` for lists | Same results. Pagination now happens in Go after loading the full list, so every page costs a full N+1 load (KI-18) |
| C4 | `readModel.CompletedTasksInRange` → pure `completedInRange` over `AllTasks(IncludeCompleted)` | Same filter, order and default limit (50); loads all to-dos |
| C5 | `today_index_ref` removed from `taskOutput` | **Visible output change** for MCP clients |
| C6 | `isConflictError`: typed `*thingscloud.HTTPError` 409, or an error message ending in `Write failed: 409` | **Regression vs March 2026**: 409 detection is a string match again (KI-21 style). The test that forbade string matching now asserts the narrow suffix match instead. Remove after the upstream PR (B2) |
| C7 | Local `neverendingRepeatYear = 4001` | Same |
| C8 | `/api/debug/history` reads the first page through `HistoryWithID(history.ID).Items(...)` (separate `History`, so the writer's cursor is untouched) and adds `uuid` (upstream's `Item.UUID` is `json:"-"`) | First page of parsed items instead of raw JSON; debug-only (KI-08 removes it) |
| C9 | `go 1.25.0` in `go.mod`; `Dockerfile` builder `golang:1.25-alpine` | Image not built yet |

New tests: `server/readmodel_test.go` (`paginate`, `completedInRange`); updated:
`write_conflict_test.go`, `mcp_format_test.go`, `write_repeat_test.go`.

## 4. Behavioural gaps (compile fine, behave differently)

| # | Area | Embedded copy (`3164e8a`) | Upstream `v0.6.0` | Impact on the server | Closing |
|---|---|---|---|---|---|
| B1 | Sync concurrency | `Syncer.mu` serialises `Sync()`, reads take `RLock` (March 2026 incident fix) | No mutex (`sync/sync.go:28-34`). `Sync()` swaps `s.db` to a transaction and mutates `s.history` | Every tool call syncs; parallel calls race on the syncer (double-applied batches, `-race` failures) | Local wrapper that serialises and coalesces `Sync()` (freshness window, architecture principle 5). Reads through `State` use the raw pool and are safe under WAL. 2 h incl. race test |
| B2 | 409 retry on write | `errors.As(*HTTPStatusError)` → retry once | `Write` returns an untyped error; `isConflictError` never matches | Concurrent edit from a Things app → write fails instead of retrying | Done on the branch as a stopgap: suffix match on `Write failed: 409` (fragile, cf. KI-21; §3.1 C6). Upstream PR: return `*HTTPError` from `Write` |
| B3 | View queries | Today: `st=1` and `sr` **or** `tir` on the current UTC day; Anytime: `st=1` not Today; Someday: `st=2` not Upcoming; Upcoming: `st=2`, `sr`/`tir` > now (UTC) | Today: `st=1`, `sr` within `startOfDay(time.Now())` in the **process time zone**, no `tir`; Anytime: `st=1 AND sr IS NULL`; Someday: `st=2 AND sr IS NULL`; Upcoming: `st=2 AND sr >= tomorrow` (`sync/state.go:120-174`) | Two KI-02 cases disappear from **every** list: `st=1` with a past start date and `st=2` with start date ≤ today. The embedded copy at least shows them in Anytime/Someday. Process time zone: correct for UTC and positive offsets only; with a negative offset Today shows tomorrow's tasks | Do not use upstream `TasksIn*` for views. Implement the ADR-0005 classifier over `State.AllTasks` (it is needed under option A too) |
| B4 | Persisted task fields | Stores `tir` (`today_index_ref`); drops `rr`, `rt`, `sb`, `dds` | Stores none of `tir`, `dds`, `sb`, `rr`, `rt` (`RecurrenceIDs` is on the struct but not stored; `recurrence_rule` column always NULL) | The classifier cannot honour deadline suppression, This Evening, or hide repeating templates (KI-05); KI-01's "persist `rr`/`rt` read-only" is impossible on this mirror | Upstream PR: add the columns and bump `TaskReplayVersion` — upstream's replay machinery then rebuilds old caches automatically. Fallback: soft fork via `replace` (§7) |
| B5 | Schema numbering | `schemaVersion = 4` (adds `today_index_ref`, v4 wipes the cache) | `schemaVersion = 2` (`sync/schema.go:3`) + separate `task_replay_state` generation (`TaskReplayVersion = 3`) | Opening an existing `/data/things.db`: `migrate()` is a no-op (4 ≥ 2), replay generation defaults to 1 → backup + full replay on first sync; `today_index_ref` stays stale; any future upstream migration numbered 3–4 would be **silently skipped** on this file | Start upstream on a fresh database file (the cache is disposable), together with `DATA_DIR` (KI-16). 1 h |
| B6 | ID generation | `generateUUID` drops a leading zero byte (KI-26) | `History.Write` refuses non-canonical or duplicate UUIDs (`histories.go:233`); `NewUUID` pads leading zero bytes with `1` (`base58.go:20-41,139`) | With the current server generator ~1/256 creates now fail with an error instead of poisoning the account. Upstream's padding contradicts AGENTS.md rule 4 and the ZhehanZhang crash report (see §5) | Local `newID()`: redraw until byte 0 ≠ 0, then `things.EncodeUUID`; replace `isBase58UUID` with `things.ValidateUUID`. 1 h |
| B7 | Debug logging | Dumps bounded, but still include `Authorization` (KI-09) | `Client.Debug` dumps full requests incl. `Authorization: Password …` (`client.go:135-146`); `Write` logs a full response dump on non-200 (`histories.go:281-282`); the `http.Client` is unexported, no injection point | KI-09 unchanged or worse; no transport-level hook for redaction or 429 backoff (KI-27) | Never set `client.Debug` in the server (`server/main.go:386`, `server/write.go:488`). Upstream PR: redaction + `WithHTTPClient` option. 15 min locally |
| B8 | Retry classification | substring match on `500`…`504` (KI-21) | typed `*HTTPError`, 500–504 only | KI-21 fixed for reads; 429 still not retried (KI-27) | Backoff in the B1 wrapper |
| B9 | Cursor persistence | saved after the whole sync | saved per batch inside the batch transaction; `validateReplayPage` rejects non-advancing pages | Better: a failed sync resumes exactly after the last committed batch, no double-applied note deltas | — |
| B10 | Unknown entity kinds | any unknown kind → `UnknownChange`, cursor advances (KI-25 root cause) | unknown `Task*` kind → `UnsupportedTaskKindError`, the whole batch is rejected before any mutation; non-task kinds (`Settings5`, `Command`) still advance | Desired fail-loud behaviour (KI-25). Operationally a future `Task8` stops all reads until the SDK is upgraded | Surface the error in tool results; alert on it |
| B11 | Soft-deleted rows | purged after each sync | never purged | Unbounded growth of deleted rows (queries filter `deleted = 0`); negligible for one user | Optional upstream PR |
| B12 | Account URLs | email path-escaped in `AccountService` | not escaped | Only the debug account endpoints use it, which KI-08 removes | — |
| B13 | History key change (KI-28) | cursor reset to 0, rows kept | same | unchanged | Out of scope |
| B14 | Task7 write preflight | n/a | Task7 modifications scan the full history before every commit | None today: the server writes `Task6`, which upstream passes through unchanged. Upstream documents Task6 modifications after a Task7 creation as valid (`docs/task7-write-policy.md`) | Keep Task6 until ADR-0004 evidence exists |

## 5. Verification of the upstream fixes the ADR relies on

Checked by reading `v0.6.0` source and tests; nothing was executed (§1).

| Claim | Result | Evidence |
|---|---|---|
| Reads `Task7` (KI-25) | ✅ | `ItemKindTask7` goes through the same `processTaskItem` path as `Task6` (`sync/process.go:88`). `ValidateTaskReadKinds` preflights every batch and rejects other `Task*` kinds before state is touched (`sync/process.go:22-25`, `task_read.go:43-58`). `DecodeTaskReadPayload`/`ApplyNulls` distinguish omitted from explicit-null fields; `tir` never overwrites `sr`. Tests: `sync/task7_test.go` (500 lines), `task_read_test.go` |
| Safe Base58 IDs (KI-26) | ✅ with a caveat | `base58.go`: canonical `EncodeUUID`, strict `DecodeUUID` (must be exactly 16 bytes), `ValidateUUID`, `NewUUID`, `EncodeLegacyIdentifier`. `History.Write` refuses invalid and duplicate UUIDs before any network call. Fuzz tests: `FuzzUUIDRoundTrip`, `FuzzValidateUUIDIsSound`, `FuzzDecodeUUIDNeverPanics`. **Caveat:** leading zero bytes are encoded as leading `1`. Upstream's evidence: real Things IDs round-trip and Things' own legacy-derived IDs can start with `1` (`base58_test.go:91-116`). The ZhehanZhang fork reported a crash with `1` padding. The two are not reconciled, so the server should avoid the case entirely (B6) |
| Replays the cache after reader fixes | ✅ | `sync/replay.go`: a replay generation in `task_replay_state`; when it is older than `TaskReplayVersion` the first `Sync()` makes a `VACUUM INTO` backup next to the DB (mode 0600, hashed name, integrity-checked, deduplicated), replays the whole history into an in-memory staging DB, then installs it atomically, guarded by (history id, cursor, generation). `change_log` entries below the old cursor are suppressed. Tests: `sync/replay_backup_test.go` |

Operational note: the first start on an existing database writes a full-size backup
file next to it (`things.db.task7-backup-*`). Size the VPS volume accordingly or start
from a fresh file (B5).

## 6. Effort estimate

| Phase | Scope | Estimate |
|---|---|---|
| 1. Compile with parity | C1–C9 | ~0.5 day — **done on the branch** (§3.1) |
| 2. Safety adapters (must ship before the first production deploy) | B1 sync wrapper (+coalescing, 429 backoff), B2 conflict detection, B5 fresh DB file, B6 ID generator, B7 debug off | ~0.5–0.75 day |
| 3. Views on the local read model | ADR-0005 classifier for the cases the upstream mirror supports (`st`, `sr`, `dd`, `ss`, `tr`, type, parents, headings), replacing `TasksIn*`; optional in-memory snapshot keyed by `LastSyncedIndex` to avoid N+1 on every call | ~1 day |
| **Total to "server runs on upstream, no regression"** | | **~2–2.25 days** |
| Blocked on upstream | B4 fields (`dds`, `sb`, `rr`, `rt`, `tir`) for the full ADR-0005 rule set and KI-05/KI-01 | calendar time |

Phase 3 is roughly the work ADR-0005 requires under either option; the switch moves it
from "improvement" to "prerequisite", because upstream's view queries are worse than the
embedded ones (B3). Without phase 3 the effort is ~1–1.25 days.

For comparison, option A means hand-porting what §5 verifies: ~1,250 lines of upstream
production code (`task_read.go`, `task_write.go`, `base58.go`, `sync/replay.go`) and
~1,800 lines of their tests into a copy that has already diverged.

Upstream PRs to open (generic, not MCP-specific): typed error from `History.Write`;
persisted `tir`/`dds`/`sb`/`rr`/`rt` with a `TaskReplayVersion` bump; debug redaction
and an injectable `http.Client`; optionally `QueryOpts.Limit/Offset` and a concurrency
guard in `Syncer`.

## 7. Recommendation

**Conditional GO for option B.**

- The compile delta is small: nine items, all closable locally in about half a day.
- The protocol work we would otherwise redo is substantial and present in source:
  Task7 reads with fail-loud handling of unknown task kinds, canonical ID validation on
  every write, cache replay with backup, per-batch cursor persistence, typed retry
  errors.
- The blockers are behavioural (B1, B2, B3, B5, B6, B7). Each has a small local
  mitigation, listed in §4.

Conditions:

1. ~~The owner approves adding the dependency and the compiler confirms that C1–C9 are
   the complete list.~~ Met on 2026-10-11 (§1, §3.1).
2. The phase 2 safety adapters ship before any production deploy on upstream.
3. Views never use upstream `TasksIn*`; the read model goes through the local
   classifier (ADR-0005).
4. Pin `v0.6.0` exactly; upgrades go through CI and the conformance suite (ADR-0001).
5. Open the upstream PRs from §6. If the B4 fields PR is not accepted within a few
   weeks, carry it as a **soft fork** (`replace github.com/arthursoares/things-cloud-sdk
   => github.com/pachamaker/things-cloud-sdk <tag>` with a small patch set rebased on
   upstream releases) rather than falling back to option A.

Not a NO-GO: nothing found requires changing upstream before the server can run
safely. The only hard dependency on upstream is the full ADR-0005 rule set (B4), and it
has a fallback.

## 8. Next steps

The spike itself is finished: the dependency is added (approved by the owner on
2026-10-11), the embedded SDK is gone, and the branch builds, vets and tests green.

If option B is accepted:

1. Mark ADR-0001 Accepted; keep this branch as the starting point, or redo phase 1 on a
   fresh branch after open PRs #1 and #2 are merged (ADR-0001 consequences).
2. Phase 2 safety adapters (§6), then phase 3 view classifier (ADR-0005). Do not deploy
   before phase 2.
3. Open the upstream PRs from §6.
4. Update the documents that still describe the embedded SDK: `CLAUDE.md`, `AGENTS.md`
   §4–§5 (commands, repository map, baseline), `docs/architecture.md` §4, the KI entries
   that point to `sync/` and root `*.go` files (KI-09, KI-15, KI-16, KI-18, KI-20,
   KI-21, KI-22, KI-23, KI-25, KI-26), and the CI workflow (KI-15).
5. Build the Docker image once (C9) and run the server against the sandbox account
   only (AGENTS.md rule 1), starting from an empty data directory (B5).
