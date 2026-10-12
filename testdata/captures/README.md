# Capture fixtures

What the official Things apps wrote to Things Cloud, sanitized: the evidence
`docs/protocol/things-cloud.md` §8 and AGENTS.md rule 2 require before the server may
write a field or value. A fixture is evidence, not permission: recurrence writes stay
forbidden (KI-01, ADR-0004) until an ADR, an encoder tested against these files and a
Mac + iOS round trip on the sandbox account exist.

## Format

One JSON file per capture, `<feature>-<date>.json`:

| Field | Meaning |
|---|---|
| `source`, `captured_on` | how and when (owner's day) it was read |
| `history_head` | the history head index at the time; `items[].index` are slots below it |
| `objects` | captured objects with their role |
| `items` | every history item of those objects, in history order: `index` (slot = one commit), `slot_items` (entities in that commit, captured or not), `uuid`, `role`, `t` (action), `e` (entity kind), `p` (payload, as stored) |
| `redacted` | paths of values the sanitizer replaced |

Sanitizing (`sanitizeCaptureValue`, `server/sandbox_rules_test.go`) keeps numbers,
booleans, `null`, empty strings, `_t` type tags, seed titles and the IDs of captured
objects and seeds. Any other ID becomes `<other id>`, any other string
`<redacted N chars>`, with its path listed in `redacted`. IDs are the sandbox account's
real ones; the harness prints them in CI logs anyway.

## `recurrence-seeds-2026-10-11.json`

Seeds 12 and 13 of `docs/testing/sandbox-e2e.md`: `SEED repeat weekly` (every week,
fixed schedule) and `SEED repeat monthly after completion`.

- **Source:** the sandbox account's history, read back by `TestSandboxRecurrenceCapture`
  (read-only). History keeps the payload a client committed and which items were one
  commit; it keeps no HTTP headers, client version or device.
- **Written:** by the owner in a Things app on 2026-10-11, 12:25:00–12:25:34 UTC
  (`cd`/`md`). Things version and device are not recorded; `Task7` means Things 3.23 or
  later (current that day: Mac 3.24.1, iPhone 3.24.2).
- **Captured:** 2026-10-11, history head 438. Nothing needed redacting.

Days are civil dates as midnight UTC (§4): 2026-10-11 (a Sunday) is `1791676800`,
2026-10-12 `1791763200`, 2026-10-18 `1792281600`; `64092211200` is 4001-01-01.

| `index` | Object | `t` | What it is |
|---|---|---|---|
| 280 | weekly to-do `R2db…` | create | a plain to-do in the area `MCP E2E SANDBOX`, Anytime (`st 1`, no date), `rr null`, `rt []` |
| 283 | weekly template `KAee…` | create | new hidden template: weekly fixed rule on Sunday, `st 2`, `sr null`, `tir` = `icsd` = 2026-10-18 (next occurrence), `icc 1`, the to-do's `ix` |
| 283 | weekly to-do | modify | becomes instance 1: `rt [template]`, `sr` = `tir` = 2026-10-11 (Today), new `ix` |
| 285 | weekly template | modify | `ti` only |
| 285 | monthly to-do `Tk2u…` | create | a plain to-do dated 2026-10-18 (`st 2`, `sr` = `tir` = 18 Oct), `rr null`, `rt []`, presumably made in Upcoming under that date |
| 287 | monthly template `DRmr…` | create | new hidden template: rule "every week, after completion" (`fu 256`, `tp 1`, `ia 0`, `of []`), `st 2`, `sr null`, `tir null`, `icsd` 2026-10-12, `icc 1` |
| 287 | monthly to-do | modify | becomes instance 1: `rt [template]`, `st 1`, `sr` = `tir` = 2026-10-11 (Today), new `ix`, `ti` |
| 290 | monthly template | modify | the whole `rr` again with `fu 8` (month) instead of `256`, and `md`; `icsd`/`icc` untouched |

`server/recurrence_capture_test.go` pins these facts and diffs the server's builders
against them. What the capture answers and what it does not: `docs/protocol/things-cloud.md`
§5.3.

Not in this capture (needs the app to do it on the sandbox, then a new read):
an instance the app generates on its own (the next weekly one is due 2026-10-18),
completing an instance (both rule kinds), editing an instance or a template (title,
when, notes, rule), ending or deleting a repetition, fixed monthly/yearly rules
(`of` with `dy`/`mo`/`wdo`), `fa` > 1, an end date or count, daily and yearly units, and
reminders (`ato`).
