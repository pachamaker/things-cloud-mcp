#!/usr/bin/env python3
"""Export the Things app's own views for the ADR-0005 conformance harness.

Reads the local Things 3 database read-only and writes <out>/expected.json:
per to-do and project its UUID, a keyed hash of its title, the views
things.py lists it in, the This Evening flag, and the classification inputs
as the app's database stores them. Notes, tags, area and checklist titles do
not leave the database. Usually run by snapshot.sh; see
docs/testing/conformance.md.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import hmac
import json
import os
import plistlib
import sqlite3
import subprocess
import sys
import tempfile
import urllib.parse
from pathlib import Path
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

import things
import things.database

FORMAT = 1
MARKER_AREA = "MCP E2E SANDBOX"
THINGS_APP_PLIST = Path("/Applications/Things3.app/Contents/Info.plist")
TYPES = {0: "to-do", 1: "project"}
# things.py's view functions, under the names expected.json uses.
VIEWS = ("inbox", "today", "anytime", "someday", "upcoming", "logbook", "trash")
# Columns things.py does not expose, read beside it. A column this Things
# version lacks reads as NULL.
EXTRA_COLUMNS = (
    "todayIndexReferenceDate",
    "deadlineSuppressionDate",
    "startBucket",
    "rt1_recurrenceRule",
    "rt1_repeatingTemplate",
)


class ExportError(Exception):
    """A snapshot that must not be written."""


def read_title_key(path: Path) -> bytes:
    key = bytes.fromhex(path.read_text().strip())
    if len(key) != 32:
        raise ExportError("title key: want 32 bytes as hex")
    return key


def title_hash(key: bytes, title: str | None) -> str:
    """HMAC-SHA256 of the UTF-8 title, first 8 bytes as hex (Go: conformanceTitleHashOf)."""
    return hmac.new(key, (title or "").encode("utf-8"), hashlib.sha256).hexdigest()[:16]


def key_id(key: bytes) -> str:
    return hashlib.sha256(key).hexdigest()[:8]


def things_day(value) -> str | None:
    """Decode a Things date: YYYYYYYYYYYMMMMDDDDD0000000 in binary.

    Anything else is kept as "raw:<value>" so a reviewer sees it.
    """
    if value is None:
        return None
    if isinstance(value, int) and value > 0 and value & 0x7F == 0:
        year, month, day = value >> 16, (value >> 12) & 0xF, (value >> 7) & 0x1F
        try:
            return dt.date(year, month, day).isoformat()
        except ValueError:
            pass
    return f"raw:{value}"


def local_zone() -> str:
    """The IANA name of this process's local zone, which SQLite's 'localtime' uses."""
    name = os.environ.get("TZ", "").lstrip(":")
    if not name:
        target = os.readlink("/etc/localtime")
        if "zoneinfo/" not in target:
            raise ExportError(f"cannot name the local time zone from /etc/localtime -> {target}")
        name = target.split("zoneinfo/", 1)[1]
    try:
        zone = ZoneInfo(name)
    except (ZoneInfoNotFoundError, ValueError) as err:
        raise ExportError(f"local time zone {name!r} is not an IANA name") from err
    now = dt.datetime.now(dt.timezone.utc)
    if now.astimezone(zone).utcoffset() != now.astimezone().utcoffset():
        raise ExportError(f"the local offset differs from {name}'s; set TZ to an IANA name")
    return name


def read_only(path: Path) -> sqlite3.Connection:
    return sqlite3.connect("file:" + urllib.parse.quote(str(path)) + "?mode=ro", uri=True)


def consistent_copy(source: Path, target: Path) -> None:
    """Copy the database with SQLite's backup API: one consistent state, even while Things writes."""
    if not source.is_file():
        raise ExportError(f"no Things database at {source}")
    src = read_only(source)
    dst = sqlite3.connect(target)
    try:
        src.backup(dst)
    finally:
        src.close()
        dst.close()


def app_version() -> str:
    try:
        with THINGS_APP_PLIST.open("rb") as f:
            return plistlib.load(f).get("CFBundleShortVersionString", "unknown")
    except OSError:
        return "unknown"


def app_running() -> bool:
    return subprocess.run(["pgrep", "-x", "Things3"], capture_output=True, check=False).returncode == 0


def marker_area(conn: sqlite3.Connection) -> str:
    rows = conn.execute("SELECT uuid FROM TMArea WHERE title = ?", (MARKER_AREA,)).fetchall()
    if len(rows) != 1:
        raise ExportError(
            f"this Things database has {len(rows)} areas titled {MARKER_AREA!r}, want 1: "
            "snapshots are taken on the sandbox account only"
        )
    return rows[0][0]


def app_rows(conn: sqlite3.Connection) -> list[dict]:
    present = {row[1] for row in conn.execute("PRAGMA table_info(TMTask)")}
    extra = ", ".join(c if c in present else f"NULL AS {c}" for c in EXTRA_COLUMNS)
    cursor = conn.execute(
        "SELECT uuid, type, title, status, trashed, start, startDate, deadline, project, heading, "
        f"{extra} FROM TMTask WHERE type IN (0, 1) ORDER BY uuid"
    )
    names = [d[0] for d in cursor.description]
    return [dict(zip(names, row)) for row in cursor.fetchall()]


def view_members(db: things.database.Database) -> dict[str, set[str]]:
    return {view: {task["uuid"] for task in getattr(things, view)(database=db)} for view in VIEWS}


def task_entry(row: dict, views: dict[str, set[str]], key: bytes) -> dict:
    uuid = row["uuid"]
    in_views = [v for v in VIEWS if uuid in views[v]]
    bucket = row["startBucket"]
    return {
        "uuid": uuid,
        "type": TYPES[row["type"]],
        "title_hash": title_hash(key, row["title"]),
        "views": in_views,
        "evening": "today" in in_views and bucket == 1,
        "app": {
            "tp": row["type"],
            "ss": row["status"],
            "tr": bool(row["trashed"]),
            "st": row["start"],
            "sr": things_day(row["startDate"]),
            "tir": things_day(row["todayIndexReferenceDate"]),
            "dd": things_day(row["deadline"]),
            "dds": things_day(row["deadlineSuppressionDate"]),
            "sb": bucket,
            "rr": row["rt1_recurrenceRule"] is not None,
            "rt": row["rt1_repeatingTemplate"] or "",
            "pr": row["project"] or "",
            "agr": row["heading"] or "",
        },
    }


def export(source: Path, key: bytes, zone_name: str) -> dict:
    zone = ZoneInfo(zone_name)
    before = dt.datetime.now(zone)
    with tempfile.TemporaryDirectory(prefix="things-conformance-") as tmp:
        copy = Path(tmp) / "main.sqlite"
        consistent_copy(source, copy)
        conn = read_only(copy)
        try:
            sqlite_today = conn.execute("SELECT date('now', 'localtime')").fetchone()[0]
            marker = marker_area(conn)
            rows = app_rows(conn)
        finally:
            conn.close()
        db = things.database.Database(filepath=str(copy))
        views = view_members(db)
        version = db.get_version()
        db.connection.close()
    after = dt.datetime.now(zone)
    today = before.date().isoformat()
    if after.date() != before.date() or sqlite_today != today:
        raise ExportError("the export straddled midnight, or SQLite's local day differs from the zone's; take it again")
    return {
        "format": FORMAT,
        "source": f"things.py {things.__version__} over a consistent copy of the Things 3 database (read-only)",
        "things_version": app_version(),
        "database_version": version,
        "app_running": app_running(),
        "exported_at": before.isoformat(timespec="seconds"),
        "today": today,
        "timezone": zone_name,
        "title_key_id": key_id(key),
        "marker_area": marker,
        "tasks": [task_entry(row, views, key) for row in rows],
    }


def encode(snapshot: dict) -> str:
    """One task per line, like the Go side's input.json."""
    head = {k: v for k, v in snapshot.items() if k != "tasks"}
    lines = [f"  {json.dumps(k)}: {json.dumps(head[k], ensure_ascii=False)}," for k in sorted(head)]
    tasks = ",\n".join("    " + json.dumps(t, ensure_ascii=False, separators=(",", ":")) for t in snapshot["tasks"])
    return "{\n" + "\n".join(lines) + '\n  "tasks": [\n' + tasks + "\n  ]\n}\n"


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--out", required=True, type=Path, help="snapshot directory; expected.json must not exist yet")
    parser.add_argument("--title-key-file", required=True, type=Path, help="the snapshot's one-off title key, 32 bytes as hex")
    parser.add_argument("--database", type=Path, help="Things database (default: things.py's discovery, THINGSDB)")
    args = parser.parse_args(argv)

    out = args.out / "expected.json"
    try:
        if out.exists():
            raise ExportError(f"{out} exists: each snapshot is taken once")
        source = args.database or Path(os.environ.get(things.database.ENVIRONMENT_VARIABLE_WITH_FILEPATH) or things.database.DEFAULT_FILEPATH)
        snapshot = export(source, read_title_key(args.title_key_file), local_zone())
    except (ExportError, OSError, ValueError, sqlite3.Error) as err:
        print(f"export_things: {err}", file=sys.stderr)
        return 1
    args.out.mkdir(parents=True, exist_ok=True)
    out.write_text(encode(snapshot), encoding="utf-8")

    counts = {v: sum(v in t["views"] for t in snapshot["tasks"]) for v in VIEWS}
    evening = sum(t["evening"] for t in snapshot["tasks"])
    print(
        f"wrote {out}: {len(snapshot['tasks'])} to-dos and projects, day {snapshot['today']} in {snapshot['timezone']}, "
        f"Things {snapshot['things_version']} {'running' if snapshot['app_running'] else 'not running'}; "
        + ", ".join(f"{v} {n}" for v, n in counts.items())
        + f", this evening {evening}",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
