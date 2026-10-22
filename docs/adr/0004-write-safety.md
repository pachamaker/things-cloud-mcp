# ADR-0004: Write safety — allowlisted mutations, preconditions, audit

## Status
Proposed (2026-10-10). Addresses KI-01, KI-08, KI-10, KI-11.
Implemented so far: §2 recurrence disabled (PR #2); §3 preconditions (phase 0, KI-10),
template and instance refusal on the SDK fork ([#34](https://github.com/pachamaker/things-cloud-mcp/pull/34),
KI-01); §6 debug endpoints and `things_smoke_test` removed (phase 0,
KI-08, KI-11); read-only unless `ENABLE_WRITES=true` (phase 0). Open: §1 allowlist with
capture fixtures, §4 read-back, §5 audit log, §6 annotations (KI-24).

## Context
- Things Cloud accepts any payload; a bad one breaks the official apps on every device,
  sometimes irreversibly (crash 2026-02-16, recurrence incident 2026-08-18).
- The server writes to the owner's only, real task database. There is no undo at the
  protocol level.
- The current write path validates input *shape* only. It does not check that the
  target exists, that it is the right kind of object, or that the resulting
  combination of fields is one the official client produces.
- Several mutations are guesses (recurrence, `sb`, headings via `agr`, tag/area
  deletion).

## Decision

### 1. Mutation allowlist
Each mutation the server can emit is a named encoder in `domain/wire` with:
- a capture fixture from the official client (`testdata/captures/…`),
- a unit test diffing our payload against the fixture (key set, types, null vs absent),
- a sandbox round-trip record (date, Things version, platforms checked).

No fixture → no encoder → the tool rejects the request with
`not supported yet: <feature>`. Initial allowlist = what is ✅ in
`docs/protocol/things-cloud.md` §5.1 and §6.

### 2. Recurrence is disabled
`repeat` is removed from tool schemas and rejected on REST until the allowlist has a
verified "create repeating to-do" encoder **and** the server stores `rr`/`rt`. Edits
never touch templates. Re-enabling requires a new ADR.

### 3. Preconditions against the mirror
Before encoding, every command:
1. syncs (freshness-bounded),
2. resolves every referenced UUID (target, project, heading, area, tags, parent) in the
   mirror: must exist, not be deleted, have the expected type,
3. refuses to modify repeating templates and trashed items (except untrash),
4. checks state transitions (e.g. complete only open items).

### 4. One commit per command, read back, return state
A command (single or batch) becomes one history commit. After commit the server syncs
and returns the resulting entity view. If the read-back differs from intent, the tool
returns an error describing the difference.

### 5. Audit log
Every commit is appended to `/data/audit.jsonl`:
`{ts, tool, request_id, history_id, ancestor_index, server_head_index, items}` (no
secrets). It is the manual-repair trail if something goes wrong.

### 6. Dangerous surfaces leave the server
- Remove `/api/debug/delete-history`, `/api/debug/nuke-account`,
  `/api/debug/confirm-account` from the server binary.
- Move `things_smoke_test` to a CLI/CI job that refuses to run unless the account email
  matches `THINGS_SANDBOX_ACCOUNT`.
- Destructive tools (`trash`, `delete_checklist_item`) keep `destructiveHint: true`;
  safe writes declare `destructiveHint: false`, and idempotent moves declare
  `idempotentHint: true`.

## Consequences
- Easier: a new feature has an explicit, reviewable bar; agents cannot "just add a
  field"; incidents become diagnosable from the audit log.
- Harder: every new write needs a capture session on a sandbox account; recurrence
  stays unavailable for now.
- Latency: preconditions add no network cost (they read the mirror after the sync the
  write needs anyway).
