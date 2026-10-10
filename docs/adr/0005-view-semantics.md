# ADR-0005: View semantics owned by one classifier, verified against real Things data

## Status
Proposed (2026-10-10). Fixes KI-02, KI-05, KI-13.

## Context
- Things computes Inbox/Today/Upcoming/Anytime/Someday/Logbook on the client from
  several fields (`st`, `sr`, `tir`, `dd`, `dds`, `sb`, `ss`, `tr`, parent state,
  templates). There is no endpoint that returns a view.
- The server's definitions in `sync/state.go` are narrower than Things' (overdue start
  dates, unconfirmed scheduled tasks, deadline-driven Today, projects in Today,
  canceled items, templates). The March 2026 incident shows the definitions were
  tuned by anecdote, not by comparison with the app.
- The same logic is duplicated, slightly differently, in `sync/detect.go`.

## Options
| Option | Pros | Cons |
|---|---|---|
| A. Patch SQL queries case by case | Small diffs | Same failure mode as March 2026; logic stays duplicated |
| B. **Pure `ViewClassifier` + conformance snapshots from a real Things DB** | One definition; regression-proof; measurable | Needs a Mac once per snapshot |
| C. Delegate to a Mac (AppleScript / things.py) at runtime | Exact | Requires a Mac online — defeats the project goal |

## Decision
Option B.

1. `domain/view.Classify(task, parents, today) → View` (+ `Evening bool`) is the only
   definition. SQL queries pre-filter cheaply; the classifier makes the final call.
   Change detection uses the same function.
2. Open-item lists exclude canceled, completed, trashed, deleted items and repeating
   templates. Logbook is the only place for `ss ∈ {2,3}`.
3. Project membership resolves through headings.
4. **Conformance harness** (`cmd/conformance`, dev-only):
   - On a Mac with Things installed, export the local Things database views using
     [`things.py`](https://github.com/thingsapi/things.py) (read-only) into
     `testdata/conformance/<date>/expected.json` (UUID → view, evening flag).
   - In the same minute, dump the server's mirror for the same account (sandbox or
     owner, sanitized: titles hashed) into `input.json`.
   - A Go test runs the classifier over `input.json` with the snapshot's `today` and
     timezone and diffs against `expected.json`. Every mismatch is a failing test
     case.
   - Take snapshots at awkward moments: just after midnight local time, after leaving
     the Mac closed for a day (unconfirmed scheduled items), with overdue deadlines,
     with This Evening items, with repeating tasks.
5. Responses of list tools include `today` and `timezone`, and each task carries its
   computed `view`.

## Consequences
- Easier: "Today is incomplete" becomes a reproducible failing test instead of a
  morning surprise; workarounds in assistant instructions can be deleted.
- Harder: snapshots need a Mac and care with personal data (hash titles, never commit
  real notes).
- Known limitation: repeating-instance generation is client-side. Until the server can
  safely materialise virtual instances from stored `rr`, a repeating to-do appears
  only after any Things app has generated it. The tool output says so when templates
  exist for the day.
