# `repeat` is temporarily disabled

Register entry: **KI-01** in `docs/known-issues.md`; decision: ADR-0004.

**Status:** any `repeat` value is rejected with an error on `things_create_task` and
`things_edit_task` (both MCP and REST), **including `repeat: "none"`**. Clearing a rule still
writes `rr: null` onto an item that may be a repeating template or instance, which is itself a
recurrence write. `repeat` is no longer part of the MCP tool schemas; the handlers still read it
so that a stray value is rejected instead of silently ignored. The guard is `rejectRepeat`
(`server/write.go`), called first in `createTask()` and `editTask()`; no write path sets `rr`.

## Why

A user reported that creating tasks with `repeat` set corrupted their Things Cloud history and
crashed Things 3 on both macOS and iOS on every subsequent launch, with no local recovery path on
iOS (recovery required direct SQLite manipulation on macOS; iOS needed vendor-side history rollback).
Reported batch: 25 tasks written in one session, 21 without `repeat` synced cleanly, 4 with `repeat`
broke both clients.

Given the severity (irreversible on iOS) and the fact that **no payload validation exists anywhere
in this codebase** to catch a malformed write before it ships, `repeat` is disabled unconditionally
as a precaution while this is investigated — independent of how strong the evidence for the specific
root cause turns out to be.

## What the investigation found

The reporter's technical diagnosis was built from the **native Things.app's local SQLite schema**
(`TMTask` table, `rt1_*` columns, an XML-plist recurrence rule read via `hex(rt1_recurrenceRule)`).
That diagnosis does not transfer to this codebase: this SDK never touches that local database file —
it speaks the **Things Cloud HTTP/JSON wire protocol**, which represents a repeating task differently
(a single `Task6` item carrying an `rr` JSON field). A repo-wide search found zero references to
`rt1_*` anywhere in this codebase.

This SDK already has a working, unit-tested implementation of that wire-format rule:
`buildRepeatRule` (`server/write.go`, tests in `server/write_repeat_test.go`; no longer called by any write path) and a typed
`RepeaterConfiguration` model (`repeat.go`, tests in `repeat_test.go`). `repeat` is not an
unimplemented or misleading parameter — it's a deliberately shipped feature
(see `docs/BACKLOG.md`: "Add recurring task support (Done)").

This repo also has its own prior crash investigation
(`docs/crash-investigation.md`, dated 2026-02-17) that specifically reproduction-tested "task with
daily repeat rule (created with repeat)" and "add weekly repeat to an existing task" on a clean test
account — both came back **OK, no crash**. The crash that investigation *did* observe happened on an
old account with 500+ pre-existing native-Things items and could not be reproduced on a clean
account; the two bugs found and fixed there (`sp: 0` instead of `null` on un-complete, and missing
past-date validation) were unrelated to `repeat`.

**This contradicts the new report's claim of an exact correlation between `repeat` and corruption.**
That contradiction is not resolved here — it needs input from whoever filed the new report: was it
reproduced on a fresh test account (isolating `repeat` from pre-existing native-Things data, the way
the prior investigation did), or on an account with a long pre-existing native-app history? Which
exact `repeat` value(s) were used for the 4 broken tasks?

Independent of that contradiction, two real risk factors remain unaddressed in the write path today:

- **No payload validation exists anywhere** in this codebase (confirmed by grep for `Validate`).
- Three payload fields — `icsd` (instance-creation-start-date), `icc` (instance-creation-count), and
  `rp` — are hardcoded to `nil`/`0` in every task-create/edit call regardless of whether `repeat` is
  set (`taskCreatePayload` in `createTask`, `server/write.go`). Nothing in this codebase confirms whether Things.app requires
  these populated alongside a non-null `rr`. Suspicious signal: `repeat.go`'s own occurrence-math
  methods (`NextScheduledAt`, `ComputeFirstScheduledAt`) — exactly the kind of logic that would
  compute these values — are currently dead code, called by nothing outside their own tests.

## What the capture answers (2026-10-11)

The owner made two to-dos repeat in a current Things app on the sandbox account
(`SEED repeat weekly`, fixed schedule; `SEED repeat monthly after completion`). Every history
item the app wrote for them is in `testdata/captures/recurrence-seeds-2026-10-11.json`
(README next to it), pinned by `server/recurrence_capture_test.go`; the protocol notes are in
`docs/protocol/things-cloud.md` §5.3. The two open questions above:

1. **`icsd`/`icc`/`rp`.** The app populates `icsd` and `icc` on the template, not `rp`: the
   template gets `icsd` = a calendar date (fixed schedule: the next occurrence, which is also
   its `tir`; after completion: the day after the rule was set) and `icc: 1`; the instance
   keeps `icsd: null`, `icc: 0`; `rp` is `null` on both. The old write path sent `rr` with
   `icsd: null`, `icc: 0`, a combination the app never wrote here.
2. **One record or two.** Two linked records, written in **one commit**: a new hidden
   template (`Task7`, `rr` set, `st: 2`, `sr: null`) and the existing to-do, which keeps its
   UUID and becomes instance 1 (`rt: [template]`, `rr: null`, dated today). The app never
   put `rr` on a visible to-do. A single record with `rr` set, as this repo used to send, is
   a shape the official client does not produce. That makes the report's correlation
   between `repeat` and corruption plausible without proving the cause; the 2026-02
   "no crash" test predates Things 3.23 and `Task7`.

The rule object itself matched: `buildRepeatRule` produces exactly the app's weekly
fixed-schedule `rr`; for "after completion" the app sends `ia: 0` and `of: []` where the
encoder sends the start date and a day. Weekly `of` was `[{"wd": 0}]` for a Sunday, consistent
with Go's `time.Weekday` (one sample). Changing a template's rule is a modify that carries the
whole `rr` object again.

A second read the same day (`testdata/captures/recurrence-completion-2026-10-11.json`), after the
owner completed both instances and, ahead of time, the weekly occurrence of 2026-10-18, adds:

- **Completing an instance** writes `ss: 3`, `sp`, `md` on it, like a plain to-do. For an
  after-completion series the same commit also sets the template's `acrd` (the completion day) and
  `tir` (a month later) and creates no instance yet. A plain completion from the server would leave
  the template part out; what the apps then do with the series is unknown, so completing an
  instance stays on the list of writes the server must refuse (KI-01).
- **An instance the app generates** is a create that copies the template (title, area, note,
  `st: 2`, `tir`, `ix`, `ti`, …) with `rt: [template]`, `rr: null`, `sr` = the template's `tir`,
  `icsd: null`, `icc: 0` and `lt: true`. In the same commit the template counts it (`icc: 2`) and
  moves `icsd` to the day after the occurrence and `tir` to the next one.

## What's needed to re-enable

Captured so far: making a to-do repeat, changing a rule, completing instances of both rule kinds,
and an instance the app generated for an occurrence completed ahead of time (with what it does to
the template's `icsd`/`icc`/`tir`). Still missing, each a capture of the app doing it on the sandbox
account (never production — the blast radius of getting this wrong again is the same crash, and iOS
has no local recovery):

- an instance the app generates when its day arrives (open, no early completion; the next weekly
  occurrence is 2026-10-25), and when the after-completion template produces its next instance
  (its `tir` is 2026-11-11);
- completing a fixed-schedule instance on its own (here it shared a commit with generating the
  next instance);
- editing an instance or a template (title, when, notes);
- fixed monthly and yearly rules (`of` with `dy`/`mo`/`wdo`), `fa` > 1, end dates and counts;
- ending or deleting a repetition.

Then a new ADR (which writes, as `Task7` or `Task6`), an encoder whose output is diffed against
these fixtures (§8 of the protocol notes), and a sandbox round trip on Mac and iOS. Until then,
do not remove `rejectRepeat` from `createTask()`/`editTask()` (`server/write.go`) and do not
re-add `repeat` to the tool schemas. Re-enabling also needs the mirror to store `rr`/`rt`, so
the server can refuse edits to templates and instances (KI-01 fix direction); the SDK fork does,
and the server refuses those edits since [#34](https://github.com/pachamaker/things-cloud-mcp/pull/34).

## Read-path gap (separate, lower-risk, unrelated to the crash)

Independent of the write-path question above: this SDK's local sync cache currently drops the
recurrence rule on read. `TaskActionItemPayload.Repeater` (`types.go:230`) is correctly parsed off
the wire but never copied onto the domain `things.Task` struct in `applyTaskPayload`
(`sync/process.go:356-479`), and the `sync` package's `tasks.recurrence_rule` column
(`sync/schema.go:55`) is always written `NULL` (`sync/store.go:189`). This means a repeating task
synced down from a real account (created natively in Things, or via `things_create_task` before this
hotfix) loses its rule in this server's local cache — already flagged independently in
`docs/2026-02-23-api-capabilities-review.md`. This is a safe, well-scoped fix that doesn't require a
live account to build or test, and is unrelated to whether `repeat` is safe to re-enable for writes.
