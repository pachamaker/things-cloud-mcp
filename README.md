# Things Cloud

A Go server and SDK for the [Things 3](https://culturedcode.com/things/) cloud API. Gives [Claude](https://claude.ai) (and other MCP-compatible AI assistants) full read/write access to your task manager.

Built on a reverse-engineered, unofficial SDK — there is no official API documentation from Cultured Code.

> **Fork status.** This is an independently maintained fork of the archived
> [`mattydsmith/things-cloud-mcp`](https://github.com/mattydsmith/things-cloud-mcp).
> It is being reworked for self-hosting on a VPS with authenticated access from Claude
> and ChatGPT. Known defects and the plan are in [`docs/known-issues.md`](docs/known-issues.md)
> and [`docs/roadmap.md`](docs/roadmap.md). Contributors and AI agents start at
> [`AGENTS.md`](AGENTS.md).
>
> **Not affiliated with Cultured Code.** "Things" is a trademark of Cultured Code GmbH & Co. KG.
> This project uses a private, undocumented protocol that can change or be blocked at any
> time, and a bad write can corrupt your Things data on every device. Use at your own risk,
> test with a separate account first, and read [`docs/legal-and-risk.md`](docs/legal-and-risk.md).
>
> **Authentication.** Every data route requires an OAuth 2.1 access token issued to you by
> your own authorization server (Pocket ID, see [`docs/deployment/vps.md`](docs/deployment/vps.md));
> without OAuth settings the server only starts in local-only mode on `127.0.0.1`. Sign-in
> from Claude and ChatGPT through Pocket ID has not been verified end to end yet.

## What it does

The server syncs your Things Cloud data into a local SQLite database and exposes it through two interfaces:

- **MCP Endpoint** (`/mcp`) — [Model Context Protocol](https://modelcontextprotocol.io/) for AI assistants like Claude
- **REST API** (`/api/*`) — off by default, same OAuth tokens, for scripts and apps

Changes you make in the Things app show up immediately. Tasks Claude creates appear in Things within seconds. No restart required.
The server performs an initial sync at startup and now exits if it cannot establish a clean local snapshot.

```
┌──────────────┐     ┌──────────────────┐     ┌──────────────┐
│  Things App  │────▶│  Things Cloud    │◀────│  API Server  │
│  (Mac/iOS)   │     │  (Cultured Code) │     │  (Fly.io)    │
└──────────────┘     └──────────────────┘     └──────┬───────┘
                                                     │
                                              ┌──────┴───────┐
                                              │              │
                                         /api/*         /mcp
                                        REST API    MCP Endpoint
                                              │              │
                                         curl/apps    Claude.ai
                                                     Connector
```

## MCP tools (35)

Once connected, Claude can do pretty much everything you'd do in the Things app:

- **Browse your views** — Today, Inbox, Anytime, Someday, Upcoming, All Tasks, by Project, by Area, Completed (with date filters)
- **Look things up** — get any task, area, or tag; search by title or note content
- **Create** — tasks (with notes, dates, deadlines, project, tags), projects, areas, tags, headings, and checklist items
- **Edit** — title, notes, dates, deadlines, project, area, and tags (recurrence is not supported and repeating to-dos are read-only, see below)
- **Organise** — move tasks between Today, Anytime, Someday, and Inbox
- **Complete & restore** — complete or reopen tasks and checklist items; trash and untrash

<details>
<summary>Full tool reference</summary>

### Read tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_list_today` | Today view: start date today or earlier (overdue and scheduled-date-arrived to-dos), placed in Today, or no start date with a deadline today or earlier that was not dismissed from Today; This Evening to-dos come last with `"evening": true`; "today" is the day in `THINGS_TIMEZONE` (UTC if unset) | `limit`, `offset` |
| `things_list_inbox` | Tasks in the inbox | `limit`, `offset` |
| `things_list_anytime` | Tasks in Anytime | `limit`, `offset` |
| `things_list_someday` | Tasks in Someday (deferred, no start date) | `limit`, `offset` |
| `things_list_upcoming` | Tasks in Upcoming (start date after today) | `limit`, `offset` |
| `things_list_all_tasks` | All open tasks | `limit`, `offset` |
| `things_list_projects` | All projects | `limit`, `offset` |
| `things_list_areas` | All areas | `limit`, `offset` |
| `things_list_tags` | All tags | `limit`, `offset` |
| `things_list_project_tasks` | To-dos in a project, those under its headings included: first the to-dos directly in the project, then each heading's in heading order; to-dos under a heading carry `heading_id` and `heading_title` | `project_uuid`, `limit`, `offset` |
| `things_list_area_tasks` | Tasks in an area | `area_uuid`, `limit`, `offset` |
| `things_list_completed` | Recently completed tasks | `limit`, `completed_after`, `completed_before` |
| `things_list_checklist_items` | Checklist items for a task | `task_uuid` |
| `things_get_task` | Get a single task | `uuid` |
| `things_get_area` | Get a single area | `uuid` |
| `things_get_tag` | Get a single tag | `uuid` |

### Write tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_create_task` | Create a task | `title` (req), `note`, `when`, `deadline`, `project`, `parent_task`, `tags` |
| `things_create_project` | Create a project | `title` (req), `note`, `when`, `deadline`, `area` |
| `things_create_heading` | Create a heading in a project | `title` (req), `project` |
| `things_create_area` | Create an area | `title` (req), `tags` |
| `things_create_tag` | Create a tag | `title` (req), `shorthand`, `parent` |
| `things_edit_task` | Edit a task | `uuid` (req), `title`, `note`, `when`, `deadline`, `project`, `parent_task`, `area`, `tags` |
| `things_complete_task` | Complete a task | `uuid` |
| `things_uncomplete_task` | Reopen a completed task | `uuid` |
| `things_trash_task` | Move to trash | `uuid` |
| `things_untrash_task` | Restore from trash | `uuid` |
| `things_move_to_today` | Schedule for today | `uuid` |
| `things_move_to_anytime` | Move to anytime | `uuid` |
| `things_move_to_someday` | Move to someday | `uuid` |
| `things_move_to_inbox` | Move to inbox | `uuid` |

### Search tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_search_tasks` | Search tasks by title or note | `query` |

### Checklist item tools

| Tool | Description | Parameters |
|------|-------------|------------|
| `things_create_checklist_item` | Add a checklist item to a task | `title` (req), `task_uuid` (req) |
| `things_complete_checklist_item` | Complete a checklist item | `uuid` |
| `things_uncomplete_checklist_item` | Reopen a checklist item | `uuid` |
| `things_delete_checklist_item` | Delete a checklist item | `uuid` |

### Parameter reference

#### `when` — Task scheduling

| Value | Effect |
|-------|--------|
| `today` | Today view |
| `anytime` | Anytime view (triaged, no date) |
| `someday` | Someday view (deferred) |
| `inbox` | Inbox (default for new tasks) |
| `none` | Strip dates without moving the task (edit only) |
| `YYYY-MM-DD` | Future date → Upcoming; today/past → Today |

#### `deadline` — Hard deadlines

| Value | Effect |
|-------|--------|
| `YYYY-MM-DD` | Set a hard deadline |
| `none` | Clear an existing deadline (edit only) |

#### Recurring tasks — not supported

Recurrence cannot be set, changed or removed through this server. `repeat` is not part of
the tool schemas, and any `repeat` value sent to `things_create_task`, `things_edit_task`,
`/api/tasks/create` or `/api/tasks/edit` — `"none"` included — is rejected with an error
before anything is written. Recurrence writes have corrupted Things on macOS and iOS
(incident 2026-08-18, KI-01). Set up or remove recurrence in the Things app. Background:
[`docs/known-issues/repeat-disabled.md`](docs/known-issues/repeat-disabled.md).

Repeating to-dos and projects are read-only. Things keeps the rule on a hidden template
and lists the items it generates. The server's lists never include templates (KI-05), so
`things_list_upcoming` lacks the next occurrence the Things app shows there ("18 Oct",
"Waiting"; KI-36). Task output marks `repeating: "template"` (reachable through
`things_get_task`) or `repeating: "instance"`. Completing, reopening, editing, moving, trashing or restoring
either, or changing its checklist, is refused before anything is written (KI-01); do it
in the Things app.

</details>

## Operational Notes

- **Startup is fail-fast.** The server now requires the initial sync to succeed before it starts serving REST or MCP traffic.
- **Shutdown is graceful.** `SIGINT` and `SIGTERM` trigger an HTTP shutdown with a timeout instead of dropping in-flight requests immediately.
- **Read surfaces support pagination.** REST list endpoints and the corresponding MCP list tools accept optional `limit` and `offset` values.
- **Old `tir`/`sr` cache corruption is repaired automatically.** Opening a pre-fix sync database triggers a one-time local cache reset and full resync from Things Cloud history.
- **Soft-deleted rows are purged from the local cache after sync.** This keeps the SQLite mirror from growing forever while preserving normal sync semantics.

## Getting started

See **[docs/installation.md](docs/installation.md)** for full setup instructions. The short version:

1. Set up a VPS with Caddy and Pocket ID ([docs/deployment/vps.md](docs/deployment/vps.md))
2. Configure your Things Cloud credentials and the OAuth settings
3. Add `https://mcp.<your-domain>/mcp` as a custom connector in Claude or ChatGPT and sign in with your passkey
4. Create a Skill file for your workflow

### Privacy and cost

Your credentials are stored as encrypted secrets on your own Fly.io account — you're not giving them to anyone. Fly.io doesn't bill you if your monthly usage is under $5. After 2+ months of active development and daily use, the author has not been billed.

### Test with a separate account

Since this uses the Things Cloud sync protocol directly, consider creating a separate Things Cloud account to experiment with before pointing it at your main account.

## Skills

The MCP server gives Claude the ability to interact with Things, but to get the most out of it you need a **Claude Skill file** — a small instruction file that teaches Claude your specific projects, tags, and workflows.

See **[docs/skills.md](docs/skills.md)** for a step-by-step guide to creating your own.

## REST API

The REST API is off unless the server runs with `ENABLE_REST_API=true`. Every `/api/*`
endpoint then requires `Authorization: Bearer <access token>` from the same authorization
server as `/mcp`, with the scope `things.read`; the write endpoints also need `things.write`.
A local-only server (`INSECURE_NO_AUTH_LOCAL_ONLY=true`) needs no token. The write endpoints
and write tools exist only when the server runs with `ENABLE_WRITES=true`; by default it is
read-only.

See **[docs/endpoints-and-things-cloud.md](docs/endpoints-and-things-cloud.md)** for how the REST and MCP surfaces sync from Things Cloud history, answer reads from the local SQLite mirror, and commit writes back to the cloud.

| Endpoint | Description |
|----------|-------------|
| `GET /` | Health check (no token) |
| `GET /.well-known/oauth-protected-resource/mcp` | RFC 9728 protected-resource metadata (no token) |
| `GET /api/verify` | Verify Things Cloud credentials |
| `GET /api/sync` | Trigger sync, returns change count |
| `GET /api/tasks/today` | Tasks scheduled for today. Optional query params: `limit`, `offset` |
| `GET /api/tasks/inbox` | Tasks in the inbox. Optional query params: `limit`, `offset` |
| `GET /api/tasks/anytime` | Tasks in Anytime. Optional query params: `limit`, `offset` |
| `GET /api/tasks/someday` | Tasks in Someday. Optional query params: `limit`, `offset` |
| `GET /api/tasks/upcoming` | Tasks in Upcoming. Optional query params: `limit`, `offset` |
| `GET /api/projects` | All projects. Optional query params: `limit`, `offset` |
| `GET /api/areas` | All areas. Optional query params: `limit`, `offset` |
| `GET /api/tags` | All tags. Optional query params: `limit`, `offset` |
| `POST /api/tasks/create` | Create a task |
| `POST /api/tasks/edit` | Edit a task |
| `POST /api/tasks/complete` | Complete a task |
| `POST /api/tasks/trash` | Trash a task |

## SDK

The underlying Go SDK can be used directly as a library. See **[docs/sdk.md](docs/sdk.md)** for documentation including:

- Getting started and quick start guide
- CLI tool (`things-cli`) with create, edit, complete, trash, batch commands
- Working with histories and items
- Persistent sync engine with 40+ semantic change types
- Wire format notes from reverse engineering

## Testing

Unit tests need no network: `go test -race ./...`.

End-to-end tests run the real server against a **dedicated sandbox Things account** and
call every MCP tool through `/mcp`. They are opt-in (build tag `e2e`), refuse to write
unless the account has an area titled `MCP E2E SANDBOX`, and inspect every commit before
it leaves the process. Setup, the one-time seed list and the recommended cadence (at most
daily) are in [docs/testing/sandbox-e2e.md](docs/testing/sandbox-e2e.md):

```bash
THINGS_SANDBOX_ENV_FILE=~/.config/things-cloud-mcp/sandbox.env go test -tags e2e -run '^TestSandboxE2E$' -count=1 -v -timeout 30m ./server
```

The older shell suites call a running server (started with `ENABLE_WRITES=true` and
`INSECURE_NO_AUTH_LOCAL_ONLY=true`) and create items in the account it is connected to —
sandbox only. The writing suites stop
before their first write unless the account has the area `MCP E2E SANDBOX`:

```bash
./tests/test-smoke.sh          # Core read/write workflow (11 checks)
./tests/test-mcp.sh 010        # All MCP write tools (44 checks)
./tests/test-mcp-read.sh       # All MCP read tools (29 checks)
./tests/test-mcp-protocol.sh   # JSON-RPC handshake and error handling (11 checks)
./tests/test-api.sh            # All REST endpoints (needs ENABLE_REST_API=true; ACCESS_TOKEN=… against an OAuth server)
```

### macOS Things app test matrix

The broader Things app test matrix tracks **154 cases**. Recorded results are
**121 passed, 4 failed, 10 partial, 1 blocked, and 18 not run**. A partial result
means only part of the expected behavior was verified. ✅ marks a passed case.
The four failed cases below have no confirmed fix commit in the test record. The matrix includes
app-only checks and checks that also observe sync. The separate MCP sandbox
results below cover the smaller set of critical bidirectional scenarios.

<details>
<summary>All 154 Things app test cases and recorded results</summary>

| ID | Area | Scenario | Result |
| --- | --- | --- | --- |
| DOC-001 | structure | Organize ongoing work | ✅ |
| DOC-002 | structure | Project handles multiple steps | ✅ |
| DOC-003 | checklists | Break down one task | ✅ |
| DOC-004 | checklists | Check step independently | ✅ |
| DOC-005 | checklists | Project progress feedback | ✅ |
| DOC-006 | creation | Keyboard creation | ✅ |
| DOC-007 | creation | Insert after selection | ✅ |
| DOC-008 | creation | Keyboard project creation | ✅ |
| DOC-009 | creation | Keyboard checklist creation | ✅ |
| DOC-010 | creation | Completion command | ✅ |
| DOC-011 | creation | Cancellation command | ✅ |
| DOC-012 | creation | Log completed item | ✅ |
| DOC-013 | creation | Keyboard reorder | ✅ |
| DOC-014 | clipboard | Paste task batch | ✅ |
| DOC-015 | clipboard | Paste multiline title | ✅ |
| DOC-016 | clipboard | Paste checklist batch | ✅ |
| DOC-017 | clipboard | Normalize pasted bullets | ✅ |
| DOC-018 | clipboard | Bound clipboard batch | ✅ |
| DOC-019 | clipboard | Finder link capture | **PARTIAL** — Finder drag did not insert a URL. The actual drop location could not be verified. |
| DOC-020 | notes | Task notes persistence | ✅ |
| DOC-021 | notes | Project notes persistence | ✅ |
| DOC-022 | notes | Notes bullet indentation | ✅ |
| DOC-023 | notes | Find within note | ✅ |
| DOC-024 | notes | Replace within note | ✅ |
| DOC-025 | notes | Titles stay plain | ✅ |
| DOC-026 | markdown | Inline emphasis | ✅ |
| DOC-027 | markdown | Attention formatting | ✅ |
| DOC-028 | markdown | Heading levels | ✅ |
| DOC-029 | markdown | Quotation and divider | ✅ |
| DOC-030 | markdown | Code punctuation | ✅ |
| DOC-031 | markdown | Markdown task statuses | ✅ |
| DOC-032 | markdown | Markdown reversibility | ✅ |
| DOC-033 | markdown | Visible link syntax | ✅ |
| DOC-034 | scheduling | Today assignment | ✅ |
| DOC-035 | scheduling | Evening assignment | ✅ |
| DOC-036 | scheduling | Future assignment | ✅ |
| DOC-037 | scheduling | Deferred assignment | ✅ |
| DOC-038 | scheduling | Clear schedule | ✅ |
| DOC-039 | scheduling | Independent deadline | ✅ |
| DOC-040 | scheduling | Clear deadline | ✅ |
| DOC-041 | scheduling | Deadline overview | ✅ |
| DOC-042 | lists | Active overlap | **FAIL** — Things showed a Today task in Anytime after expanding the list, while the REST Anytime result excluded the same UUID (SYNC-001). |
| DOC-043 | lists | Deferred exclusion | ✅ |
| DOC-044 | lists | Future project hiding | ✅ |
| DOC-045 | lists | Inbox processing | ✅ |
| DOC-046 | lists | Daily grouping | ✅ |
| DOC-047 | lists | Upcoming drag scheduling | **PARTIAL** — Neither attempted drop moved the task into the target day in Upcoming. |
| DOC-048 | lists | Logged history | ✅ |
| DOC-049 | natural dates | Relative start | ✅ |
| DOC-050 | natural dates | Combined reminder | ✅ |
| DOC-051 | natural dates | Relative deadline | ✅ |
| DOC-052 | natural dates | Ordinal date | ✅ |
| DOC-053 | notifications | Reminder delivery | **PARTIAL** — Things accepted the reminder and the mirror recorded it. Notification Center timed out before delivery could be verified. |
| DOC-054 | notifications | Reminder snooze | **NOT RUN** — Snooze was not attempted because the reminder delivery could not be observed. |
| DOC-055 | notifications | Deadline limitation | ✅ |
| DOC-056 | tags | Create and apply tag | ✅ |
| DOC-057 | tags | Inherited tag filter | ✅ |
| DOC-058 | tags | Conjunctive filter | **PARTIAL** — Command-click acted as single selection in both attempts. Modifier delivery and app behavior could not be isolated. |
| DOC-059 | tags | Global tag search | ✅ |
| DOC-060 | tags | Remove assignment | ✅ |
| DOC-061 | tags | Nested tags | ✅ |
| DOC-062 | tags | Tag ordering | **NOT RUN** — The tag ordering scenario was not executed. The run log gives no reason. |
| DOC-063 | tags | Tag shortcut toggle | ✅ |
| DOC-064 | headings | Heading scope | ✅ |
| DOC-065 | headings | Group selection | ✅ |
| DOC-066 | headings | Heading move | ✅ |
| DOC-067 | headings | Heading duplicate | ✅ |
| DOC-068 | headings | Heading promotion | ✅ |
| DOC-069 | headings | Heading archive | ✅ |
| DOC-070 | moving | Batch filing | ✅ |
| DOC-071 | moving | Heading destination search | ✅ |
| DOC-072 | moving | Create destination while moving | ✅ |
| DOC-073 | moving | Remove parent | ✅ |
| DOC-074 | moving | Area movement | ✅ |
| DOC-075 | moving | Checklist reorder | ✅ |
| DOC-076 | conversion | Checklist promotion | ✅ |
| DOC-077 | templates | Project duplication | ✅ |
| DOC-078 | templates | Task duplication | ✅ |
| DOC-079 | templates | Dates do not shift | ✅ |
| DOC-080 | repeat | Periodic repeat | ✅ |
| DOC-081 | repeat | After completion repeat | **PARTIAL** — Upcoming showed the next occurrence preview. The next occurrence had not materialized in the mirror when checked. |
| DOC-082 | repeat | Selected weekdays | ✅ |
| DOC-083 | repeat | Repeat project | ✅ |
| DOC-084 | repeat | Template edits propagate | ✅ |
| DOC-085 | repeat | Pause and resume | **PARTIAL** — The paused state was visible before the due boundary. Behavior after the boundary and the subsequent resume were not verified. |
| DOC-086 | repeat | Stop repeating | ✅ |
| DOC-087 | repeat | Deadline lead time | ✅ |
| DOC-088 | search | Immediate title search | ✅ |
| DOC-089 | search | Extended content search | ✅ |
| DOC-090 | search | Checklist search | ✅ |
| DOC-091 | search | Logged search | ✅ |
| DOC-092 | search | Special project views | ✅ |
| DOC-093 | trash | Delete and recover | ✅ |
| DOC-094 | trash | Delete undo | ✅ |
| DOC-095 | windows | Separate list windows | ✅ |
| DOC-096 | windows | Cross-window move | ✅ |
| DOC-097 | windows | Parent window | **PARTIAL** — The project opened, but a separate window was not verified. Computer Use could not perform the documented Option-click. |
| DOC-098 | quick entry | Global capture | **NOT RUN** — The global shortcut did not produce an observed Quick Entry window. Task creation was not reached. |
| DOC-099 | quick entry | Discard capture | ✅ |
| DOC-100 | quick entry | Save capture keyboard | ✅ |
| DOC-101 | quick entry | Autofill source | **NOT RUN** — The Safari Autofill scenario was not executed. The run log gives no reason. |
| DOC-102 | calendar | Local event display | **FAIL** — The test iCloud event was missing from Upcoming after reselecting the calendar and restarting Things. The cause was not established. |
| DOC-103 | calendar | Calendar selection | ✅ |
| DOC-104 | calendar | Calendar local scope | **NOT RUN** — The second Things client was not observed, so per-device calendar behavior could not be checked. |
| DOC-105 | widgets | Widget list and tag | **NOT RUN** — Computer Use could not open the widget gallery or attach to Notification Center. |
| DOC-106 | widgets | Widget completion | **NOT RUN** — Computer Use could not reach an interactive widget to complete a task. |
| DOC-107 | widgets | Widget capture destination | **NOT RUN** — Computer Use could not reach the widget controls to test the capture destination. |
| DOC-108 | shortcuts | Shortcut creation | ✅ |
| DOC-109 | shortcuts | Shortcut query | ✅ |
| DOC-110 | shortcuts | Shortcut edit | ✅ |
| DOC-111 | url scheme | URL creation | ✅ |
| DOC-112 | url scheme | URL project creation | ✅ |
| DOC-113 | url scheme | URL authenticated edit | **NOT RUN** — The authenticated URL edit was not executed. The run log gives no reason. |
| DOC-114 | url scheme | URL navigation | ✅ |
| DOC-115 | applescript | Script creation | ✅ |
| DOC-116 | applescript | Script deadline | ✅ |
| DOC-117 | applescript | Script status | ✅ |
| DOC-118 | share | Plain text export | ✅ |
| DOC-119 | share | List share limitation | ✅ |
| DOC-120 | mail | Email capture | **NOT RUN** — No Mail to Things capture attempt was recorded. |
| DOC-121 | mail | Email constraints | **NOT RUN** — No Mail to Things attempt covering attachment and body limits was recorded. |
| DOC-122 | siri | Voice creation | **NOT RUN** — No Siri task creation attempt was recorded. |
| DOC-123 | siri | Voice schedule | **NOT RUN** — No Siri scheduled task creation attempt was recorded. |
| DOC-124 | spotlight | Spotlight creation | **NOT RUN** — Computer Use timed out while attaching to Spotlight. The Create To-Do action was not reached. |
| DOC-125 | metadata | Creation timestamp | ✅ |
| DOC-126 | cloud | Cross-device propagation | **NOT RUN** — The run did not record observations from a second Things device on the same account. |
| DOC-127 | cloud | Offline availability | **BLOCKED** — The offline test required an authorized isolated environment to preserve the current network connection. |
| REG-001 | recurrence | Early completion | ✅ |
| REG-002 | recurrence | Rescheduling choices | ✅ |
| REG-004 | recurrence | Relative-repeat rescheduling | ✅ |
| REG-005 | recurrence | Early project generation | **FAIL** — Editing a child from the Upcoming preview changed the template child. Things offered no early generation prompt and no project instance appeared. |
| REG-007 | recurrence | Batch rule controls | **PARTIAL** — Pause and Resume were verified. Stop and the unaffected third-template check remained open. |
| REG-008 | layout | Expanded-item layout | ✅ |
| REG-009 | checklist | Empty checklist editing | ✅ |
| REG-010 | checklist | Whitespace paste | **FAIL** — Pasting one nonblank line with surrounding line breaks created three empty checklist rows. It reproduced after restarting Things 3.24.1 (APP-001). |
| REG-011 | checklist | Checklist additive selection | ✅ |
| REG-012 | tags | Tag suggestion order | **NOT RUN** — The tag suggestion order scenario was not executed. The run log gives no reason. |
| REG-013 | deadline | Deadline color | ✅ |
| REG-014 | search | Search status styling | ✅ |
| REG-015 | search | Search highlight reset | ✅ |
| REG-016 | checklist | Checklist cut/delete | ✅ |
| REG-018 | quick-entry | Capture checklist deletion | ✅ |
| DOC-128 | lifecycle | Reopen completed task | ✅ |
| DOC-129 | lifecycle | Reopen canceled task | ✅ |
| DOC-130 | lifecycle | Reopen project | ✅ |
| DOC-131 | lifecycle | Rename project | ✅ |
| DOC-132 | lifecycle | Rename area | ✅ |
| DOC-133 | lifecycle | Delete project tree | ✅ |
| DOC-134 | lifecycle | Delete area tree | **NOT RUN** — The disposable area tree deletion scenario was not executed. The run log gives no reason. |
| DOC-135 | lifecycle | Rename tag | ✅ |
| DOC-136 | lifecycle | Delete tag | **NOT RUN** — The disposable tag deletion scenario was not executed. The run log gives no reason. |
| DOC-137 | lifecycle | Project sidebar order | **PARTIAL** — The sidebar drag changed neither the visible order nor the server indices. A successful move was not established. |
| DOC-138 | lifecycle | Area sidebar order | **PARTIAL** — The sidebar order could not be verified against the mirror because the baseline area indices tied. |
| DOC-139 | lifecycle | Checklist reopen | ✅ |

</details>

### macOS sandbox results (2026-11-07)

On a dedicated Things Cloud sandbox account, the following scenarios passed in both
directions between Things on Mac and the MCP server:

| Scenario | Result |
| --- | --- |
| Create and edit a to-do | ✅ |
| Move a to-do between projects, including from under a heading | ✅ with the fixes in [commit `9c2dec7`](https://github.com/pachamaker/things-cloud-mcp/commit/9c2dec745f604b9be60e9256c3f7528bc536f11b) |
| Complete and reopen a to-do | ✅ |
| Move a to-do to Trash and restore it | ✅ |

For each scenario, we compared the task UUID, affected fields, and a control task across
MCP responses, the VPS mirror, and Things on Mac. After the tested restarts, Things opened
and the checked tasks still appeared once. Moving a scheduled task without specifying
`when` preserved its start date. Moving a task out from under a heading cleared the old
heading, and a separate check confirmed that the task's tag and three checklist items
survived the move and restart.

Search excluded trashed tasks. A direct lookup by UUID could still return `open`
without a Trash flag. The run covered macOS on a sandbox account. iOS, recurring-task
writes, and production use remain outside this run's coverage.

## Local development

```bash
go build -v -o things-server ./server/
export THINGS_USERNAME='...' THINGS_PASSWORD='...'   # sandbox account, never your real one
export DATA_DIR="$PWD/.data"   # SQLite cache things-upstream.db; default /data
export ENABLE_WRITES=true      # write tools are off unless this is true
export INSECURE_NO_AUTH_LOCAL_ONLY=true   # no OAuth; listens on 127.0.0.1 only
export THINGS_TIMEZONE=Europe/Moscow   # owner's IANA zone; "today" is the UTC day if unset
./things-server

go test -race ./...
```

## Credits

Built on top of [arthursoares/things-cloud-sdk](https://github.com/arthursoares/things-cloud-sdk), which reverse-engineered the Things Cloud sync protocol. Since ADR-0001 the server uses it as a pinned Go dependency (`go.mod`) instead of an embedded copy.
