# Bounded MCP reads — verification and migration

For maintainers and MCP client authors, 2026-11-14. Contract: [ADR-0008](../adr/0008-bounded-mcp-reads.md).
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

CI supports manual dispatch for checking an exact feature-branch commit when a PR
event does not schedule checks. It runs the same secret-free jobs.

## Live verification gate

Run the existing sandbox workflow on the PR branch, using only its configured
sandbox credentials. Record the run URL and which account-dependent steps ran.
In a read-only ChatGPT sandbox connection, verify page completeness metadata,
continuation, normal detail, text chunks and recovery from a changed revision.
Confirm read-only credentials do not expose write tools. Record limitations rather
than substituting a local HTTP test for a ChatGPT check.

Live Things Cloud: pending. The GitHub `things-sandbox` environment currently allows
protected branches only. Do not weaken this policy to run a feature branch.
ChatGPT: pending. Production: unchanged.

## Rollback gate

Before production rollout, record the currently deployed image digest. Test rolling
back on disposable sandbox state to that exact image, reconnect the MCP client and
restore the previous schemas/instructions. There are no mirror-schema or cloud-wire
changes in this feature. Rollback behavior on a real deployment remains pending.
Merge, release tagging and production rollout require the owner's instruction.
