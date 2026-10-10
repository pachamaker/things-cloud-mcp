#!/usr/bin/env bash
# Sourced by the shell suites that write (AGENTS.md rule 1). They refuse to run
# unless the connected Things account has the sandbox marker area, the same one
# the Go sandbox harness requires (docs/testing/sandbox-e2e.md). A suite pointed
# at the owner's real server stops before its first write.

SANDBOX_MARKER_AREA="MCP E2E SANDBOX"

# has_marker_area reads a JSON list of areas on stdin; MCP tools return
# lowercase keys, the REST API the SDK's field names.
has_marker_area() {
  SANDBOX_MARKER_AREA="$SANDBOX_MARKER_AREA" python3 -c '
import json, os, sys
try:
    areas = json.loads(sys.stdin.read()) or []
except ValueError:
    sys.exit(1)
marker = os.environ["SANDBOX_MARKER_AREA"]
sys.exit(0 if any((a.get("title") or a.get("Title")) == marker for a in areas) else 1)
'
}

refuse_non_sandbox() {
  echo "Refusing to write: the account has no area titled \"${SANDBOX_MARKER_AREA}\"." >&2
  echo "These suites create real items. Run them only against a server connected to the" >&2
  echo "sandbox account (docs/testing/sandbox-e2e.md), started with ENABLE_WRITES=true." >&2
  exit 1
}

# require_sandbox_mcp <mcp endpoint URL>
require_sandbox_mcp() {
  local resp
  resp=$(curl -s --max-time 60 "$1" \
    -X POST \
    -H "Content-Type: application/json" \
    --data-raw '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"things_list_areas","arguments":{}}}')
  printf '%s' "$resp" \
    | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['result']['content'][0]['text'])" 2>/dev/null \
    | has_marker_area || refuse_non_sandbox
}

# require_sandbox_rest <base URL> <Authorization header>
require_sandbox_rest() {
  curl -s --max-time 30 "$1/api/areas" -H "$2" | has_marker_area || refuse_non_sandbox
}
