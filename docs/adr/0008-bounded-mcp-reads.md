# ADR-0008: Bounded MCP reads and explicit pagination

## Status

Accepted (2026-11-14). Owner approved the contract and proposed limits.
Implementation planning is authorized. Implementation has not started.
Addresses KI-19 and the unbounded task-list output documented below.
Baseline inspected: `a7a205e` (`v3.0.0`). No production measurements were run
while preparing this proposal.

If accepted, this ADR supersedes the list-output and pagination portions of
ADR-0006 decision 2. Other parts of ADR-0006 remain separate proposals.

## Context

`server/mcp.go` defaults open lists to `limit=0`, which `paginate` interprets as
unlimited. All task lists include full notes through `formatTask`, and serialize
indented JSON arrays without completeness metadata. Search scans titles and notes
of open tasks and returns every match. Completed tasks default to 50 and support a
completion-date range, with no offset. A large response can exhaust an assistant's
context even when sync and the local mirror are healthy.

The owner reported approximately 63k tokens for 247 Anytime tasks and 2.2k tokens
for 10. These are user-supplied observations, not reproduced benchmark results.
Token counts depend on the model tokenizer and task contents.

The server mirrors the owner's history locally. Bounding MCP responses does not
bound the mirror's size, initial sync cost, query memory, or the accumulated context
of a chat that explicitly requests every page.

## Options

| Option | Benefit | Trade-off |
|---|---|---|
| Add a default item limit only | Small patch | Notes remain unbounded, completeness is ambiguous, and search/history stay inconsistent |
| **Bounded summaries, explicit pages, detail on demand** | Predictable responses with a small implementation surface | Breaking output contract, additional detail calls |
| Snapshot cursors, a new query engine, and server-generated triage summaries | Stable long-running exports and richer selection | Snapshot lifecycle and storage costs, substantially larger scope |

## Decision

Adopt bounded summaries and explicit pages in a new major release. Start with
existing tool names and query semantics. Add one text-detail tool. Counts and
additional selection filters are a follow-up, with independently bounded output.
The values below are the approved defaults.

### 1. Common request contract

All collection reads accept optional `revision` for continuation and use `limit` (default 25, integer 1..100) and `offset` (default
0, non-negative integer). Reject explicit zero, negative, fractional, non-finite,
wrong-type, or out-of-range input. Omission is the only way to select the default.
Validate in handlers as well as tool schemas. Never interpret zero as unlimited.
Use overflow-safe comparisons for offsets and page arithmetic.

Apply this contract to task views, all tasks, project/area tasks, projects, areas,
tags, completed tasks, search, and checklist items. None may bypass the response
budget. Read-only OAuth scopes and write-safety checks retain their current meaning.

`things_search_tasks` adds `scope=open|completed|all` (default `open`). Search title
and note contents before paging. Return summary cards even for matches inside a
note. Reject a blank/whitespace-only query. Preserve case-insensitive substring
semantics. `completed` means status completed, matching `things_list_completed`.
Canceled items are not silently added to the completed scope in this change.
`all` means the union of these two scopes. Trash and recurrence templates remain
excluded, including inherited trash. Actual recurring instances remain readable.

Completed lists retain `[completed_after, completed_before)` and add common paging.
Bare dates use the owner's timezone as today. Invalid ranges fail explicitly.

### 2. Collection response

```json
{
  "items": [
    {
      "uuid": "synthetic-task-id",
      "title": "Send the revised estimate",
      "status": "open",
      "type": "task",
      "has_note": true
    }
  ],
  "returned": 1,
  "total": 247,
  "has_more": true,
  "next_offset": 1,
  "revision": "opaque-read-revision",
  "today": "2026-11-14",
  "timezone": "Europe/Moscow",
  "page_limited_by": "bytes"
}
```

The example represents a byte-limited page. `total` is the count after all scope,
visibility, date, and query filters and before pagination. `returned` is the number
actually emitted. `next_offset` is `offset + returned` when more matches exist,
otherwise null. An empty query result or an offset beyond the end returns an empty
page, the correct total, `has_more=false`, and `next_offset=null`.
`page_limited_by` is `limit`, `bytes`, or null on an exhaustive page.

Task cards retain UUID, status, type, scheduled date, deadline, completion time,
project/area/heading IDs, heading title, tags, recurrence-instance marker, and
Today evening flag where applicable. Omit absent optional values. Replace full
`note` with `has_note`. Checklist items and area/tag cards keep their own domain
fields within the same page envelope. No task's notes are sent automatically from
any collection, including project lists, search, and completed tasks.

### 3. Pagination consistency

Filter, order, count, paginate, project, and enforce bytes in that order, against
one consistent read revision. Sync once before acquiring that read. Do not mix
states from separate syncs while building a response.

Preserve existing view ordering, including This Evening last. Establish explicit
ordering for every collection and use UUID as the final unique tie-breaker.
Completed tasks sort by completion time descending, then UUID ascending.
Search uses a documented deterministic order, initially UUID ascending.

The first page returns an opaque `revision`. Continuation requests at `offset>0`
must include it. Bind it to the effective query, ordering, owner civil day, and
mirror generation/history revision. Page offsets and page-size limits are excluded
from query identity, so a caller may request a smaller next page. Reject a different query, expired/different
revision, or revision lost on restart with a structured `stale_page` tool error.
Tell callers to restart at offset zero and replace earlier results rather than
append to them. Set MCP `isError=true` and return compact JSON with
`error.code="stale_page"` and `error.message` explaining the restart. There is no
wall-clock TTL. The revision remains valid while its bound state is unchanged.
Detail responses do not invalidate a page merely by being read.

This is detection of changed data, not a retained snapshot. Writes, external sync
changes, a mirror rebuild, and owner-day rollover may require restarting an
iteration. A guaranteed export under continuous mutation would require the
separate snapshot-cursor option. Unchanged data must remain pageable beyond the
sync gate's reuse window.

### 4. Response size and oversized fields

Cap the complete compact UTF-8 JSON text emitted by each read tool at **24 KiB**,
including metadata and escaped characters. Check the encoded result, not string
length before JSON encoding. Return one JSON text content block. Avoid a second
full copy of the payload in structured content. This bounds our emitted text,
not client-specific framing or an exact tokenizer-dependent token count.

The byte limit may end a page before its requested item limit. Calculate the next
offset from emitted items, so omitted records are returned on the following page.
Metadata and error responses also fit the budget. No successful response may
silently omit matches or return an empty page with more results available.

For a task that cannot fit even alone because of its title, shorten the title on
Unicode code-point boundaries, mark `title` in `truncated_fields`, and expose the
text retrieval tool below. Never shorten identifiers, dates, or status values.
If any other record or required metadata cannot fit, return an explicit
`item_too_large` tool error identifying the entity UUID and field. Do not silently
omit tags, checklist data, or non-task titles. Tell the caller to inspect that
entity in Things. General paging of oversized metadata fields is deferred.
Resource limits must not change stored data or create a non-progress loop.

### 5. Detail reads

`things_get_task(uuid)` retains full normal-sized titles and notes. If its result
would exceed the budget, return the bounded task card, `truncated_fields`, note
length, and explicit detail instructions. If metadata itself exceeds the budget,
return `item_too_large` rather than an incomplete tag array. Clients must never interpret
a shortened field as the complete stored value or overwrite notes from a preview.

Add `things_get_task_text(uuid, field=note|title, offset=0, limit=4000, revision?)`.
Offsets and lengths count Unicode code points. Default 4000, maximum 8000.
Return `uuid`, `field`, `text`, `total_characters`, `returned_characters`,
`next_offset`, `has_more`, and `revision`. Enforce the same encoded byte budget,
shortening a chunk further if required. Continuations require the matching revision.
An empty field is a successful empty result. Unknown UUIDs return the existing
not-found tool error. No private text or tokens enter logs or committed fixtures.

### 6. Assistant workflow

Tool descriptions must state default/max limits, summary-note behavior, pagination,
and stale-page recovery. Explain that `total` describes the full selection and
`returned` describes this page. Fetch more pages when the user's request needs them.
Retrieve notes only for tasks whose detail matters to the request.

A full 247-task review still requires reading 247 summaries. Page limits cannot
force the host to summarize earlier messages or prevent it from accumulating all
responses. Read-only scheduled triage should select a useful scope and process
bounded batches. Follow-up counts/filters can reduce unnecessary collection reads.

## Compatibility and release

Changing arrays to envelopes, omitting notes from collections, changing unlimited
zero to an error, and requiring revisions for continuation are breaking changes.
Ship together in a new major version after v3.0.0. Publish before/after examples
and migration guidance for consumers and personal assistant instructions. Reconnect
or refresh tool schemas in clients after upgrading. Do not advertise a release
number until it is chosen.

Keep one active contract per deployment. A user requiring old output can pin the
previous image while migrating. Operational rollback must use that exact image
and must be tested without losing mirror compatibility. This proposal requires
no Things Cloud wire changes, account mutations, or persistent schema migration.

## Acceptance criteria

- Exercise every read tool through its MCP entry point with synthetic data. Every
  success and error JSON text is <=24 KiB, valid UTF-8 and valid JSON.
- Omitted, zero, negative, fractional, huge, and wrong-type pagination arguments
  follow the declared contract. Verify offsets beyond the end and empty results.
- On unchanged data, walk every page of 247 and 10,000 synthetic tasks and obtain
  each matching UUID exactly once. Exercise equal completion dates and every view's
  ordering, especially This Evening and heading membership.
- A byte-shortened page continues at the first unreturned record. Oversized notes,
  titles, non-ASCII text, escapes, and large tag arrays neither silently lose data
  nor loop. Unsupported oversized metadata produces `item_too_large`. Text chunks
  reconstruct original task titles and notes exactly.
- Search includes matches beyond page one and inside omitted notes. All scopes
  preserve trash/template exclusions. Completion-date boundaries stay correct.
- Mid-pagination mutation, a mismatched query, rebuild, restart, and day rollover
  produce `stale_page`. Unchanged data survives ordinary subsequent sync checks.
- Default list responses contain no note text. Normal detail remains complete.
  Clients can distinguish truncated task titles/notes and retrieve their complete
  text. Other oversized entities fail explicitly with an inspection instruction.
- Benchmark serialized bytes before/after on the same synthetic fixtures. Token
  measurements name their tokenizer and are informative rather than guarantees.
- Run existing build, vet, race tests, and synthetic MCP regression tests. Separately
  verify a read-only ChatGPT workflow on the sandbox account, including continuation
  and detail retrieval. Record any unsupported client behavior explicitly.

## Consequences

Default reads become bounded and completeness becomes explicit. Large notes remain
retrievable on demand. More calls are needed for detailed reviews. Revision checks
favor detecting inconsistency over silently skipping tasks during concurrent edits.
Response bounds do not remove full-history query costs in the current read model.
Measure those costs separately before adding SQL search or indexing work.
