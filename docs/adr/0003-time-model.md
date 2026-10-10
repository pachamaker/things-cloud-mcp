# ADR-0003: Civil dates in the owner's timezone

## Status
Proposed (2026-10-10). Fixes KI-03, prerequisite for KI-02.

## Context
- Things stores day-precision fields (`sr`, `tir`, `dd`, recurrence dates) as the Unix
  time of **00:00 UTC on the user's local calendar date** (verified by capture).
- The server derives "today" from `time.Now().UTC()` in at least five places
  (`server/write.go:226`, `sync/state.go:375-384`, `sync/detect.go:135-170`,
  `server/mcp.go:169-183`, `buildRepeatRule`). For an owner in Europe/Moscow (UTC+3)
  "today" is wrong every night from 00:00 to 03:00; for UTC−X owners it is wrong every
  evening.
- Day comparisons are mixed with instant comparisons (`scheduled_date > now`).
- Tests that depend on `time.Now()` are non-deterministic near midnight.

## Options
| Option | Pros | Cons |
|---|---|---|
| A. Keep UTC, document it | No work | Wrong for every non-UTC user; workarounds in prompts |
| B. Use the host's `TZ` / `time.Local` | One line | Container TZ is an ops accident; silent breakage on redeploy |
| C. **Explicit owner timezone config + civil-date type + injectable clock** | Correct, testable, explicit | Touches every date path once |
| D. Ask the assistant to pass "today" in each call | No server config | Assistants are unreliable at this; every tool grows a parameter |

## Decision
Option C.

1. Config `THINGS_TIMEZONE` (IANA name, e.g. `Europe/Moscow`). **Required**; the server
   refuses to start without a valid zone. The container image embeds tzdata
   (`import _ "time/tzdata"`).
2. `domain/civil` package:
   - `type Date struct{ Y int; M time.Month; D int }` with `FromWire(int64)`,
     `Wire() int64` (midnight UTC), `Before/After/Equal`, `AddDays`, `String()` (`YYYY-MM-DD`).
   - `type Clock interface{ Now() time.Time }` and `Today(clock, loc) Date`.
3. Every day-precision field in the domain model is a `*civil.Date`, never `time.Time`.
   Instants (`cd`, `md`, `sp`) stay `time.Time`.
4. All "is it today / overdue / upcoming" logic compares `civil.Date` values only.
5. SQL queries receive pre-computed civil-date bounds as integers; no `now` instants in
   view queries.
6. Tool inputs accept `YYYY-MM-DD` interpreted in the owner's zone; relative words
   (`today`, `tomorrow`) are resolved by the server with the same clock. Tool outputs
   report `today` (the server's notion of it) in list responses so the assistant can
   detect disagreement.
7. Sentinel handling lives in `civil.FromWire`: year ≥ 4000 → `nil` (KI-04).

## Consequences
- Easier: correct behaviour at night; deterministic tests (`fakeClock`); one place to
  reason about dates; KI-02 becomes a pure function.
- Harder: one more required config value; a one-time refactor across `server/`,
  `sync/`, and change detection.
- Travel: if the owner changes timezone, they change one env var. Things itself uses
  the device's zone, so a mismatch is possible while travelling; acceptable for a
  single user and visible through the `today` field in responses.
