#!/usr/bin/env python3
"""Read-only Docker rollback probe; sandbox credentials, Linux disposable runner only."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


class ProbeError(Exception):
    pass


def require(ok, message):
    if not ok:
        raise ProbeError(message)


def docker(*args, env=None):
    result = subprocess.run(["docker", *args], env=env, capture_output=True, text=True)
    # Docker diagnostics or server logs may contain credentials/account data.
    require(result.returncode == 0, "Docker operation failed: " + args[0])
    return result.stdout.strip()


class MCP:
    def __init__(self, port):
        self.url = f"http://127.0.0.1:{port}"
        self.session = None
        self.serial = 0
        self.rpc("initialize", {"protocolVersion": "2025-03-26", "capabilities": {},
                                "clientInfo": {"name": "sandbox-rollback", "version": "1"}})
        self.rpc("notifications/initialized", {}, notification=True)

    def rpc(self, method, params, notification=False):
        self.serial += 1
        body = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notification:
            body["id"] = self.serial
        headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
        if self.session:
            headers["Mcp-Session-Id"] = self.session
        request = urllib.request.Request(self.url + "/mcp", json.dumps(body).encode(), headers)
        with urllib.request.urlopen(request, timeout=180) as response:
            self.session = response.headers.get("Mcp-Session-Id", self.session)
            raw = response.read().decode()
        if notification:
            return None
        if raw.startswith("event:") or raw.startswith("data:"):
            raw = next(line[5:].strip() for line in raw.splitlines() if line.startswith("data:"))
        value = json.loads(raw)
        require("error" not in value, "MCP protocol error")
        return value["result"]

    def call(self, name, arguments):
        require(name.startswith("things_list_") or name == "things_get_task", "Non-read tool refused")
        result = self.rpc("tools/call", {"name": name, "arguments": arguments})
        require(not result.get("isError"), "Read tool returned an error: " + name)
        return json.loads(result["content"][0]["text"])


def collection(client, tool, modern):
    out, args = [], {}
    while True:
        page = client.call(tool, args)
        if not modern:
            require(isinstance(page, list), "Rollback did not restore array responses")
            return page
        require(isinstance(page, dict) and "items" in page, "Candidate page contract missing")
        out.extend(page["items"])
        require(page["returned"] == len(page["items"]), "Page count mismatch")
        if not page["has_more"]:
            require(len(out) == page["total"], "Incomplete page traversal")
            require(len({item["uuid"] for item in out}) == len(out), "Duplicate page items")
            return out
        require(page["returned"] > 0, "Pagination made no progress")
        args = {"offset": page["next_offset"], "revision": page["revision"]}


def fingerprint(database):
    tables = ["areas", "tags", "tasks", "checklist_items", "task_tags", "area_tags"]
    with sqlite3.connect(f"file:{database}?mode=ro", uri=True) as connection:
        require(connection.execute("pragma integrity_check").fetchone()[0] == "ok", "SQLite integrity check failed")
        digest, counts = hashlib.sha256(), {}
        for table in tables:
            rows = connection.execute(f'SELECT * FROM "{table}"').fetchall()
            # Sort encoded rows, so query order is irrelevant; retain every data column.
            encoded = sorted(json.dumps(row, ensure_ascii=True, separators=(",", ":")) for row in rows)
            digest.update(json.dumps([table, encoded], separators=(",", ":")).encode())
            counts[table] = len(rows)
        schema = connection.execute("SELECT name,sql FROM sqlite_master WHERE type='table' ORDER BY name").fetchall()
        digest.update(json.dumps(schema, separators=(",", ":")).encode())
    return {"logical_sha256": digest.hexdigest(), "row_counts": counts}


def wait_ready(port, container, timeout=600):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        require(docker("inspect", "--format", "{{.State.Running}}", container) == "true", "Container exited before readiness")
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=3) as response:
                return json.load(response)
        except (urllib.error.URLError, TimeoutError, OSError):
            time.sleep(3)
    raise ProbeError("Readiness timed out")


def run(args):
    require(os.geteuid() == 0, "Run on a disposable Linux runner as root")
    require(bool(os.environ.get("THINGS_SANDBOX_USERNAME")) and bool(os.environ.get("THINGS_SANDBOX_PASSWORD")), "Sandbox credentials missing")
    require(not os.environ.get("THINGS_USERNAME") and not os.environ.get("THINGS_PASSWORD"), "Refusing inherited non-sandbox credentials")
    env = dict(os.environ, THINGS_USERNAME=os.environ["THINGS_SANDBOX_USERNAME"],
               THINGS_PASSWORD=os.environ["THINGS_SANDBOX_PASSWORD"])
    phases = []
    with tempfile.TemporaryDirectory(prefix="things-sandbox-rollback-") as root:
        data = Path(root) / "data"
        data.mkdir(mode=0o700)
        os.chown(data, 65532, 65532)
        baseline, expected_ids, expected_task = None, None, None
        container = "things-sandbox-rollback-" + str(os.getpid())
        for label, image, modern in [("candidate", args.new_image, True), ("rollback", args.old_image, False), ("reupgrade", args.new_image, True)]:
            try:
                docker("run", "-d", "--name", container, "--network", "host", "--cap-drop", "ALL",
                       "--security-opt", "no-new-privileges:true", "--mount", f"type=bind,source={data},target=/data",
                       "-e", "THINGS_USERNAME", "-e", "THINGS_PASSWORD", "-e", "ENABLE_WRITES=false",
                       "-e", "INSECURE_NO_AUTH_LOCAL_ONLY=true", "-e", "THINGS_TIMEZONE=UTC",
                       "-e", "PORT=18346", image, env=env)
                health = wait_ready(18346, container)
                require(health["version"] == ("v2.0.1" if not modern else args.new_version), "Unexpected running image version")
                mcp = MCP(18346)
                tools = {t["name"]: t for t in mcp.rpc("tools/list", {})["tools"]}
                require(all(t.startswith("things_list_") or t.startswith("things_get_") or t == "things_search_tasks" for t in tools), "Write tools exposed")
                revision = tools["things_list_today"]["inputSchema"]["properties"].get("revision")
                require((revision is not None) == modern and ("things_get_task_text" in tools) == modern, "Schema version mismatch")
                areas = collection(mcp, "things_list_areas", modern)
                require(any(a["title"] == "MCP E2E SANDBOX" for a in areas), "Sandbox marker missing")
                tasks = collection(mcp, "things_list_all_tasks", modern)
                ids = sorted(t["uuid"] for t in tasks)
                seed = next((t for t in tasks if t["title"] == "SEED anytime"), None)
                require(seed is not None, "Expected sandbox seed missing")
                detail = mcp.call("things_get_task", {"uuid": seed["uuid"]})
                if expected_ids is None:
                    expected_ids, expected_task = ids, detail
                else:
                    require(ids == expected_ids, "Task selection changed across rollback")
                    require(detail == expected_task, "Seed detail changed across rollback")
                docker("stop", "--time", "30", container)
                snapshot = fingerprint(data / "things-upstream.db")
                if baseline is None:
                    baseline = snapshot
                else:
                    require(snapshot == baseline, "Mirror schema or logical data changed across rollback")
                require(not (data / "things-upstream.db.before-rebuild").exists(), "Mirror unexpectedly rebuilt")
                phases.append({"phase": label, "version": health["version"], "image_id": docker("inspect", "--format", "{{.Image}}", container),
                               "contract": "pages" if modern else "arrays", "tool_count": len(tools), "task_count": len(ids),
                               "mirror": snapshot, "result": "passed"})
                print(label + ": passed (read-only tools, sandbox marker, schemas, data and SQLite integrity)", flush=True)
            finally:
                # Never print container logs, which may include private account URLs.
                docker("rm", "-f", container)
    return {"result": "passed", "read_only": True, "phases": phases,
            "scope": "Docker image rollback on one disposable sandbox mirror; no OAuth/ChatGPT/VPS rollback"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--old-image", required=True)
    parser.add_argument("--new-image", required=True)
    parser.add_argument("--new-version", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    try:
        result = run(args)
    except Exception as error:
        # Only deliberate diagnostics are safe for public logs; suppress raw transport errors.
        result = {"result": "failed", "error": str(error) if isinstance(error, ProbeError) else type(error).__name__}
    Path(args.output).write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result), flush=True)
    return 0 if result["result"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
