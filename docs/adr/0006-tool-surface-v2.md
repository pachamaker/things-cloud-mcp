# ADR-0006: MCP tool surface v2

## Status
Proposed (2026-10-10). Addresses KI-06, KI-12, KI-14, KI-24.

## Context
- 36 narrow tools, UUID-only parameters, sparse output. A morning review takes many
  calls (`list_today` + `list_upcoming` + `list_projects` + `list_tags` to translate
  IDs), each paying a sync round trip.
- Output omits the task's view, evening flag, heading, project/area/tag names,
  checklist progress, repeating flag, created/modified dates.
- Write tools return only `{status, uuid}`; there is no batch, so multi-step edits are
  slow and partially applied on failure.
- Defaults from `mcp-go` mark every write as destructive, so clients ask for
  confirmation on harmless moves.
- Claude and ChatGPT both connect to the same server; ChatGPT's connector modes may
  require specific tools (see ADR-0002 research notes).

## Decision
1. **Names in, names out.** Parameters accept UUID *or* exact title for project, area,
   heading and tags (case-insensitive; ambiguous or unknown → error listing
   candidates). Tags are never auto-created.
2. **Enriched task view** (structured output with an output schema, plus a compact text
   rendering):
   `uuid, title, notes, type, status, view, evening, when, deadline, project{uuid,title},
   heading{uuid,title}, area{uuid,title}, tags[{uuid,title}], checklist{done,total},
   repeating, created_at, modified_at, completed_at`.
   List responses add `today`, `timezone`, `count`, `next_offset`.
3. **Consolidated read tools** (thin aliases may stay for compatibility during
   migration):
   - `things_list(view, project?, area?, tag?, limit?, offset?)` where `view ∈
     inbox|today|upcoming|anytime|someday|logbook`,
   - `things_get(uuid)` (any entity, with children summary),
   - `things_search(query, include_logbook?, limit?)`,
   - `things_catalog()` → areas, projects, headings, tags in one call.
4. **Batch writes.** `things_batch(ops[])` with ops `create|update|move|complete|reopen|
   trash|untrash|checklist_add|checklist_complete`. Preconditions for all ops run
   first; then **one** commit; then per-op results with resulting entities. Single
   write tools become one-op wrappers over the same path.
5. **Annotations.** Read tools `readOnlyHint: true`. Create/update/move/complete:
   `destructiveHint: false`; moves and complete/reopen `idempotentHint: true`.
   Trash and delete: `destructiveHint: true`.
6. **Errors are actionable.** Every tool error states what was wrong and what to call
   next (e.g. "project 'Hom' not found; did you mean 'Home' (uuid …)?").
7. **Server instructions.** Use the MCP `instructions` field to ship a short operating
   guide (timezone, view semantics, recurrence unsupported), replacing per-user prompt
   workarounds.
8. Remove `things_smoke_test` from the surface (ADR-0004).

## Consequences
- Easier: typical requests take 1–2 calls; assistants stop guessing IDs; partial
  failure disappears for batched edits.
- Harder: breaking change for existing prompts/skills — ship as `v2.0.0` and keep the
  old tool names as deprecated aliases for one minor release.
- Token budget: enriched output is larger per item; mitigated by default `limit`
  and compact text rendering.
