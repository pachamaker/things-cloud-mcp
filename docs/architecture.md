# Architecture

Current state (baseline `3164e8a`) and target state of the Things Cloud MCP server.
Decisions that move us from one to the other live in [`docs/adr/`](adr/).

## 1. Goal and constraints

**Goal.** One person manages their Things 3 tasks from any device (iPhone, iPad, Mac,
web) through Claude or ChatGPT, from anywhere, without a Mac being online.

**Hard constraints**

| Constraint | Consequence |
|---|---|
| Things has no public API; only the private Things Cloud sync protocol | We speak that protocol directly and own every byte we write (see [`protocol/things-cloud.md`](protocol/things-cloud.md)) |
| Things Cloud accepts any payload and lets clients choke on it | All validation happens in this server; write paths must be evidence-based |
| Claude/ChatGPT call remote MCP servers from their own cloud, not from the phone | The server must be reachable over public HTTPS and must authenticate callers (OAuth) |
| Single user, single Things account | No multi-tenancy; SQLite is enough; one process |
| Repetition, Today "confirmation" and similar logic run inside the Things apps | The server sees their results only after an app syncs; some views must be derived |

**Quality attributes, in priority order:** data safety → correctness of views →
security → operability → latency → breadth of features.

## 2. System context

```mermaid
flowchart LR
  user([Owner<br/>iPhone · iPad · Mac · Web])
  subgraph AI["AI assistants (vendor cloud)"]
    claude[Claude<br/>custom connector]
    chatgpt[ChatGPT<br/>connector / app]
  end
  subgraph VPS["Owner's VPS"]
    mcp[things-cloud-mcp]
  end
  cloud[(Things Cloud<br/>cloud.culturedcode.com)]
  apps[Things apps<br/>Mac · iOS · iPadOS · watchOS]

  user -- chat --> claude
  user -- chat --> chatgpt
  claude -- "MCP over HTTPS + OAuth" --> mcp
  chatgpt -- "MCP over HTTPS + OAuth" --> mcp
  mcp -- "history pull / commit" --> cloud
  apps -- "history pull / commit" --> cloud
  user -- uses --> apps
```

The server is just another sync client of Things Cloud, next to the official apps.

## 3. Containers

```mermaid
flowchart TB
  subgraph Host["VPS (Docker Compose)"]
    proxy[Caddy<br/>TLS termination · rate limit]
    idp[Pocket ID<br/>OAuth 2.1 AS · passkeys]
    subgraph App["things-cloud-mcp (single Go process)"]
      transport[Transport<br/>/mcp · /.well-known · /api]
      engine[Engine<br/>queries · commands · clock]
      syncer[Sync engine<br/>history → SQLite]
      writer[History writer<br/>commit · 409 retry]
    end
    db[(SQLite WAL<br/>/data/things.db)]
    audit[(Write audit log<br/>/data/audit.jsonl)]
  end
  cloud[(Things Cloud)]

  proxy --> transport --> engine
  proxy --> idp
  transport -. "validate JWT (JWKS)" .-> idp
  engine --> syncer --> db
  engine --> writer
  writer --> audit
  syncer --> cloud
  writer --> cloud
```

Today `transport` and `engine` are fused in `server/` with package-level globals, there
is no OAuth, no audit log, and deployment targets Fly.io. The target split is described
in §7.

## 4. Code map (current)

| Path | Role | Notes |
|---|---|---|
| `server/main.go` | Process wiring, REST handlers, debug endpoints, graceful shutdown | Globals `client`, `syncer`, `history`; hard-coded `/data/things.db` |
| `server/mcp.go` | MCP tool registry (36 tools) and read handlers, output formatting | `mark3labs/mcp-go` v0.44, Streamable HTTP, stateless |
| `server/write.go` | All write commands, wire payload builders, `historyWrite` | One item per commit; global `historyMu` |
| `server/json_body.go` | Bounded JSON body decoding for REST | |
| `sync/sync.go` | `Syncer`: incremental pull, paging, retries, cursor persistence | Serialised by `Syncer.mu` |
| `sync/process.go` | Applies history items to SQLite inside one transaction; tombstones | Field-by-field merge in `applyTaskPayload` |
| `sync/store.go` | Row ⇄ `things.Task` mapping | Drops `rr`, `rt`, `sb`, `dds` |
| `sync/state.go` | Read model: view queries (Inbox/Today/Anytime/Someday/Upcoming/…) | UTC-based; see KI-02/KI-03 |
| `sync/detect.go`, `sync/changes.go` | Semantic change detection (40+ change types) | Used by `thingsync`, not by the MCP tools |
| `sync/schema.go` | SQLite schema v4 and migrations | v4 wipes the cache to force resync |
| `*.go` (root, package `thingscloud`) | SDK: HTTP client, histories, items, wire types, notes, repeat maths, account | Module path still `github.com/arthursoares/things-cloud-sdk` |
| `state/memory/` | In-memory aggregation of items | Used by CLI tools and tests only |
| `syncutil/` | Helpers for CLI analytics | |
| `cmd/` | 14 CLI/debug tools (`things-cli`, `thingsync`, tracers, dumpers) | Some duplicate the server's wire format (divergence risk) |
| `tests/*.sh` | Black-box scripts against a deployed server | Hit a real account; must target a sandbox |
| `tapes/` | Recorded HTTP responses for SDK unit tests | |

## 5. Runtime flows (current)

### 5.1 Read

```mermaid
sequenceDiagram
  participant A as Assistant
  participant M as MCP handler
  participant S as Syncer
  participant C as Things Cloud
  participant D as SQLite
  A->>M: tools/call things_list_today
  M->>S: Sync()
  S->>C: GET /history/{id} (head index)
  alt new slots
    S->>C: GET /history/{id}/items?start-index=N (paged)
    S->>D: apply items (one transaction)
  end
  M->>D: view query (State.TasksInToday)
  D-->>M: rows (+1 query per task for tags)
  M-->>A: JSON text
```

Every read costs at least one round trip to Things Cloud. Sync failures surface as
tool errors (since 2026-03-05) instead of stale data.

### 5.2 Write

```mermaid
sequenceDiagram
  participant A as Assistant
  participant W as write.go
  participant H as History (writer)
  participant C as Things Cloud
  participant S as Syncer
  A->>W: tools/call things_edit_task
  W->>W: validate input shape (Base58, dates)
  Note over W: no check that the target exists or has the right type (KI-10)
  W->>H: lock historyMu, Sync() head index
  H->>C: GET /history/{id}
  W->>C: POST /commit?ancestor-index=N  (1 item)
  alt 409 Conflict
    W->>C: GET /history/{id} then retry once
  end
  W->>S: Sync() (best effort)
  W-->>A: {"status":"updated","uuid":...}
```

A write costs three to five round trips and returns no resulting state.

## 6. Domain model

```mermaid
erDiagram
  AREA ||--o{ PROJECT : contains
  AREA ||--o{ TODO : contains
  PROJECT ||--o{ HEADING : groups
  PROJECT ||--o{ TODO : contains
  HEADING ||--o{ TODO : groups
  TODO ||--o{ CHECKLIST_ITEM : has
  TODO ||--o{ TODO : "subtask (pr = parent task)"
  TAG ||--o{ TAG : "parent (pn)"
  TODO }o--o{ TAG : tagged
  PROJECT }o--o{ TAG : tagged
  AREA }o--o{ TAG : tagged
  REPEAT_TEMPLATE ||--o{ TODO : "generates instances (rt)"
```

Ubiquitous language (use these words in code, tools and docs):

| Term | Meaning | Wire |
|---|---|---|
| To-do / task | Actionable item | `Task6`, `tp=0` |
| Project | Container of to-dos with an outcome | `Task6`, `tp=1` |
| Heading | Section inside a project | `Task6`, `tp=2` |
| Area | Long-lived responsibility containing projects and to-dos | `Area3` |
| Start state | Inbox / started / deferred | `st` |
| When / start date | Day the item becomes active | `sr` (+`tir` for Today) |
| This Evening | Sub-section of Today | `sb=1` |
| Deadline | Hard due date | `dd` |
| Logbook | Completed or canceled items | `ss∈{2,3}` |
| Repeating template / instance | Hidden rule-bearing item / generated copy | `rr` / `rt` |
| Civil date | A calendar date in the owner's timezone, no time | encoded as midnight UTC |

## 7. Target architecture

```mermaid
flowchart TB
  subgraph transport["transport/ (thin)"]
    mcpT[mcp: tool schemas, annotations, structured output]
    restT[rest: optional, same use cases]
    oauth[auth: RFC 9728 resource metadata + JWT validation]
  end
  subgraph app["app/ (use cases)"]
    q[Queries: views, search, get]
    c[Commands: create, update, move, complete, batch]
    pre[Preconditions: exists · type · not template · not trashed]
  end
  subgraph domain["domain/ (pure, no I/O)"]
    model[Model: Task, Project, Heading, Area, Tag, Checklist]
    view[ViewClassifier]
    civil[CivilDate + Clock + TZ]
    wire[Wire encoders: verified mutations only]
  end
  subgraph infra["infra/"]
    cloudc[Things Cloud client]
    store[SQLite mirror]
    auditl[Audit log]
  end
  transport --> app --> domain
  app --> infra
  infra --> domain
```

Principles:

1. **One `Engine` value** owns config, clock, cloud client, syncer, writer and audit
   log. No package-level mutable state. Handlers receive it explicitly.
2. **Domain is pure.** View classification, date maths and wire encoding are pure
   functions of (entity, today, timezone) and are table-tested.
3. **Commands are validated against the mirror before any byte leaves the process**
   (ADR-0004), committed as one history commit (single or batch), audited, then
   read back and returned.
4. **Wire encoders are an allowlist.** A mutation that is not backed by a capture
   fixture does not have an encoder and therefore cannot be sent.
5. **Reads are freshness-bounded.** Sync on read, but coalesce concurrent syncs and
   skip if the last successful sync is younger than a small window (e.g. 2 s), so a
   burst of tool calls costs one round trip.
6. **Transport is replaceable.** Upgrading `mcp-go` or switching to the official Go
   SDK touches only `transport/`.

Migration is incremental (strangler): extract `domain/civil` and `domain/view` first
(fixes KI-02/03), then `Engine`, then preconditions and the allowlist, then OAuth.
See [`roadmap.md`](roadmap.md).

## 8. Cross-cutting concerns

| Concern | Current | Target |
|---|---|---|
| AuthN to the server | none on `/mcp`; optional static bearer on `/api` | OAuth 2.1 single-user (ADR-0002) |
| Secrets | env vars; password logged in `DEBUG` (KI-09) | env/secret files; redaction tested |
| Time | UTC everywhere | owner's IANA timezone, injectable clock (ADR-0003) |
| Observability | `log.Printf` | structured logs (`log/slog`), request id, write audit log, `/healthz` + `/readyz` |
| Backups | none | nightly copy of SQLite + audit log; the cloud stays the source of truth |
| Testing | unit + tapes; shell scripts against real account | unit, captures, conformance snapshots, sandbox e2e (ADR-0005, ADR-0007) |
