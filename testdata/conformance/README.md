# Conformance snapshots

What the Things app showed and what the server's mirror held for the sandbox account at
the same moment (ADR-0005). How to take one, the privacy rules and how failures read:
[`docs/testing/conformance.md`](../../docs/testing/conformance.md). Every directory with
an `expected.json` is a snapshot, and `TestConformance_Snapshots` (`server/conformance_test.go`)
replays it in `go test ./...`. Snapshots are evidence: never edit them by hand.

Directory name: `<YYYY-MM-DDTHHMM>-<label>`, the Mac's local time when it was taken.
Both files are JSON with one task per line; `format` is `1`.

## `expected.json` (the Things app, `scripts/conformance/export_things.py`)

| Field | Meaning |
|---|---|
| `source`, `things_version`, `database_version` | things.py version, Things version, database schema version |
| `app_running` | whether Things was running during the export (`false` for a "Mac closed" snapshot) |
| `exported_at`, `today`, `timezone` | the moment, the Mac's calendar day and its IANA zone |
| `title_key_id` | names the snapshot's one-off title key; equal in both files |
| `marker_area` | UUID of the area `MCP E2E SANDBOX`: the same account on both sides |
| `tasks[]` | every to-do and project in the app's database, headings excluded |
| `tasks[].type` | `to-do` or `project` |
| `tasks[].title_hash` | HMAC-SHA256 of the title under the title key, first 8 bytes as hex |
| `tasks[].views` | things.py views it is in: `inbox`, `today`, `anytime`, `someday`, `upcoming`, `logbook`, `trash` (things.py's Anytime includes Today's started to-dos) |
| `tasks[].evening` | in Today with `startBucket = 1` (This Evening) |
| `tasks[].app` | the classification inputs as the app's database holds them, under wire names (below) |

## `input.json` (the server, `TestSandboxConformanceDump`)

| Field | Meaning |
|---|---|
| `source`, `sdk` | how it was read; the SDK module version that filled the mirror |
| `dumped_at`, `today`, `timezone` | the moment and the server's day in that zone (equal to the export's) |
| `history_head` | the history index the mirror and the history read both reached |
| `title_key_id`, `marker_area` | as in `expected.json` |
| `tasks[]` | every to-do, project and heading on Things Cloud, trashed and closed ones included |
| `tasks[].mirror` | the SDK's `things.Task` exactly as the server's mirror held it, `Title` and `Note` cleared; absent if the mirror lacks the object |
| `tasks[].wire` | history folded per key, under wire names (below); absent if history has no live object |

## Wire-name fields (`app`, `wire`)

`tp` type (0 to-do, 1 project, 2 heading) · `ss` status (0 open, 2 canceled, 3
completed) · `tr` trashed · `st` start (0 Inbox, 1 started, 2 deferred) · `sr` start
date · `tir` today reference date · `dd` deadline · `dds` deadline suppression date ·
`sb` start bucket (1 This Evening) · `rr` whether a repeat rule is set (a repeating
template) · `rt` the template of a repeating instance · `pr` project · `agr` heading.
Days are civil dates `YYYY-MM-DD` (4001-01-01 is Things' "never"); a value that does not
decode as a day is kept as `raw:<value>`; `null` means no value; `""` no reference.
`docs/protocol/things-cloud.md` §5 describes the fields.
