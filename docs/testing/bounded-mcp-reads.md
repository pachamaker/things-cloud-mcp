# Bounded MCP reads — verification and migration

For maintainers and MCP client authors, updated 2026-11-15. Contract: [ADR-0008](../adr/0008-bounded-mcp-reads.md).
This branch targets the next major release after v3.0.0. Its release number is unassigned.

## Migration

Every `things_list_*` tool and `things_search_tasks` returns a page object instead of
an array. Read `items` for this page, `total` for the whole filtered selection, and
`returned` for this page's size. No note text is included in collection cards.

```json
{"items":[{"uuid":"synthetic-id","title":"Review estimate","status":"open","type":"task","has_note":true}],"returned":1,"total":1,"has_more":false,"next_offset":null,"revision":"opaque-value","today":"2026-11-14","timezone":"Europe/Moscow","page_limited_by":null}
```

The default is 25 items. Explicit `limit` must be an integer from 1 through 100.
Explicit zero is an error. Pagination applies to search, completed tasks and
checklist items as well as open lists, projects, areas and tags. Area/tag catalogs
use UUID order because the SDK does not expose their stored index. Task lists
preserve view/group order with UUID tie-breakers. Search uses UUID order.

1. Make the first request with no offset/revision or `offset=0`.
2. If more results are needed and `has_more=true`, request `offset=next_offset`
   and copy the returned `revision`. Keep the same tool and filters. Page size may change.
3. On `stale_page`, discard earlier pages for that iteration and restart at zero.
   Another user's edit, server write, sync/rebuild, restart, or owner-day rollover
   can invalidate a revision. A no-op sync does not. There is no time-based expiry.
4. Call `things_get_task` when task detail matters. Normal-sized detail still has
   the full note. An oversized detail explicitly lists `truncated_fields` and
   explains how to use `things_get_task_text`.
5. For complete title/note text, call `things_get_task_text` with `uuid` and
   `field=note|title`. Chunk limit defaults to 4000 Unicode code points, maximum
   8000. Continue using its `next_offset` and `revision`. Do not reuse a list's
   revision for text retrieval. Never overwrite a stored field using a preview.

Each read emits one compact JSON text block capped at 24576 UTF-8 bytes, including
metadata and escaping. A page can end early with `page_limited_by="bytes"`.
The ceiling does not specify a token count or bound the whole conversation.
`item_too_large` errors identify the UUID and field when unsupported metadata
cannot fit even alone. Inspect that entity in Things. Raw field content is omitted.

Search adds `scope=open|completed|all`, default `open`, and still matches title and
note substrings without returning the note. Here `all` is open + completed. Canceled,
trashed, inherited-trash and hidden recurrence templates are excluded. Completed
lists retain inclusive `completed_after` / exclusive `completed_before` boundaries.

Refresh/reconnect the MCP client after upgrading so it discovers the new schemas.
Update scripts from `result[]` to `result.items[]` and stop interpreting a default
page as the complete list. Assistant instructions should request more pages only
when the user's request needs them. A full review requires all necessary pages.

## Offline verification

```sh
go mod tidy -diff
go build -o /dev/null ./...
go vet ./...
go test -race ./...
go test -tags e2e ./server -run '^$'
go test -tags 'e2e fakecloud' -run '^TestSandbox' -count=1 ./server
go test ./server -run '^$' -bench BenchmarkReadPayload -benchtime=1x -benchmem
```

Synthetic HTTP tests cover all 14 collections and four detail/text tools, complete
247/10000-item walks, stale revisions, coherent reads, strict numeric parameters,
byte-limited pages, metadata errors and Unicode text reconstruction. The fake-cloud
harness passed locally (70.16 seconds) and exercises the registered tools against a local Things protocol stand-in.
It is a separate check from an actual Things Cloud account or ChatGPT connection.

Main now includes [PR #46](https://github.com/pachamaker/things-cloud-mcp/pull/46),
which upgrades the Go toolchain and pinned Docker builder to 1.26.11. This resolves
the previous GO-2026-6859 scan finding (KI-44). Local checks are rerun on the
merged branch; the results below distinguish them from GitHub CI and live gates.

Reverification on 2026-11-15 after merging main (`0252cec`):

- Go 1.26.11: `go mod tidy -diff`, build, vet and full race tests passed.
- `staticcheck@2026.2.1 -tags 'e2e fakecloud'` passed after removing the unused
  `syncForRead` helper; reads already synchronize through `withRead`.
- `govulncheck@v1.8.0` passed with zero reachable vulnerabilities. It still reports
  advisories in imported/required code without reachable calls.
- E2E-tagged tests compile. Offline fake-cloud sandbox tests passed (70.151 seconds).
- GitHub CI passed on `48ad69f` ([run 43699727145](https://github.com/pachamaker/things-cloud-mcp/actions/runs/43699727145)).
  Live sandbox results follow below; ChatGPT and real deployment rollback remain unverified.


### Serialized payload measurement

Measured locally on 2026-11-14 with synthetic tasks, each containing a 1500-byte
ASCII note. Exact payload bytes from `BenchmarkReadPayload`, one iteration. Values
exclude MCP framing. No tokenizer was used. Do not extrapolate to personal notes.

| Fixture | Legacy full indented list | New first page (25 summaries) | All summary pages combined |
|---|---:|---:|---:|
| 247 tasks | 427,065 B | 2,687 B | 26,579 B |
| 10,000 tasks | 17,290,002 B | 2,689 B | 1,076,356 B |

These measurements isolate serialization. Query cost, mirror size and initial sync
are not reduced by this contract. Full HTTP traversal correctness is tested, while
SQLite query-performance tuning remains separate. Microbenchmark timings from a
single iteration are unsuitable for latency guarantees.

## Live verification gate

Run the existing sandbox workflow on the PR branch, using only its configured
sandbox credentials. Record the run URL and which account-dependent steps ran.
In a read-only ChatGPT sandbox connection, verify page completeness metadata,
continuation, normal detail, text chunks and recovery from a changed revision.
Confirm read-only credentials do not expose write tools. Record limitations rather
than substituting a local HTTP test for a ChatGPT check.

### Live sandbox result — 2026-11-15

[Workflow run 43700227573](https://github.com/pachamaker/things-cloud-mcp/actions/runs/43700227573)
completed successfully on PR #45 commit `48ad69f4dbbfbe8e000e01f725f8019ed054269c`.
Run identifier: `e2e-gh43700227573-1`; server day: 2026-11-15, UTC.

| Step | Result | Duration |
|---|---|---:|
| Offline harness self-test | ✅ | 70.453 s |
| Live `TestSandboxE2E` | ✅ | 347.149 s |
| Read-only `TestSandboxSeedChecks` | ✅ | 71.396 s |
| `TestSandboxCleanupLeftovers` | ✅ | 33.733 s |

All 12 live subtests passed: setup, placement, edit, lifecycle, checklist,
repeat rejection, read tools, conflict 409, independent observer mirror, wire,
cleanup and tool coverage. The wire check verified 41 committed items in history
(1 Area3, 3 ChecklistItem3, 1 Tag4, 35 Task6 and 1 Tombstone2). The live test made
308 cloud requests, including 56 posted commits and one tested 409; no GET retry
for a 429 was needed. These are harness observations, not production benchmarks.

Only the `things-sandbox` environment's `THINGS_SANDBOX_*` credentials were supplied.
The harness passed its sandbox marker guard before writes and constrained writes to
run-owned objects. No personal connector, VPS or personal Things credentials were used.
The environment policy was not changed for this run; GitHub admitted the feature branch.

Cleanup trashed 14 objects with zero failures and verified generated Task6 objects
were in the trash. The final leftover cleanup passed. Cleanup is not complete deletion:
this run's area `TQ6KwX6zfDtXqtbvgSuRqA` and tag `Hf4eFkS3ntgMKwoawGDXZy` remain,
because the harness has no verified area/tag delete operation. They are identifiable
by the `[e2e-gh43700227573-1]` title prefix and can be removed in the sandbox app.

This verifies the registered MCP tools against real Things Cloud through the test
harness. Large-page/byte-budget edge cases remain covered by synthetic tests; this
run does not establish a ChatGPT integration or deployment rollback result.
### ChatGPT personal-account read verification — 2026-11-15

The owner explicitly authorized a personal deployment and read/write smoke test
instead of a separate ChatGPT sandbox deployment. The deployed build is
`pr45-b18a12b` (source `b18a12b71bd4490b996249001f8d4348f443fa1e`).
The sandbox workflow above remains a separate test on sandbox credentials.

The owner refreshed tools and tested in a new ChatGPT conversation, then supplied
these results. The maintainer did not inspect that conversation's raw trace.
No personal task titles, notes, UUIDs or revision tokens are included here.

| ChatGPT check | Result |
|---|---|
| Updated schema discovery | ✅ `things_get_task_text` and `revision` available |
| Today first page, `limit=2` | ✅ Two items, continuation available |
| Next page using returned offset/revision | ✅ Two more items, no duplicate UUIDs |
| Task detail | ✅ One selected task read successfully |
| Title text, `limit=5` | ✅ Reconstructed in 13 calls, matched detail exactly |
| Writes during this read-only conversation | None reported |
| Naturally invalidated revision | Not observed |

The owner reported `INVALID_ARGUMENT` for deliberately invalid revisions.
A maintainer reproduced the response through the connected Things Cloud app in
Codex, including a 64-character revision with exactly one character changed:

```json
{"content":[{"type":"text","text":"{\"error\":{\"code\":\"stale_page\",\"message\":\"pre-read sync or query failed: stale_page: data or query changed; restart at offset zero and replace previous results\"}}"}],"structuredContent":{"error_code":"INVALID_ARGUMENT"},"isError":true}
```

These are two different response layers. The MCP server emits `stale_page` in its
JSON text error payload. The connected app also presents a generic
`structuredContent.error_code=INVALID_ARGUMENT`; its precise mapping mechanism is
not established by this test. Do not classify the domain error solely from that
generic code: inspect `content[].text` and its `error.code` too.

The maintainer then restarted at offset zero and successfully fetched the next
page with the new revision, without duplicate UUIDs. This confirms explicit bad
revision rejection and manual recovery through the Codex app. It does not prove
ChatGPT automatically recovers after a naturally occurring sync/day change.

### Personal deployment write smoke — separate from ChatGPT read verification

Through the connected Things Cloud app in Codex, the maintainer created one
synthetic Inbox QA task, read it back, edited its title/note and moved it to Today,
then read back the changes and confirmed Today membership. The task was trashed;
search no longer returned it and a read-only VPS mirror query confirmed
`in_trash=1`. Server logs showed successful create/edit/trash commits. No existing
personal tasks were modified. This is a Codex connector smoke, not a ChatGPT UI
write test.

The personal deployment has writes enabled and OAuth protection intact. The old
image/configuration and a stopped-container mirror snapshot were retained for
rollback. Real deployment rollback remains untested.

## Rollback gate

Before production rollout, record the currently deployed image digest. Test rolling
back on disposable sandbox state to that exact image, reconnect the MCP client and
restore the previous schemas/instructions. There are no mirror-schema or cloud-wire
changes in this feature. Rollback behavior on a real deployment remains pending.
Merge, release tagging and production rollout require the owner's instruction.
