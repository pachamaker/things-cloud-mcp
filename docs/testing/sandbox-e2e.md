# Sandbox end-to-end tests

Opt-in Go tests that run the real server in-process against a **dedicated sandbox
Things Cloud account**, call every MCP tool through `/mcp` with a real MCP client, and
check with a second, independent SDK client what actually landed in the account's
history. Tracking issue: KI-34. Layer "Sandbox e2e" of the test pyramid in ADR-0007.

| File | What |
|---|---|
| `server/sandbox_e2e_test.go` | `TestSandboxE2E`, `TestSandboxSeedChecks`, `TestSandboxCleanupLeftovers` (build tag `e2e`) |
| `server/sandbox_harness_e2e_test.go` | Harness: server start, throttled transport, write firewall, 409 injection, observer client, cleanup (tag `e2e`) |
| `server/sandbox_rules_test.go` | Pure rules (account guard, wire rules, firewall, env file); no tag, unit-tested by `go test ./...` |
| `server/sandbox_fakecloud_e2e_test.go` | In-memory stand-in for Things Cloud for an offline self-test of the harness (tags `e2e fakecloud`) |
| `.github/workflows/e2e.yml` | Manual and nightly CI run in the `things-sandbox` environment (see "CI") |

## Safety model

The harness must never touch the owner's real account (AGENTS.md rule 1) and must never
send a write the official apps might choke on (rule 2). Layers, in the order they act:

1. **Opt-in.** Nothing compiles without `-tags e2e`; without credentials every test skips.
2. **Credentials** come only from `THINGS_SANDBOX_USERNAME` / `THINGS_SANDBOX_PASSWORD`
   or from the file named by `THINGS_SANDBOX_ENV_FILE` (outside the repository,
   `chmod 600`). The real `THINGS_USERNAME` / `THINGS_PASSWORD` are never read for
   access. Values are never printed: all log output of the server and the SDK goes
   through a redactor that masks the username, the password and the history key (history
   endpoints take no `Authorization` header, so the key is a capability).
3. **Account guard.** After the initial (read-only) sync and before any write, the
   account must contain an area titled exactly **`MCP E2E SANDBOX`**, and neither the
   sandbox username nor the e-mail Things Cloud reports for it may equal
   `THINGS_USERNAME` (when set; CI does not set it, so there the marker area is the
   guard). Until the guard passes, the write firewall is locked.
4. **Write firewall.** Every commit is inspected *before it leaves the process*; a
   refused commit fails the tool call and the test, and never reaches Things Cloud:
   - entity kinds `Task6`, `ChecklistItem3`, `Area3`, `Tag4`, `Tombstone2` only (never
     `Task7`); actions create and modify only, modify only for `Task6`/`ChecklistItem3`;
   - created IDs: 22-character canonical Base58 of 16 bytes, first byte ≠ 0 (KI-26);
   - no `rr` on modifies, not even `null`; on creates only `rr: null` and `rt: []`, the
     full key set the official client sends (KI-01);
   - `st`/`sr`/`tir` only in the verified combinations of AGENTS.md §7 (Inbox, Today,
     Anytime, Someday, Upcoming on a future date); `sr`/`tir` without `st` is refused;
   - `ix`, `ti` non-negative integers (KI-31); `sp` never `0`; tag `sh` never `""`;
   - titles must start with the run prefix;
   - modifies and tombstones only for objects this run created (cleanup registers the
     leftovers it trashes). Seed items and everything else on the account are untouchable.
5. **Run prefix and cleanup.** Every title starts with `[e2e-YYYYMMDD-HHMM-xxxx] `. At the
   end — also when the test fails or panics — the run trashes every to-do, heading and
   project it created. A run killed by `-timeout` leaves objects behind:
   `TestSandboxCleanupLeftovers` trashes everything titled `[e2e-…` or `[smoke-test] …`
   (leftovers of the removed `things_smoke_test`, KI-11).
   With `THINGS_E2E_RUN_ID` set (`e2e-` plus up to 40 letters, digits or hyphens) the run
   uses that ID, and the cleanup test trashes only that run's objects (plus
   `[smoke-test] …` leftovers); CI sets it per workflow run.
6. **Throttle.** At most one Things Cloud request per second (`THINGS_E2E_REQUEST_INTERVAL`,
   minimum `250ms`). A GET answered with `429` waits and retries (30 s, 1 min, 2 min, or
   the `Retry-After` header, capped at 5 min); the run log counts these retries. A `429`
   that outlasts the retries, or one on a commit (never retried), stops the run; cleanup
   then waits a minute first. Each CI step is a new process with a full initial sync, so
   the step after a long run can start rate-limited (KI-27).
7. **Time.** The harness reads `THINGS_TIMEZONE` from its environment the way the
   server does (ADR-0003): an IANA name makes the server's "today" that zone's day,
   unset keeps the UTC day, an unknown name fails the test. It skips itself within
   15 minutes of midnight in that zone, where a run could straddle two days, and the
   wire check takes the day of each write from its `md`/`cd` instant in the same zone.
   With `THINGS_E2E_REQUIRE_RUN` set (CI sets it) this and the missing-credentials skip
   fail the test instead: a skipped run passes in GitHub and would look green without
   touching the account.
8. **Public logs.** CI logs of this public repository are readable by anyone, and GitHub
   masks only exact secret values. The harness prints UUIDs, field names, statuses,
   dates, entity kinds and its own strings (run-prefixed titles it wrote, seed titles from
   this page), never an e-mail or a title or note read from the account: tasks are
   printed with title and note lengths only, stored-text differences without values, and
   the URL-escaped username and `/history/<key>` are masked as well.

## One-time setup

1. Create a separate Things Cloud account for testing. Sign in to it only on a device or
   macOS user that has never held your real Things data: signing a Things app into a
   different account can merge its local data into that account.
2. In a Things app signed in to the sandbox, create the area **`MCP E2E SANDBOX`** and the
   seed items below.
3. Put the credentials in a file outside the repository and restrict it:

   ```bash
   mkdir -p ~/.config/things-cloud-mcp
   ```

   ```bash
   chmod 600 ~/.config/things-cloud-mcp/sandbox.env
   ```

   with the content

   ```
   THINGS_SANDBOX_USERNAME=<sandbox e-mail>
   THINGS_SANDBOX_PASSWORD=<sandbox password>
   ```

   `export `, blank lines, `#` comments and one pair of surrounding quotes are accepted.

### Seed list (create once, by hand, in the Things app)

The server never writes `Task7` or repeating to-dos, so these shapes reach the account
only through the official apps. Create them with Things 3.23 or later (so to-dos are
written as `Task7`), let the app sync, and do not edit them afterwards except as noted.
Unless stated otherwise, put each to-do in the area `MCP E2E SANDBOX` with no date.

| # | Create in the Things app | Title | `TestSandboxSeedChecks` expects |
|---|---|---|---|
| 1 | Area | `MCP E2E SANDBOX` | account guard; listed by `things_list_areas` |
| 2 | To-do | `SEED anytime` | in `things_list_anytime` and in `things_list_area_tasks` for the area |
| 3 | To-do, When → Someday | `SEED someday` | in `things_list_someday` |
| 4 | To-do, When → 1 January 2030 | `SEED upcoming` | in `things_list_upcoming`, `scheduled_for` = `2030-01-01` |
| 5 | To-do, Deadline → 1 June 2030 | `SEED deadline` | `deadline` = `2030-06-01` |
| 6 | To-do with checklist items `one` (ticked) and `two` | `SEED checklist` | two checklist items, one completed |
| 7 | Tag | `SEED tag` | listed by `things_list_tags` |
| 8 | To-do tagged `SEED tag` | `SEED tagged` | its `tags` contain the tag |
| 9 | Project in the area, with heading `SEED heading` | `SEED project` | listed by `things_list_projects` |
| 10 | To-do directly in `SEED project` | `SEED in project` | in `things_list_project_tasks` |
| 11 | To-do under heading `SEED heading` | `SEED under heading` | in `things_list_project_tasks` for `SEED project` (KI-13) |
| 12 | Repeating to-do, every week, fixed schedule | `SEED repeat weekly` | template with `rr.fu = 256`, `rr.tp = 0`; generated instances are counted, one is checked like the templates |
| 13 | Repeating to-do, every month, after completion | `SEED repeat monthly after completion` | template with `rr.fu = 8`, `rr.tp = 1` |
| 14 | To-do, then complete it | `SEED completed` | in `things_list_completed`; in no open list |
| 15 | To-do, then cancel it | `SEED canceled` | in no open list and not in `things_list_all_tasks` (KI-05) |

Every to-do seed is also checked for its entity kind: at least one must be `Task7`
(KI-25). A mismatch in `rr.fu`/`rr.tp` means the protocol notes (§5.3, evidence 🟡) or
the seed are wrong. Repeating templates (and the first generated instance of each) are
checked for KI-01/KI-05: `things_get_task` must report `repeating: "template"` (or
`"instance"`); a template listed in any view, in the marker area or by
`things_search_tasks`, or an open instance listed in no view, fails the test; the views
of each instance are logged; `things_complete_task`, `things_edit_task`,
`things_move_to_today` and `things_trash_task` on each must be refused with a KI-01
error. The refusals happen before any commit is built, and the read-only firewall would
stop one that was not, so the check still sends no commit.

## Running

```bash
export THINGS_SANDBOX_ENV_FILE="$HOME/.config/things-cloud-mcp/sandbox.env"
```

```bash
go test -tags e2e -run '^TestSandboxE2E$' -count=1 -v -timeout 30m ./server
```

```bash
go test -tags e2e -run '^TestSandboxSeedChecks$' -count=1 -v ./server
```

```bash
go test -tags e2e -run '^TestSandboxCleanupLeftovers$' -count=1 -v ./server
```

To run in the owner's zone, as the deployment does, add `THINGS_TIMEZONE` to the
environment (not to the sandbox env file, which holds credentials only):

```bash
export THINGS_TIMEZONE=Europe/Moscow
```

`-count=1` defeats the test cache. Run one test at a time and never two runs on the same
account at once. A full `TestSandboxE2E` run makes roughly 250 Things Cloud requests and
takes about five minutes at the default throttle.

Offline self-test of the harness itself (in-memory fake cloud, no account, no network
except loopback; proves harness logic only):

```bash
go test -tags 'e2e fakecloud' -run '^TestSandbox' -count=1 ./server
```

## CI (GitHub Actions)

`.github/workflows/e2e.yml` runs on `workflow_dispatch` and nightly at 03:17 UTC, never
on push or pull requests. Every test step sets `THINGS_E2E_REQUIRE_RUN=1`, so a skip
fails the job; a dispatch inside the midnight window stops at the offline self-test,
before the account is touched. The job runs in the environment `things-sandbox`, holds the
concurrency group `things-sandbox` (one run at a time, none cancelled), and passes the
secrets only to the steps that run tests. Steps: the offline self-test (no secrets),
`TestSandboxE2E`, `TestSandboxSeedChecks`, then a cleanup of the run's own objects that
runs after failures and cancellations too.

The workflow does not set `THINGS_TIMEZONE`, so CI runs on the UTC day and skips near
00:00 UTC; the nightly 03:17 UTC run is far from it. To exercise the owner's zone in
CI, add `THINGS_TIMEZONE: Europe/Moscow` (not a secret) to the job's `env` in `e2e.yml`
and keep the schedule away from midnight in that zone (03:17 UTC is 06:17 in Moscow).

One-time setup by the owner:

1. GitHub → Settings → Environments → New environment `things-sandbox`:
   **Required reviewers** — yourself (leave "Prevent self-review" off);
   **Deployment branches and tags** — Selected branches, `main` only.
2. Add the environment secrets; `gh` asks for each value interactively:

   ```bash
   gh secret set THINGS_SANDBOX_USERNAME --env things-sandbox --repo pachamaker/things-cloud-mcp
   ```

   ```bash
   gh secret set THINGS_SANDBOX_PASSWORD --env things-sandbox --repo pachamaker/things-cloud-mcp
   ```

3. Run the first few times by hand (Actions → Sandbox e2e → Run workflow on `main`) and
   read the log before relying on the schedule.

With a required reviewer every run, the nightly one included, waits for your approval.
The schedule only fires from the default branch, so it starts once the workflow is on
`main`.

On macOS (case-insensitive file system) the legacy `.github/Workflows/` directory and
`.github/workflows/` are the same folder until KI-15 removes the former from `main`;
`git add` of a new or edited workflow then records it under `Workflows/`, which GitHub
ignores. Check `git ls-files .github` before committing.

## Cadence

- Run `TestSandboxE2E` before merging a change to the write path (`server/write.go`,
  tool handlers that write, wire payloads) or an SDK upgrade, and after deploying such
  a change.
- **At most once a day**, the nightly workflow included. Each run costs ~250 requests to a
  service that rate-limits (KI-27) and leaves one area and one tag behind.
- `TestSandboxSeedChecks` is read-only; run it after SDK upgrades and changes to reading
  or view classification.
- `TestSandboxCleanupLeftovers` only when a run was killed or its cleanup reported errors.
- Every few weeks, delete the `[e2e-…]` areas and tags in the Things app: the server has
  no verified write to delete them (cleanup lists them).

## What `TestSandboxE2E` checks

| Section | Checks |
|---|---|
| setup | create tag, area (with the tag), project (anytime, deadline, area), heading, to-do (today, deadline, project, tag), subtask; read them back with `things_get_task`/`_area`/`_tag` |
| placement | create a to-do per placement (inbox, today, anytime, someday, upcoming date) and check it is listed in exactly that view; move one to-do through today → anytime → someday → upcoming (`when` = date) → inbox, checking after each step |
| edit | title, note, clear note, deadline, clear deadline, area (+ `things_list_area_tasks`), clear area, tags, project (+ `things_list_project_tasks`) |
| lifecycle | complete (+ `things_list_completed`), uncomplete, trash, untrash, with list membership after each |
| checklist | add, complete, reopen, delete |
| repeat_rejected | `repeat` on create and edit (`"none"` included) is rejected with KI-01 and sends no commit |
| read_tools | projects, areas, tags, project/area tasks, all tasks, search |
| conflict_409 | the observer commits a title change to a to-do while the server's edit is in flight; Things Cloud must answer 409 and the server's retry must land both changes |
| observer_mirror | a second SDK client syncs into its own temporary SQLite mirror; titles, parents, tags, statuses, trash flags and view classification must match |
| wire | every committed item is read back from raw history: same kind and action, stored payload equals sent payload, and it passes the firewall rules again; writes by other devices to run objects are logged |
| cleanup | everything created is trashed; the observer confirms |
| tool_coverage | every tool in `tools/list` was called; a new tool fails the run until it is covered |

## Reading the output

- `the sandbox firewall refused a commit` — the server tried to send something outside
  the verified write set. The commit did not leave the process. This is a server bug (or
  a new write that needs evidence first, see `docs/protocol/things-cloud.md` §8).
- `Things Cloud answered 429` — the retries ran out (or a commit got a 429): stop,
  wait, retry later with a larger `THINGS_E2E_REQUEST_INTERVAL`.
- `<key>: sent …, stored …` or `sent but not stored` in the wire section — Things Cloud
  stored something other than what the server sent. Record it in
  `docs/protocol/things-cloud.md` before relaxing the check.
- `KNOWN ISSUE KI-NN` lines are logged, not failures; when one disappears, update the KI.
- `foreign write on a run object` — a Things app on the sandbox wrote housekeeping
  changes to an object of the run while it was running; informational.

## Limitations

- It proves that Things Cloud accepts the writes and that the SDK reads them back as
  intended — not that the Things apps render them correctly. After a run that exercised
  a new or changed write, open Things on Mac and iOS (ADR-0004 round trip).
- One zone per run: the one in `THINGS_TIMEZONE`, or UTC. The owner's-zone paths
  (KI-03) run end to end only when it is set, which CI does not do; otherwise unit tests
  cover them. Dates in assertions follow the server's day.
- Not exercised because the write is not verified: `things_edit_task` with `when: "none"`
  (writes `sr`/`tir` without `st`; the firewall would refuse it), This Evening,
  reminders, reordering, deleting areas and tags, and anything recurring.
