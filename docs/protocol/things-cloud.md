# Things Cloud Protocol Reference

Consolidated, evidence-tagged reference of the Things Cloud sync protocol as used by
this repo. There is **no official documentation**; everything here was learned from
traffic captures of the official apps, crash investigations, and the open-source
`things-cloud-sdk` lineage.

> **Rule for agents:** a field or value marked anything other than ✅ must not be
> *written* by new code without first capturing what the official client sends
> (see "Adding a new write" at the end). Reading is fine.

Evidence legend:
- ✅ **verified** — observed in captures of the official client and round-tripped
  without client errors
- 🟡 **inferred** — consistent with captures/DB schema, not explicitly verified
- ❓ **unknown** — name or semantics are guesses
- ⛔ **dangerous** — writing it has corrupted or crashed clients before

Sources: `docs/client-side-bugs.md`, `docs/crash-investigation.md`,
`docs/2026-02-23-api-capabilities-review.md`, `types.go` (schema comments copied from
the Things SQLite DB), `server/write.go`.

---

## 1. Transport

Base URL: `https://cloud.culturedcode.com`. JSON over HTTPS.

Common request headers (set in `client.go`):

| Header | Value | Notes |
|---|---|---|
| `User-Agent` | `ThingsMac/32209501` | Impersonates Things for Mac. See `docs/legal-and-risk.md`. |
| `Things-Client-Info` | base64(JSON device metadata) | `ClientInfo` in `client.go` |
| `Accept` / `Accept-Charset` / `Accept-Language` | `application/json` / `UTF-8` / `en-US` | |
| `Authorization` | `Password <plaintext>` | Only on account-level endpoints (verify, account, history keys). **Never log it** (KI-09). |

Commit requests additionally send `Schema: 301`, `Push-Priority: 5`,
`App-Id: com.culturedcode.ThingsMac`, and an all-zero `App-Instance-Id`.

Error responses observed (here and in other implementations):

| Status | Meaning | Handling |
|---|---|---|
| 409 `OutdatedAncestor` | Commit's `ancestor-index` is behind the head; nothing was written | Re-read head, re-validate, retry (bounded) |
| 429 `AbusePrevention` | Too many requests, including plain reads | Back off; coalesce syncs |
| 404 `AccountIssue` / 404 without `Authorization` | Stale history key after account reset, or missing auth header on account endpoints | Rebuild mirror / add header |
| 500 | `start-index` beyond head | Check head first |

The shell integration tests wait 1 s between calls to avoid 429.

## 2. Endpoints

| Endpoint | Method | Purpose | Status |
|---|---|---|---|
| `/version/1/account/{email}` | GET | Verify credentials; returns the account's history key | ✅ |
| `/version/1/account/{email}` | PUT / DELETE | Sign-up, confirm, change password, accept SLA, delete account | 🟡 (dangerous, not used by the server in normal operation) |
| `/version/1/history/{id}` | GET | History metadata incl. `latest-server-index`, `latest-schema-version` | ✅ |
| `/version/1/history/{id}/items?start-index=N` | GET | Page of history slots starting at slot `N` | ✅ |
| `/version/1/history/{id}/commit?ancestor-index=N&_cnt=1` | POST | Append one commit (map of items) | ✅ |
| `/version/1/account/{email}/own-history-keys` | GET/POST/DELETE | List/create/delete history keys | ❓ returns 404 for all tested accounts |
| `/version/1/app-instance/{id}` | PUT | Register device for APNS push | 🟡 unused by the server |

### 2.1 Reading history

`GET .../items?start-index=N` returns:

```json
{
  "items": [ { "<uuid>": { "t": 1, "e": "Task6", "p": { ... } } }, ... ],
  "current-item-index": 1234,
  "schema": 301,
  "latest-total-content-size": 0, "start-total-content-size": 0, "end-total-content-size": 0
}
```

- Each element of `items` is one **history slot** (one commit) and may contain several
  entity items keyed by UUID.
- The next cursor is `start-index + len(items)` — not `current-item-index`
  (that was the 2026-03-05 cursor bug).
- A `start-index` beyond the server head returns HTTP 500; check
  `GET /history/{id}` first.

### 2.2 Writing history

`POST .../commit?ancestor-index=<latest known server index>` with body:

```json
{ "<uuid-1>": { "t": 0, "e": "Task6", "p": { ... } },
  "<uuid-2>": { "t": 1, "e": "Task6", "p": { ... } } }
```

- Optimistic concurrency: if another device committed after `ancestor-index`, the
  server answers **409 Conflict**; re-read the head and retry.
- Success returns `{"server-head-index": N}`.
- Multiple items per commit are supported (the official app batches; `things-cli
  batch` uses this). The MCP server currently sends exactly one item per commit.
- The cloud does **not validate** payload semantics. A malformed item is accepted and
  then breaks clients when they pull it. The server is the only line of defence.

## 3. Item envelope

| Key | Meaning | Values |
|---|---|---|
| `t` | action | `0` create ✅, `1` modify ✅, `2` delete 🟡 (behaviour unclear; prefer tombstones) |
| `e` | entity kind | `Task6` ✅, `Task7` ✅ read (written by Things ≥ 3.23, Aug 2026 — **not handled by this repo yet, KI-25**), `ChecklistItem3` ✅, `Area3` ✅, `Tag4` ✅, `Tombstone2` ✅, `Settings3`/`Settings5` 🟡, `Command`/`Command3` 🟡 (Mail to Things queue); older kinds (`Task2/3/4`, `Area2`, `Tag2/3`, `ChecklistItem/2`) appear in old histories and must be readable |
| `p` | payload | partial object; on modify, only changed keys are sent |

**Identifiers** ✅: 21–22 character Base58 strings (Bitcoin alphabet, no `0 O I l`)
encoding 16 random bytes. A non-Base58 identifier crashes Things during decode
(`BSIdentifierFromBase58String`), and the bad item stays in the history forever.
⛔ An identifier whose first random byte is `0x00` loses that byte in naive big-integer
encoding (the current `generateUUID()`, KI-26), and `1`-padding it was also reported to
crash Things. Only emit identifiers whose natural encoding carries all 16 bytes
(safest: redraw until 22 characters).

**Entity kinds and versions.** Things 3.23 (iPhone 19 Aug 2026, Mac 21 Aug 2026)
introduced `Task7` together with the repeating to-dos overhaul; current versions are
Mac 3.24.1 / iPhone 3.24.2. Readers must accept both `Task6` and `Task7`. Upstream
[`task7-write-policy.md`](https://github.com/arthursoares/things-cloud-sdk/blob/main/docs/task7-write-policy.md)
documents which `Task7` writes have been validated; title-only `Task6` modifies on
`Task7` items were reported to apply correctly.

## 4. Value encodings

| Kind | Encoding | Evidence |
|---|---|---|
| Instant (`cd`, `md`, `sp`, `dld`) | Unix seconds as float with sub-second precision, e.g. `1770713623.4716659` | ✅ |
| Calendar date (`sr`, `tir`, `dd`, `rr.sr`, `rr.ia`, `rr.ed`) | Unix seconds of **00:00 UTC on the user's local calendar date**, integer | ✅ (capture: `1770681600` = 2026-02-10 00:00 UTC for a task made "Today" that day) |
| "Never" sentinel | `64092211200` = 4001-01-01 | ✅ for `rr.ed`; reported in `dd` (KI-04) |
| Boolean | JSON bool; some old payloads use `0/1` | ✅ |
| Null vs absent | On modify, an explicit `null` clears a field; an absent key leaves it unchanged | ✅ (`TaskActionItemPayload.UnmarshalJSON` tracks presence) |

Consequence: to compute "today" you need the user's timezone. Deriving it from UTC is
wrong for any user not on UTC (KI-03).

## 5. `Task6` payload

Tasks, projects and headings are all `Task6`, distinguished by `tp`.

| Key | Meaning | Values / notes | Evidence |
|---|---|---|---|
| `tp` | type | `0` to-do, `1` project, `2` heading | ✅ |
| `tt` | title | string | ✅ |
| `nt` | note | object, see §5.2 | ✅ |
| `ss` | status | `0` open, `2` canceled, `3` completed | ✅ |
| `sp` | stop date (completion/cancel instant) | instant or `null`; `0` renders as 1970 — never send `0` | ✅ |
| `tr` | in trash | bool | ✅ |
| `st` | start state | `0` not started (Inbox), `1` started (Anytime/Today), `2` deferred (Someday/Upcoming) | ✅ |
| `sr` | start date ("when") | calendar date or `null` | ✅ |
| `tir` | today index reference date | calendar date or `null`; set together with `sr` when a task is put into Today | ✅ |
| `ti` | today index (ordering within Today) | int | 🟡 |
| `sb` | start bucket | `0` normal, `1` This Evening | 🟡 (named `SubtaskBehavior` in `types.go`, which is wrong) |
| `dd` | deadline | calendar date or `null` | ✅ |
| `dds` | deadline suppression date | calendar date or `null`; set when the user dismisses an overdue deadline from Today | 🟡 |
| `pr` | parent | `[projectUUID]` for a task in a project; `[taskUUID]` for a subtask; `[]` otherwise | ✅ |
| `agr` | action group (heading) | `[headingUUID]` or `[]` | 🟡 |
| `ar` | area | `[areaUUID]` or `[]` | ✅ |
| `tg` | tags | `[tagUUID, ...]` | ✅ |
| `ix` | index within its container | int | ✅ |
| `cd` / `md` | creation / modification instant | | ✅ |
| `rr` | recurrence rule | object or `null`, see §5.3 | ⛔ |
| `rt` | repeating template | `[templateUUID]` on generated instances | 🟡 |
| `icsd`, `icc`, `icp` | instance creation start date / count / completed-by-children | | ❓ |
| `ato` | alarm time offset (reminder time, seconds from start of day) | int or `null` | ❓ |
| `rmd`, `lai`, `acrd`, `do`, `dl`, `lt`, `rp` | reminder date, last alarm interaction, action-required date, due order, delegates, leaves tombstone, ? | defaults in `taskCreatePayload` | ❓ |
| `xx` | extension data | `{"sn": {}, "_t": "oo"}` on create; official clients put device metadata in `sn` | 🟡 |

On **create** (`t: 0`) the official client sends every key. The server mirrors that in
`taskCreatePayload` (no `omitempty`). On **modify** (`t: 1`) send only the keys you
change plus `md`.

### 5.1 View placement (what the user sees)

Things computes views on the client from `st`, `sr`, `tir`, `dd`, `dds`, `sb`, status and
the parent's state. Our best current model, with `today` = the user's local calendar
date. The Today/Upcoming/Someday rules mirror `things.py`'s `today()` (three unions:
started with a start date · deferred with start date ≤ today · no start date with an
unsuppressed past/today deadline), `upcoming()` and `someday()` in
[`things/api.py`](https://github.com/thingsapi/things.py/blob/main/things/api.py),
which reads the Things app's own database. Still to be verified against our data by the
conformance harness (ADR-0005):

| View | Rule |
|---|---|
| Inbox | `tp=0`, `st=0`, open, not trashed |
| Today | open, not trashed, `tp∈{0,1}`, and one of: (`st=1` and `sr ≠ null`) · (`st=2` and `sr ≤ today`) · (`sr = null` and `dd ≤ today` and `dds = null`) |
| This Evening | Today ∩ `sb=1` |
| Upcoming | `st=2` and `sr > today` (plus future instances of repeating templates, generated client-side) |
| Anytime | `st=1`, not in Today; excludes items whose project is deferred |
| Someday | `st=2` and `sr = null` |
| Logbook | `ss ∈ {2,3}`, not trashed |
| Trash | `tr = true` |

Writing a placement — verified combinations only:

| Intent | `st` | `sr` | `tir` |
|---|---|---|---|
| Inbox | 0 | null | null |
| Today | 1 | today | today |
| Anytime | 1 | null | null |
| Someday | 2 | null | null |
| Upcoming on date D (D > today) | 2 | D | null |

`st=2` together with today's date was the original crash trigger in early SDK versions
(`docs/client-side-bugs.md`, Bug 1). Never write it.

### 5.2 Notes (`nt`)

| Form | Shape | Evidence |
|---|---|---|
| Full text | `{"_t": "tx", "t": 1, "ch": crc32(text), "v": "text"}` | ✅ |
| Delta | `{"_t": "tx", "t": 2, "ps": [{"r": "...", "p": pos, "l": len, "ch": crc32}]}` | ✅ read, never written |
| Legacy | XML string `<note xml:space="preserve">…</note>` | 🟡 read only |

The server always writes full-text notes. Delta patches are applied with
`ApplyPatches` (`notes.go`), which clamps out-of-range positions.

### 5.3 Recurrence (`rr`) ⛔

Do not write. See KI-01 and ADR-0004. Known keys, for reading only:

| Key | Meaning (inferred) |
|---|---|
| `fu` | frequency unit: `16` daily, `256` weekly, `8` monthly, `4` yearly |
| `fa` | frequency amplitude (every N units) |
| `of` | detail list: `{"dy": n}`, `{"wd": n}`, `{"dy": n, "mo": n}`, `{"wdo": n, "wd": n}` — base (0/1) and weekday numbering unverified |
| `tp` | `0` fixed schedule, `1` after completion |
| `ia`, `sr` | anchor / start reference dates |
| `ed` | end date (`4001-01-01` = never) |
| `rc` | repeat count; `ts` time shift; `rrv` rule version (`4` observed) |

Repetition is a client-side mechanism: a hidden template carries `rr`, and each
Things app generates instances (`rt: [template]`) as days arrive. A server that only
talks to the cloud sees an instance only after some Things app has created it.

## 6. Other entities

| Entity | Create payload keys (server) | Notes |
|---|---|---|
| `ChecklistItem3` | `tt`, `ts` (`[taskUUID]`), `ss`, `sp`, `ix`, `cd`, `md`, `lt`, `xx` | ✅ complete = `ss:3, sp:now`; reopen = `ss:0, sp:null` |
| `Area3` | `tt`, `ix`, `tg`, `xx` | ✅ create; deletion untested |
| `Tag4` | `tt`, `sh` (shortcut or `null`, never `""`), `pn` (`[parentTagUUID]`), `ix`, `xx` | ✅ create; deletion untested |
| `Tombstone2` | `dloid` (deleted object UUID), `dld` (instant) | ✅ for checklist items; untested for tags/areas |

## 7. Consistency model

- The history is an append-only log shared by every device on the account; the cloud
  orders commits by server index.
- Every Things app pulls the log and materialises its own local database. The server
  does the same into SQLite (`sync/`).
- Last writer wins per field, ordered by commit (with `md` used by clients for
  conflict resolution).
- Propagation to other devices is near-real-time when they are online (APNS push),
  otherwise on next launch.
- Things apps perform background "housekeeping" writes (e.g. confirming scheduled
  tasks, generating repeat instances). The server must tolerate seeing those.

## 8. Adding a new write (procedure)

1. Perform the action in the official Things app on a **sandbox account** while
   capturing traffic (e.g. Proxyman/Charles on macOS).
2. Save the commit body, strip personal data, and commit it under
   `testdata/captures/<feature>-<date>.json` with the Things app version.
3. Write a test that builds the payload with server code and diffs it against the
   capture (key set, value types, null vs absent).
4. Round-trip on the sandbox account: write via the server, then open Things on Mac
   **and** iOS and confirm nothing breaks after a full sync and app restart.
5. Update this document (evidence ✅) and the capability table in `AGENTS.md`.
