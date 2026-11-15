#!/usr/bin/env bash
# Sourced by the shell suites that write (AGENTS.md rule 1). They refuse to run
# unless the connected Things account has the sandbox marker area, the same one
# the Go sandbox harness requires (docs/testing/sandbox-e2e.md). A suite pointed
# at the owner's real server stops before its first write.

SANDBOX_MARKER_AREA="MCP E2E SANDBOX"
SANDBOX_HELPER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Materialize all pages for assertions that require the complete selection.
mcp_list_all() {
  local endpoint="$1" tool="$2" args="${3:-}"
  [ -n "$args" ] || args='{}'
  python3 "${SANDBOX_HELPER_DIR}/mcp-collection.py" "$endpoint" "$tool" "$args"
}

mcp_collection_has_uuid() {
  local endpoint="$1" tool="$2" uuid="$3" args="${4:-}" response
  response=$(mcp_list_all "$endpoint" "$tool" "$args") || return 1
  printf '%s' "$response" | python3 -c '
import json, sys
items=json.load(sys.stdin)["items"]
print("true" if any(item.get("uuid") == sys.argv[1] for item in items) else "false")
' "$uuid"
}

# has_marker_area reads the collected page items on stdin.
has_marker_area() {
  SANDBOX_MARKER_AREA="$SANDBOX_MARKER_AREA" python3 -c '
import json, os, sys
try:
    areas = json.load(sys.stdin)["items"]
    if not isinstance(areas, list) or not all(isinstance(a, dict) for a in areas):
        sys.exit(1)
except (ValueError, KeyError, TypeError):
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
  mcp_list_all "$1" things_list_areas | has_marker_area || refuse_non_sandbox
}
