# Installation Guide

> **Legacy (Fly.io).** Kept for reference. The supported target is
> [`deployment/vps.md`](deployment/vps.md). Since KI-07 the server does not start without
> the OAuth settings (`OAUTH_ISSUER`, `MCP_PUBLIC_URL`, `OAUTH_ALLOWED_SUBJECT`) and an
> authorization server that issues its tokens; set them as Fly secrets as described there.
> `API_KEY` no longer exists.

Self-host the Things Cloud MCP server to connect Claude (or any MCP-compatible AI assistant) to your Things 3 task manager.

## Prerequisites

- A [Things 3](https://culturedcode.com/things/) account with Things Cloud sync enabled
- [Go 1.24+](https://go.dev/dl/) (for building from source)
- [Fly.io CLI](https://fly.io/docs/flyctl/install/) (`brew install flyctl`)
- A free [Fly.io account](https://fly.io)

## 1. Clone the repository

```bash
git clone https://github.com/<your-account>/things-cloud-mcp.git
cd things-cloud-mcp
```

## 2. Build locally (optional, to verify)

```bash
go build -v ./...
go test -v ./...
```

Run the server locally to confirm everything works:

```bash
export THINGS_USERNAME='your-things-email'
export THINGS_PASSWORD='your-things-password'
mkdir -p /data
go run ./server/
```

The server starts at `http://localhost:8080`. Verify with:

```bash
curl http://localhost:8080/
# {"service":"things-cloud-api","status":"ok"}
```

## 3. Deploy to Fly.io

### Create the app

```bash
fly launch
```

When prompted, choose a region close to you. The included `fly.toml` configures a shared CPU VM with 1 GB RAM and a persistent volume for the SQLite database.

### Set your credentials

```bash
fly secrets set THINGS_USERNAME='your-things-email' THINGS_PASSWORD='your-things-password'
```

### Deploy

```bash
fly deploy
```

The first deploy creates the persistent volume automatically. The container image is ~14 MB.

### Verify the deployment

```bash
curl https://your-app-name.fly.dev/
# {"service":"things-cloud-api","status":"ok"}
```

## 4. Connect to Claude

### Claude.ai (web)

1. Go to **Settings > Connectors > Add custom connector**
2. Set the URL to `https://your-app-name.fly.dev/mcp`
3. Leave authentication fields empty (the MCP endpoint has no auth)
4. Save

Then ask Claude: *"What's on my Things today?"*

### Claude Code (CLI)

Add the server to your Claude Code MCP config (`~/.claude/mcp.json` or project-level):

```json
{
  "mcpServers": {
    "things": {
      "type": "url",
      "url": "https://your-app-name.fly.dev/mcp"
    }
  }
}
```

## 5. Available tools

Once connected, Claude has access to 36 tools:

**Read** — List tasks by view (today, inbox, anytime, someday, upcoming, all), by project, by area. Get individual tasks, areas, tags. List completed tasks with date filters. List checklist items.

**Write** — Create tasks, projects, areas, tags, headings, and checklist items. Edit tasks (title, notes, dates, project, area, tags, recurrence). Complete, uncomplete, trash, and restore tasks.

**Search** — Case-insensitive substring search across task titles and notes.

**Diagnostic** — Built-in smoke test that exercises the full create/read/edit/complete/trash cycle.

## Infrastructure notes

- The server scales to zero when idle and auto-starts on the first request (cold start takes a few seconds)
- SQLite with WAL mode is stored on a persistent Fly.io volume at `/data`
- The server syncs incrementally from Things Cloud before every read, so changes made in the Things app are immediately visible
- Writes use event-sourced sync with automatic retry on 409 conflicts (race with the Things app)

## Privacy and credentials

The server needs your Things Cloud email and password to sync your tasks. Since you're deploying this on your own Fly.io account, your credentials are stored as encrypted secrets on infrastructure you control — they're not shared with anyone. Every data route requires an OAuth access token issued to you (see [`deployment/vps.md`](deployment/vps.md)).

## Cost

Fly.io doesn't bill you if your monthly usage is under $5. The server scales to zero when idle and uses minimal resources when running (shared CPU, 1 GB RAM). After 2+ months of active development and daily use, the author has not been billed.

## Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `THINGS_USERNAME` | Yes | Your Things account email |
| `THINGS_PASSWORD` | Yes | Your Things account password |
| `OAUTH_ISSUER`, `MCP_PUBLIC_URL`, `OAUTH_ALLOWED_SUBJECT` | Yes | OAuth settings, see [`deployment/vps.md`](deployment/vps.md) |
| `ENABLE_REST_API` | No | `true` serves `/api/*` behind the same tokens as `/mcp` |
| `PORT` | No | Server port (default: `8080`) |
| `DATA_DIR` | No | Directory of the SQLite cache `things-upstream.db` (default: `/data`) |
| `ENABLE_WRITES` | No | `true` enables the write tools and endpoints; read-only otherwise |

## Updating

```bash
git pull
fly deploy
```

## Troubleshooting

**Server won't start:** Check your credentials with `fly logs`. The most common issue is incorrect Things Cloud credentials.

**Sync errors:** The server retries sync automatically. If issues persist, check `fly logs` for details. With `ENABLE_REST_API=true` you can trigger a manual sync via `curl -H "Authorization: Bearer <access token>" https://your-app-name.fly.dev/api/sync`.

**Cold start latency:** The first request after idle may take a few seconds as Fly.io starts the machine and the server performs an initial sync. Subsequent requests are fast.
