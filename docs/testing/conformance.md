# Conformance snapshots (ADR-0005)

The server derives Inbox, Today, Anytime, Someday, Upcoming and Logbook itself
(`server/views.go`, `server/readmodel.go`); Things Cloud has no view endpoints. The
conformance harness checks that derivation against the Things app: a **snapshot**
records, at one moment, which views the Things app on a Mac shows every to-do and
project in, and what the server's mirror of the same account held. A Go test in the
normal suite replays the mirror through the read model on the snapshot's day and
compares. Decision: ADR-0005 option B. Tracking issue: KI-02. Exit criterion of roadmap
Phase 1: the suite passes on at least three snapshots taken at different times of day.

| File | What |
|---|---|
| `scripts/conformance/export_things.py` | Exporter: reads the Things app's database read-only with [things.py](https://github.com/thingsapi/things.py), writes `expected.json` |
| `scripts/conformance/requirements.txt` | things.py, pinned by version and hash; installed into a virtual environment outside the repository |
| `scripts/conformance/test_export_things.py` | Exporter tests on a synthetic database |
| `scripts/conformance/snapshot.sh` | Takes a snapshot: key, export, dump, comparison |
| `server/conformance_dump_e2e_test.go` | `TestSandboxConformanceDump` (tag `e2e`): reads the sandbox account, writes `input.json` |
| `server/conformance_dump_fakecloud_e2e_test.go` | Offline self-test of the dump against the fake cloud (tags `e2e fakecloud`) |
| `server/conformance_test.go` | `TestConformance_Snapshots`, the comparison and the known gaps (no tag: runs in `go test ./...`) |
| `server/conformance_unit_test.go` | Unit tests of the comparison |
| `testdata/conformance/` | The snapshots, one directory each; file format in its [`README.md`](../../testdata/conformance/README.md) |

## How a snapshot is compared

1. **The app's side** (`expected.json`). The exporter copies the Things database with
   SQLite's backup API (one consistent state, even while Things writes) into a private
   temporary directory and runs things.py's `inbox`, `today`, `anytime`, `someday`,
   `upcoming`, `logbook` and `trash` over the copy. things.py does not expose This
   Evening, the today reference date, deadline suppression or repeat columns; the
   exporter reads them from the same copy (`startBucket`, `todayIndexReferenceDate`,
   `deadlineSuppressionDate`, `rt1_recurrenceRule`, `rt1_repeatingTemplate`). "This
   Evening" is Today with `startBucket = 1`. `today` is the Mac's local calendar day,
   the one things.py compares against, and `timezone` its IANA zone.
2. **The server's side** (`input.json`). `TestSandboxConformanceDump` starts the sandbox
   harness read-only, checks that its day in that zone is the export's day and that the
   account holds the export's marker area (same UUID: the same account as the Mac), then
   records every to-do, project and heading twice: as the SDK mirror holds it (`mirror`,
   the SDK's `things.Task` with title and note cleared) and as history stores it (`wire`,
   all history items folded per key, which also carries `tir`, `sb`, `dds`, `rr`, `rt`
   that the SDK mirror does not keep yet). If history moves while it reads, it stops.
3. **The comparison** (`TestConformance_Snapshots`). The mirror goes through the same
   read-model methods the list tools call (`readModel.tasksInViewOn` for the five views,
   `CompletedTasksInRange` for Logbook) on the snapshot's day. The app's views are mapped
   to the server's conventions first: `things_list_anytime` leaves out what Today shows
   (the app's Anytime includes it), and there is no Trash tool.

Each to-do and project gets one verdict:

| Verdict | Meaning | Test result |
|---|---|---|
| agree | the server lists it in exactly the app's views; This Evening matches | pass |
| known gap | the difference is fully explained by entries of `conformanceKnownGaps` | pass, logged as `KNOWN GAP KI-NN name` with the UUIDs |
| mismatch | the app shows it in views the server does not list it in, or the reverse | failing subtest `TestConformance_Snapshots/<snapshot>/<uuid>` |
| drift | views differ and the app's database and Things Cloud disagree on a field that decides a view (`st`, `sr`, `dd`, `dds`, `sb`, `ss`, `tr`, `tp`, `rr`) or on the title: the snapshot caught a change one side had not synced | failing subtest: retake the snapshot |
| read gap | the SDK mirror disagrees with history on `tp`, `ss`, `tr`, `st`, `sr`, `dd`, `pr` or `agr`, or history has an object the mirror lacks | failing subtest: an SDK read bug, open a KI |
| unsynced | the app has an object Things Cloud does not, or the server lists one the app lacks | failing subtest: retake the snapshot |

Drift that changes no view is logged as a note. Known gaps, each with its KI:

| Gap | KI | Why the server differs |
|---|---|---|
| `projects-not-listed` | KI-02 | the list tools classify to-dos only; the app lists projects in Today, Anytime, Someday, Upcoming and Logbook |
| `this-evening` | KI-02 | the SDK mirror does not keep `sb`, so nothing is flagged as This Evening |
| `dismissed-deadline` | KI-02 | the SDK mirror does not keep `dds`, so a to-do whose overdue deadline was dismissed from Today is still listed there |
| `repeating-template` | KI-05 | the SDK mirror does not keep `rr`, so a repeating template is listed like a to-do; the app lists it in no view |
| `canceled-not-in-logbook` | KI-05 | `things_list_completed` lists completed to-dos only |

A gap applies only to items that have its cause (for example `this-evening` only when
history has `sb = 1`), and only when correcting for it makes the views equal; otherwise
the item fails and the message names the gaps that did not explain it. A gap that
explains nothing in any snapshot is logged, so it can be deleted once its KI is fixed.

## Privacy

Snapshots are committed to a public repository.

- **Sandbox only.** The exporter refuses a Things database without exactly one area
  titled `MCP E2E SANDBOX`; the dump runs behind the sandbox harness's account guard
  (`docs/testing/sandbox-e2e.md`) and checks that the cloud holds that same area UUID.
  Never sign this Mac's Things into the real account for a snapshot.
- **Titles** become HMAC-SHA256 hashes (8 bytes, hex) under a one-off key that
  `snapshot.sh` keeps in a `chmod 600` temporary file for the length of the run and then
  deletes. Only a key ID is committed, so the two files can be matched with each other
  but not with guessed titles.
- **Nothing else from the text**: no notes, tags, area or checklist titles. The mirror's
  title and note are cleared before writing, and the suite fails if a committed mirror
  carries either or a hash is not a hash.
- **What is committed**: UUIDs, types, states, calendar dates, indexes, IDs of
  projects, headings, areas and tags, creation and modification instants of sandbox
  objects, the Things version, and whether the app was running.
- **The database copy** holds everything, notes included; it lives in a private
  temporary directory only while the exporter runs. things.py opens it read-only.
- **Logs** carry counts, UUIDs, field names and dates only, like the sandbox harness's.

## One-time setup

1. The Mac's Things app is signed in to the sandbox account only, and the sandbox has
   the seed list of `docs/testing/sandbox-e2e.md`.
2. The sandbox credentials file exists and `THINGS_SANDBOX_ENV_FILE` points to it
   (`docs/testing/sandbox-e2e.md`, "One-time setup").
3. The exporter's virtual environment, outside the repository:

   ```bash
   python3 -m venv ~/.venvs/things-conformance
   ```

   ```bash
   ~/.venvs/things-conformance/bin/pip install --require-hashes -r scripts/conformance/requirements.txt
   ```

   Another location goes into `THINGS_CONFORMANCE_PYTHON`.
4. macOS may ask on the first run whether the terminal may access another app's data
   (Things' group container); allow it.
5. For the "awkward" snapshots below, add these to-dos in the area `MCP E2E SANDBOX` by
   hand in the Things app (prefix `CONF `, so they are told apart from the seeds):

   | Title | Set up in the Things app | Exercises |
   |---|---|---|
   | `CONF evening` | When → This Evening, on the day of the snapshot | This Evening (`sb`) |
   | `CONF deadline yesterday` | Deadline → today, no start date, the day before the snapshot | deadline-driven Today of an overdue deadline |
   | `CONF deadline dismissed` | as `CONF deadline yesterday`; on the snapshot day, remove it from Today | deadline suppression (`dds`) |
   | `CONF tomorrow` | When → Tomorrow, the day before an after-midnight snapshot | a scheduled day that arrives at midnight |
   | `CONF today yesterday` | When → Today, the day before; leave it open | carry-over of an overdue start date |
   | `CONF project today` | a project, When → Today | projects in Today |
   | `CONF in someday project` | a to-do inside a project whose When is Someday | items of a deferred project |

## Taking a snapshot

```bash
export THINGS_SANDBOX_ENV_FILE="$HOME/.config/things-cloud-mcp/sandbox.env"
```

```bash
scripts/conformance/snapshot.sh daytime
```

The label names the situation (lower-case letters, digits, hyphens). The script takes
one to three minutes: two full reads of the sandbox history at one request per second
(`THINGS_E2E_REQUEST_INTERVAL` lowers it, minimum `250ms`). Meanwhile, do not change
anything in Things on any device: a change that reaches only one side shows up as drift,
and a sync during the dump stops it. It writes
`testdata/conformance/<YYYY-MM-DDTHHMM>-<label>/` and prints the comparison. The
timezone is the Mac's; the dump runs in it (`THINGS_TIMEZONE` is set for it).

### When to take them

At least three, at different times of day, are the Phase 1 exit criterion:

| Label | When | How | What it catches |
|---|---|---|---|
| `daytime` | any normal day | Things running and synced | the baseline |
| `after-midnight` | 00:05–00:30 local time | Things running; `CONF tomorrow` and `CONF today yesterday` set up the evening before | the day boundary: arrived scheduled items, overdue carry-over, a server on the wrong day (KI-03) |
| `mac-closed` | after the Mac was closed for a day | quit Things the evening before, close the Mac; the next day take the snapshot **before opening Things** (`app_running: false`). Optionally open Things afterwards, let it sync, and take `mac-closed-opened` | scheduled items no Things client has confirmed yet (`st = 2`, start date ≤ today): things.py predicts them into Today the way the app shows them with a yellow dot |
| `evening-overdue` | any time | `CONF evening`, `CONF deadline yesterday` and `CONF deadline dismissed` present | This Evening, deadline-driven Today, dismissed deadlines |

The repeating seeds (12, 13) and the completed and canceled seeds are in every snapshot.

## Mismatches

Every mismatch is a finding. For each failing UUID (the Things link
`things:///show?id=<uuid>` opens it on the Mac):

1. **Drift or unsynced**: retake the snapshot (delete its directory, wait for the apps to
   sync, run the script again). Do not commit a snapshot with drift.
2. **Read gap**: an SDK reading bug. Open a KI with the UUID and the fields.
3. **View mismatch**: find the rule that differs. Either fix the classifier (a test named
   after the KI first, AGENTS.md §9), or open a KI and add a known gap to
   `conformanceKnownGaps` whose `adjust` recognises exactly the cause from the item's
   fields. Never skip by UUID and never edit a snapshot's files by hand.

## Adding a snapshot to the repository

1. Run the script; read its output, the notes and known gaps included.
2. Look at the directory: two files, one task per line, no clear text.
3. Commit the directory on the branch of the KI it is evidence for, for example
   `KI-02: add the after-midnight conformance snapshot of 2026-10-13`, with the
   findings in the PR description.

Snapshots are evidence and are never rewritten. A new situation is a new snapshot; one
that no longer reflects how Things behaves (a Things update changed a rule) is deleted
with a note in the KI.

## When the server or the SDK changes

- **A gap is fixed** (say the classifier learns `dds`): delete its entry from
  `conformanceKnownGaps`; the suite then demands agreement on every snapshot.
- **This Evening**: once the server flags it, set it in `serverViews` and compare.
- **The SDK keeps a new field** (`rr`, `sb`, `dds`, `tir`): `mirror` is the SDK's own
  `things.Task` as JSON, so the field decodes in new snapshots by itself, but older
  snapshots lack it. Fill it from the `wire` block where `conformanceCases` builds the
  mirror for the replay, so older snapshots replay what the newer SDK would have read.
  A renamed SDK field fails the strict decoder: migrate the snapshots.
- **The file format changes**: bump `format` on both sides and migrate the snapshots in
  the same change.

## Limitations

- things.py is a model of the app as well, not the app's screen: it counts to-dos of a
  Someday project as Anytime, leaves the next occurrences of repeating to-dos out of
  Upcoming, and predicts Today for a database the app has not opened since the day
  changed. The harness checks the server against things.py's reading of the app's
  database; check the key snapshots by eye in the app.
- One Mac, one account (the sandbox). The repeating to-dos are whatever the app has
  generated by the time of the snapshot (ADR-0005 "Known limitation").
- The server's own conventions are taken as given: Anytime excludes Today, Logbook is
  `things_list_completed`, there is no Trash.
