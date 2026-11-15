# Bounded MCP Reads Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task in the current session. Delegate only with explicit authorization. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement ADR-0008 so every read has bounded output, collection completeness is explicit, and oversized task text remains retrievable.

**Architecture:** Keep the existing MCP tool names and domain classifiers. Add shared argument validation, bounded serialization, and revision-aware collection responses at the server boundary. Collect each result under the sync gate against one stable mirror state, then release the gate before serialization and transport.

**Tech Stack:** Go, existing mcp-go v0.44.0, existing Things SDK soft fork, SQLite mirror, standard-library JSON/crypto, existing offline and sandbox harnesses.

**Spec:** `docs/adr/0008-bounded-mcp-reads.md` (Accepted, 2026-11-14).

## Global Constraints

- Collection default 25, maximum 100, minimum 1. Offset is a non-negative integer.
- Each read success/error has one compact JSON text block <=24576 UTF-8 bytes.
- Task-text chunks default 4000, maximum 8000 Unicode code points, minimum 1.
- Collection notes become `has_note`. Normal task detail retains full notes.
- Every continuation at offset >0 requires a revision. No wall-clock TTL.
- Revision binds tool, effective filters, ordering, owner day/timezone, process and mirror identity. Exclude offset, limit and the supplied revision itself.
- Preserve civil-date, view, heading, inherited-trash, recurrence-read and OAuth rules.
- Use synthetic offline fixtures. Live checks target the sandbox account only.
- SDK source, dependencies, credentials, migrations and cloud write payloads need no change.
- This is a major read-contract change. No unlimited compatibility mode. Release number remains unassigned.
- The plan authorizes no merge, production deployment, or release publication.

## Review Focus

1. JSON escapes and multi-byte text can exceed the byte cap despite a short string. Tasks 1 and 5 test encoded lengths and Unicode reconstruction.
2. A background mirror rebuild can close a database while collecting parent/heading data. Task 2 tests coherent capture and rebuild exclusion.
3. A fractional or overflowing MCP number can silently become an integer through GetInt. Task 1 tests raw argument validation.
4. Equal sort keys and byte-shortened pages can duplicate/skip items. Tasks 3 and 6 walk complete collections.
5. Old harness decoders and prompts assume arrays/unlimited results. Tasks 4 and 6 update consumers explicitly and detect accidental whole-list traversal.

## File map

| File | Responsibility |
|---|---|
| `server/mcp_read_contract.go` (new) | Page/text options, envelope, bounded encoder and read-error JSON |
| `server/mcp_read_revision.go` (new) | Canonical query identity and opaque revision generation |
| `server/syncgate.go`, `server/mirror.go` | Coherent local read and mirror generation |
| `server/readmodel.go`, `server/views.go` | Complete filtered selections and deterministic ordering |
| `server/mcp.go` | Existing handler integration and tool schemas/descriptions |
| `server/mcp_task_text.go` (new) | Task title/note chunks and bounded detail fallback |
| Adjacent `*_test.go` files | Offline regressions for each component |
| `server/sandbox_harness_e2e_test.go`, `server/sandbox_*_test.go` | Envelope-aware test consumers and live sandbox assertions |
| `tests/test-mcp*.sh` | Update any array/pagination assumptions in scripts |
| `docs/testing/bounded-mcp-reads.md` (new) | Reproducible measurements and sandbox/client verification procedure |
| `README.md`, `docs/known-issues.md`, `docs/adr/0006-tool-surface-v2.md` | New surface, KI-19 status, supersession notice |

Use this branch/worktree. Before execution, compare origin/main with baseline a7a205e and accommodate intervening changes. Run baseline offline checks before product edits. Commit each completed task with only its named files.

## Task 1 — Contract primitives and byte accounting

**Files:** Create `server/mcp_read_contract.go` and `server/mcp_read_contract_test.go`.

**Interfaces to produce**

- `pageOptions{Limit int, Offset int, Revision string}`.
- `parsePageOptions(mcp.CallToolRequest) (pageOptions, error)` and `parseTextOptions(mcp.CallToolRequest) (pageOptions, error)` with different limits.
- `readPage[T any]` with exactly ADR fields. Null terminal offsets/reasons must encode as null, not vanish. Empty items encode as `[]`.
- `boundedReadResult(v any) *mcp.CallToolResult` marshals compact JSON once, enforces the cap, and emits a bounded structured error on encoding/size failure.
- `readError(code, message string) *mcp.CallToolResult` sets IsError and encodes `{error:{code,message}}`. Cap externally derived error text before encoding and never echo task notes/query contents.
- `encodeCollectionPage[T any](items []T, opts pageOptions, revision string, today civil.Date, timezone string) *mcp.CallToolResult`. Input is the complete filtered, sorted and projected selection. Append only a fitting prefix, account for final metadata, calculate total before paging, and advance by emitted count.

- [ ] Write failing `TestReadArgumentsStrict` covering omission=25/0, explicit zero, negative, fractional, non-finite, string/bool/null, huge values, maximum 100, missing revision on continuation. Test omitted text limit=4000 and max=8000. Validate raw numeric values rather than using GetInt coercion.
- [ ] Write failing `TestReadEnvelopeByteBudget` covering len(text)<=24576, one text block, no second structured payload, UTF-8/JSON validity, escaped quotes/control characters, metadata growth at integer boundaries, empty page, beyond-end offset, item cap, byte cap and exhaustive page.
- [ ] Run `go test ./server -run 'TestReadArgumentsStrict|TestReadEnvelopeByteBudget' -count=1`. Confirm new tests fail before implementing helpers.
- [ ] Implement the interfaces. On a non-fitting first record produce `item_too_large`, never an empty successful continuation. Task 5 adds task-title fallback before this encoder.
- [ ] Rerun targeted tests and existing serialization-error test. Commit `feat: add bounded MCP read contract primitives`.

## Task 2 — Coherent reads and revision lifecycle

**Files:** Create `server/mcp_read_revision.go`, `server/mcp_read_revision_test.go`. Modify `server/syncgate.go`, `server/mirror.go`. Add tests in `server/syncgate_history_test.go`.

**Interfaces to produce**

- `readQuery{Tool string, Filters map[string]string, Order string}` populated from validated, effective request values.
- `readRevisionState{Identity mirrorIdentity, Generation uint64, Today civil.Date, Timezone string}`.
- `makeReadRevision(state readRevisionState, query readQuery) (string, error)`. Use HMAC-SHA256 with a random per-process secret and deterministic canonical encoding. Encode the digest only. No history key, query text or credentials appear in tokens/logs. Restart changes the secret.
- `(*syncGate).withRead(fn func(readModel, readRevisionState) error) error`. Sync before entry, then acquire g.mu, reject rebuilding/unavailable state, acquire current state and mirror identity, take ownerToday once, run the local collection callback, release. No nested sync calls or historyMu acquisition inside the callback. Return detached values for subsequent formatting.
- Increment mirror generation when the file is replaced. Reuse stored identity/cursor from mirror.go. A no-op sync must not change revision. Synthetic clocks/generation/identity sources make tests independent of wall time.

- [ ] Add failing `TestReadRevisionBinding` for different query/tool/scope/parent/order/day/timezone/index/generation/process secret, unchanged effective defaults, and excluded offset/limit. Invalid or mismatched token returns `stale_page` without attempting a page read.
- [ ] Add failing `TestCoherentReadDuringSyncAndRebuild` using channels to interleave local parent/heading queries with sync/rebuild. Assert no mixed state, closed-database access, deadlock, or cloud calls under the local-read callback.
- [ ] Run `go test -race ./server -run 'TestReadRevision|TestCoherentRead' -count=1` and confirm intended failures.
- [ ] Implement locking and revision helpers. Rebuild starts under g.mu and excludes reads until finished, including its asynchronous file replacement. Verify every mutation path of the local mirror is covered by this exclusion.
- [ ] Run new tests plus existing sync-gate/history tests under race detection. Commit `feat: bind read pages to coherent mirror revisions`.

## Task 3 — Full selections, search scope and stable ordering

**Files:** Modify `server/readmodel.go`, `server/views.go`. Add `server/read_selection_test.go`.

**Interfaces to produce**

- `(*readModel).selectTasks(query readQuery, today civil.Date) ([]*things.Task, error)` routes task collections to existing classification/membership logic and returns all visible matches before paging.
- `(*readModel).searchTasks(query string, scope string) ([]*things.Task, error)` searches titles/notes over the requested visible open/completed union.
- `(*readModel).completedTasks(after, before *time.Time) ([]*things.Task, error)` returns complete filtered completed selection with no hidden 50-item cap.
- Existing listOpts/query methods may remain for internal callers. MCP must avoid applying offset or default caps before calculating total or searching.

- [ ] Add failing `TestReadSelectionsCompleteBeforePaging` with matches after item 25 and 100, note-only search matches, both completion boundaries, and blank search rejection at the handler boundary.
- [ ] Add failing `TestReadSelectionVisibility` for canceled/completed distinctions, inherited trash, repeat templates/instances, and heading/project/area membership.
- [ ] Add failing `TestReadOrderingTieBreaks` covering equal completion times and every supported collection. Inspect existing SDK/view order keys and preserve primary behavior, including This Evening last, with UUID as final tie-breaker. Document exact chosen primary ordering in code comments where not already explicit.
- [ ] Run `go test ./server -run 'TestReadSelection|TestReadOrdering' -count=1`, implement minimal selection changes, then run existing readmodel/view/conformance unit tests.
- [ ] Commit `feat: select and order complete read collections before paging`.

## Task 4 — Wire all collection tools to summaries and pages

**Files:** Modify `server/mcp.go`. Add `server/mcp_read_contract_integration_test.go`. Update existing `server/mcp_format_test.go` where affected.

**Interfaces**

- Consume pageOptions, coherent-read callback, readQuery and encodeCollectionPage from Tasks 1–3.
- Add `taskSummaryOutput` with the approved metadata fields and `HasNote bool`. Keep `taskOutput` for normal detail. Do not project full notes then merely hide them in a second rendering.
- Register limit/offset/revision consistently for all 13 collections: Today, Anytime, Inbox, Someday, Upcoming, projects, areas, tags, all tasks, project tasks, area tasks, completed, and checklist items. Search is the 14th collection and adds scope.

- [ ] Write failing `TestEveryCollectionReadUsesEnvelope` through registered MCP tools, using synthetic state, for all 14 collections. Assert default 25, max 100, total, continuation, no note text, empty-array shape, visibility and scope-specific metadata.
- [ ] Write failing `TestMCPReadErrorsBounded` covering bad arguments and external error messages exceeding 24 KiB for every read entry point, including get_task/get_area/get_tag. JSON parse/transport errors outside a tool call are outside this payload contract.
- [ ] Run `go test ./server -run 'TestEveryCollectionRead|TestMCPReadErrors' -count=1` and confirm intended failures.
- [ ] Replace each read handler's sync/query/output path with the shared flow. Preserve evening and heading fields. Apply bounded errors to detail reads, without altering write handlers' contract.
- [ ] Update tool schemas and descriptions with limits, summary behavior, revision recovery, and the distinction between returned and total. Avoid instructions that automatically drain every page for a simple query.
- [ ] Rerun target tests and auth/read-scope tests. Commit `feat: expose bounded summary pages across MCP collections`.

## Task 5 — Task detail and Unicode text chunks

**Files:** Create `server/mcp_task_text.go`, `server/mcp_task_text_test.go`. Modify `server/mcp.go` and task-summary projection in Task 4.

**Interfaces to produce**

- `taskTextOutput` with uuid, field, text, total_characters, returned_characters, next_offset, has_more, revision.
- `mcpGetTaskText(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)`, registered as `things_get_task_text`, read-only. Uses strict text options and binds revision to UUID and note/title field.
- `boundedTaskDetail(task *things.Task, revision string) *mcp.CallToolResult` tries normal complete detail, then a marked summary with note length and retrieval instructions if needed. Keep error fallback explicit if non-text metadata alone is too large.
- Summary task titles may be shortened only when that task cannot fit alone. Mark `truncated_fields`. Heading titles and other non-task fields fail explicitly if oversized.

- [ ] Write failing `TestTaskDetailNormalAndOversized` for full normal notes, fallback without note leaks, title-only oversize, metadata-only oversize, missing task and unmodified stored data.
- [ ] Write failing `TestTaskTextChunkReconstruction` for empty text, non-ASCII/emoji/combining characters, quotes/control escapes, max limit and out-of-range offsets. Concatenate all chunks and assert exact original text with <=24576 encoded bytes per response.
- [ ] Write failing `TestTaskTextRevisionChanges` for task edit between chunks, field/UUID mismatch, missing revision and restart. Require positive progress whenever has_more=true.
- [ ] Run `go test ./server -run 'TestTaskDetail|TestTaskText' -count=1`, implement detail/text paths and schema, rerun under race detection.
- [ ] Commit `feat: read oversized task text in bounded Unicode chunks`.

## Task 6 — Contract regression, measurements and migration

**Files:** Add `server/mcp_read_walk_test.go`, `server/mcp_read_benchmark_test.go`, `docs/testing/bounded-mcp-reads.md`. Modify sandbox harness/consumers, read shell scripts, README, known issues and ADR-0006.

- [ ] Add `TestReadPageWalk247And10000` through normal MCP entry points, walking unchanged collections to exhaustion and asserting exact UUID set, no duplicates, correct totals, no notes and positive progress. Include byte-limited pages and equal sort keys. Add `TestReadWalkRestartsAfterMutation` to prove stale responses never append silently.
- [ ] Update sandbox listTasks decoding to envelopes with explicit page-walking only where a test needs the full set. Update completed/search/date boundary and auth tool-discovery assertions. Audit direct array decoding and calls assuming default unlimited data in all test files and scripts.
- [ ] Add `BenchmarkReadPayload` using identical synthetic 247/10000-item fixtures before/after. Record exact text bytes for legacy full lists, default summary pages, and full summary walks separately. Also measure query/capture/serialization time and allocations, including a long-note-heavy fixture. Report query costs independently from output savings.
- [ ] Write the verification guide with commands and blank result fields. Do not claim measured improvements until commands run. Optional token measurements must name tokenizer/version and remain distinct from the hard byte bound.
- [ ] Update README tool table, full request/response examples, text chunks, search scope, canceled/trash behavior and migration instructions for array consumers. Document exact-image rollback, schema refresh/reconnection and reverting prompts. Add an ADR-0006 notice that ADR-0008 supersedes its list fields/pagination, leaving unrelated proposals intact. Mark KI-19 fixed only when its implementation and checks pass.
- [ ] Run `go mod tidy -diff`, `go build -o /dev/null ./...`, `go vet ./...`, `go test -race ./...`, `go test ./server -run '^$' -bench BenchmarkReadPayload -benchmem` and `go test -tags e2e ./server -run '^$'` for sandbox compilation. All must succeed. Run the existing harness offline self-test as documented in sandbox-e2e.md.
- [ ] Inspect git status/diff and scan staged content for personal fixtures, secrets and unrelated changes. Commit `test: verify bounded reads and document major-version migration`.
- [ ] Push the branch, open one PR with Accepted ADR, migration notes, measured byte results and explicit verification limits, then follow CI to completion. Propose independent review of sync-gate/revision and pagination diff because race correctness and compatibility are the highest risks.

## Sandbox and release gates

- [ ] Before merge, run sandbox e2e against the dedicated account. Separately exercise read-only ChatGPT pagination, task detail/text chunks and stale-page recovery. Verify only read tools are exposed to read-only tokens. Write-capable sandbox harness coverage is a separate result.
- [ ] On the sandbox, test rollback to the exact previous image using compatible disposable state. Reconnect the client and restore the old schema/prompts. Record the tested image identifiers and outcome.
- [ ] Owner merges after CI/review. Tag/release and production rollout require the owner's instruction. Publish migration notes with the eventual major version and refresh clients' tool schemas.

## Self-review completed for this plan

Every ADR section maps to Tasks 1–6. The five review risks each have named tests.
The 24 KiB bound concerns the JSON text content, not transport framing or an exact
token count. Counts, extra selection filters, SQL search, retained snapshots, and
write-tool changes are deferred. No production benchmark or test has been run as
part of preparing this plan.
