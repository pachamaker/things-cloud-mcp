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

## Implementation notes (2026-10-12)
Decision 4, the conformance harness, is built (`docs/testing/conformance.md`); decisions
1–3 and 5 are not yet. Differences from the text above:

1. **No `cmd/conformance`.** The exporter is a dev-only Python script
   (`scripts/conformance/export_things.py`, things.py pinned by hash); the dump is an
   `e2e`-tagged test (`TestSandboxConformanceDump`) so that it runs behind the sandbox
   harness's account guard, read-only firewall and redaction; the comparison
   (`TestConformance_Snapshots`) lives in `server/` because it replays the snapshot
   through the read-model methods the list tools call, which are in package `main`.
   `scripts/conformance/snapshot.sh` runs the three steps.
2. **Snapshots are sandbox-only**, not "sandbox or owner": the exporter requires the
   marker area `MCP E2E SANDBOX` in the app's database and the dump requires the same
   area UUID on Things Cloud. Directory names carry the local time and a label
   (`<YYYY-MM-DDTHHMM>-<label>`), not only the date.
3. **`input.json` records the mirror and history.** `mirror` is the SDK's `things.Task`
   as the server held it (title and note cleared); `wire` is history folded per key,
   which also keeps `tir`, `sb`, `dds`, `rr`, `rt` for SDK versions that did not. The test
   fails where the two disagree (an SDK read bug) and where the app's database and Things
   Cloud disagree on a view-deciding field (an unsynced change: retake the snapshot).
4. **Titles** are HMAC-SHA256 under a one-off key that is never committed, so hashes
   match across the two files but not against guessed titles.
5. **The app's views are mapped to the server's conventions** before comparing: the
   app's Anytime includes Today's started to-dos, `things_list_anytime` does not; there
   is no Trash tool; Logbook is `things_list_completed`.
6. **Known gaps** are explicit entries (`conformanceKnownGaps`, each with a KI) that
   explain a difference only for items that have the gap's cause and only when
   correcting for it makes the views equal: projects not listed (KI-02), repeating
   templates and canceled to-dos (KI-05). This Evening and dismissed deadlines were gaps
   until the classifier read `sb` and `dds` (KI-02); the harness now compares the
   evening flag too.
7. **things.py is the reference**, so the harness inherits its blind spots: to-dos of a
   Someday project count as Anytime, and future occurrences of repeating to-dos are not
   in Upcoming. Comparing with the app's own lists (AppleScript `to dos of list "Today"`)
   would close them; not done.
