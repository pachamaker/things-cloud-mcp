# Architecture

Current state (after the upstream SDK migration, ADR-0001, 2026-10-11) and target state
of the Things Cloud MCP server.
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
    db[(SQLite WAL<br/>$DATA_DIR/things-upstream.db)]
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
is no audit log, and the OAuth resource server (`server/auth.go`) has not been run against
Pocket ID yet. The target split is described
in §7.

## 4. Code map (current)

This module (`github.com/pachamaker/things-cloud-mcp`) contains only the server. The
protocol client and the sync engine are the
[`arthursoares/things-cloud-sdk`](https://github.com/arthursoares/things-cloud-sdk)
dependency, pinned in `go.mod` (v0.6.0) and replaced by one commit of the fork
`pachamaker/things-cloud-sdk` that carries the fixes proposed upstream (`rr`/`rt`,
`sb`/`dds`/`tir`, tombstones without a target; ADR-0001). Differences from the embedded
copy this repo used to carry: `docs/2026-10-10-upstream-sdk-spike.md` §3–§4.

| Path | Role | Notes |
|---|---|---|
| `server/main.go` | Process wiring, router, REST handlers, graceful shutdown | Globals `client`, `mirrorDB`, `mirror`, `history`; cache at `$DATA_DIR/things-upstream.db` |
| `server/mirror.go` | Owns the mirror file and its Syncer; history check (account key + head vs the stored key + cursor) and rebuild | KI-28; one backup `things-upstream.db.before-rebuild` |
| `server/auth.go` | OAuth resource server: config, JWKS cache, RFC 9068 token checks, RFC 9728 metadata, per-tool scopes | Every data route goes through a `gate` (KI-07) |
| `server/mcp.go` | MCP tool registry and read handlers, output formatting | `mark3labs/mcp-go` v0.44, Streamable HTTP, stateless |
| `server/write.go` | All write commands, wire payload builders, ID generator, `historyWrite` | One item per commit; global `historyMu`; `repeat` always rejected (KI-01) |
| `server/readmodel.go` | Read model over SDK `State`: pagination, open-list filtering (canceled excluded), completed range | Loads full lists, then pages in Go (KI-18) |
| `server/views.go` | View classifier (Inbox/Today/Anytime/Someday/Upcoming, This Evening) over `State.AllTasks` | Compares civil dates against the owner's day; reads `dds` and `sb`, not `tir` (KI-02) |
| `server/today.go` | `THINGS_TIMEZONE` parsing, `ownerToday()` | Global `ownerZone`, set once in `main` |
| `domain/civil` | Civil date, clock, owner's "today", wire encoding of day values, zone loading (ADR-0003) | Pure; embeds `time/tzdata` |
| `server/syncgate.go` | Serialises and coalesces `Syncer.Sync()`, backs off on 429, runs history checks and background rebuilds with backoff, logs the history items the SDK skips | The SDK `Syncer` has no lock (KI-27, KI-28); skipped items: KI-29 |
| `server/json_body.go` | Bounded JSON body decoding for REST | |
| `server/redact.go` | Hides the URL of a failed Things Cloud request where SDK errors enter the server (sync gate, `historyWrite`, startup) | The URL carries the history key, a bearer capability (KI-35) |
| SDK `thingscloud` | HTTP client, histories, items, wire types (`Task6`/`Task7`), Base58 codec, notes, account | `History.Write` refuses non-canonical IDs |
| SDK `sync` | `Syncer`: incremental pull, per-batch cursor, cache replay with backup, SQLite store, `State` queries | The fork stores `rr`, `rt`, `sb`, `dds`, `tir` (`TaskReplayVersion` 5: an older cache is replayed once on first start) and skips tombstones without a target as `ItemSkipped` |
| `tests/*.sh` | Black-box scripts against a deployed server | Hit a real account; must target a sandbox |

## 5. Runtime flows (current)

### 5.1 Read

```mermaid
sequenceDiagram
  participant A as Assistant
  participant M as MCP handler
  participant G as Sync gate
  participant S as SDK Syncer
  participant C as Things Cloud
  participant D as SQLite
  A->>M: tools/call things_list_today
  M->>G: Sync()
  alt no sync in the last 2 s and no 429 backoff
    G->>S: Sync()
    S->>C: GET /history/{id} (head index)
    alt new slots
      S->>C: GET /history/{id}/items?start-index=N (paged)
      S->>D: apply each batch and its cursor (one transaction per batch)
    end
  end
  M->>D: State.AllTasks
  D-->>M: rows (+1 query per task for tags)
  M->>M: classify (server/views.go), drop canceled, paginate
  M-->>A: JSON text
```

A burst of reads costs one round trip to Things Cloud. Sync failures surface as
tool errors (since 2026-03-05) instead of stale data.

The gate also keeps the mirror on the account's current history (KI-28): every 15 min on
reads, after a 404 on the mirrored history and before writes (at most a minute old) it
verifies the account's history key, reads that history's head and compares both with the
key and cursor stored in the mirror. A stale mirror is set aside as
`things-upstream.db.before-rebuild` and rebuilt from index 0 in the background; calls fail
fast with a "being rebuilt" error until it is done, and rebuilds back off up to an hour.

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
| To-do / task | Actionable item | `Task6`/`Task7`, `tp=0` |
| Project | Container of to-dos with an outcome | `Task6`/`Task7`, `tp=1` |
| Heading | Section inside a project | `Task6`/`Task7`, `tp=2` |
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
| AuthN to the server | OAuth 2.1 bearer tokens on `/mcp` and `/api` (`server/auth.go`), or local-only without auth on `127.0.0.1` | same, verified end to end with Pocket ID (ADR-0002) |
| Secrets | env vars; password logged in `DEBUG` (KI-09) | env/secret files; redaction tested |
| Time | owner's IANA timezone (`THINGS_TIMEZONE`, UTC if unset) through `domain/civil`; pure date logic takes `today` | injectable clock end to end (ADR-0003) |
| Observability | `log.Printf` | structured logs (`log/slog`), request id, write audit log, `/healthz` + `/readyz` |
| Backups | none | nightly copy of SQLite + audit log; the cloud stays the source of truth |
| Testing | unit tests in `server/`; shell scripts against real account | unit, captures, conformance snapshots, sandbox e2e (ADR-0005, ADR-0007) |
