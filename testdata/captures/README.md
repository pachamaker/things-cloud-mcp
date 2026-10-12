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
reminders (`ato`). Completions of both kinds and an app-generated instance (for an
occurrence completed ahead of time) are in `recurrence-completion-2026-10-11.json`.

## `recurrence-completion-2026-10-11.json`

The same two seeds, read again after the owner completed both instance 1s and, ahead of
time, the weekly occurrence of 2026-10-18 (which the app now allows).

- **Source:** as above (`TestSandboxRecurrenceCapture`, read-only).
- **Written:** by the owner in a Things app on 2026-10-11, 13:49:59–13:50:01 UTC
  (16:49–16:50 in Moscow; `sp`/`cd`/`md`). Things version and device are not recorded.
- **Captured:** 2026-10-11, history head 439. Nothing needed redacting.
- Every item below index 438 is identical to `recurrence-seeds-2026-10-11.json`
  (history is append-only); the file repeats them so it stands on its own.

New days: 2026-10-19 is `1792368000`, 2026-10-25 `1792886400`, 2026-11-11 `1794355200`.
All five new items are one commit (slot 438, `slot_items` 5), although the owner's
completions of the two series were separate actions: the app batches what it has not
synced yet, so a commit is not one user action.

| `index` | Object | `t` | What it is |
|---|---|---|---|
| 438 | monthly template `DRmr…` | modify | `acrd` = 2026-10-11 (the day instance 1 was completed), `tir` = 2026-11-11 (a month later). No `md`; `icsd`/`icc` untouched; no new instance is created |
| 438 | weekly template `KAee…` | modify | `icc 2`, `icsd` = 2026-10-19 (the day after the occurrence just generated), `tir` = 2026-10-25 (the next occurrence). No `md` |
| 438 | weekly instance 1 `R2db…` | modify | completed: `ss 3`, `sp`, `md`, the keys of a plain to-do's completion |
| 438 | monthly instance 1 `Tk2u…` | modify | completed: `ss 3`, `sp`, `md` |
| 438 | weekly instance 2 `Vxtk…` | create | the instance for 2026-10-18, generated by the app when the owner completed that occurrence: every to-do key, a copy of the template as it stood (title, area, note, `st 2`, `tir`, `ix`, `ti`, …) with `rt [template]`, `rr null`, `sr` = `tir` = 2026-10-18, `icsd null`, `icc 0`, `lt true`, and already completed (`ss 3`, `sp`) |

Pinned by `server/recurrence_capture_test.go` (`…_SecondReadExtendsFirst`,
`…_CompletionCommit`, `…_GeneratedInstance`, and the completion and create key sets
against `completeTask` and `taskCreatePayload`).

Not in this capture: an instance the app generates when its day arrives, without an
early completion (the next weekly occurrence is 2026-10-25, the after-completion one
2026-11-11), so the open instance's `st`/`tir` and the moment of generation; whether
completing a fixed-schedule instance alone touches its template (here it coincided with
generating instance 2); in which zone `acrd` is taken (the completion was on 2026-10-11
both in UTC and in Moscow); and everything else listed for the first capture: edits,
ending or deleting a repetition, fixed monthly/yearly rules, `fa` > 1, end dates and
counts, daily and yearly units, reminders.
