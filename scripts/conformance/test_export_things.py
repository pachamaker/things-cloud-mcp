"""Tests of export_things.py on a synthetic Things database.

Run from the repository root with the exporter's virtual environment:

    ~/.venvs/things-conformance/bin/python -m unittest discover -s scripts/conformance -v

HOME points to an empty directory before things.py is imported, so its
database discovery cannot find, and these tests never open, a real Things
database.
"""

import datetime as dt
import json
import os
import plistlib
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from zoneinfo import ZoneInfo

_HOME = tempfile.TemporaryDirectory(prefix="export-things-test-home-")
os.environ["HOME"] = _HOME.name
os.environ.pop("THINGSDB", None)

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import export_things  # noqa: E402
import things.database  # noqa: E402

KEY = bytes(range(32))
ZONE = "Europe/Moscow"


def packed(day: dt.date) -> int:
    return (day.year << 16) | (day.month << 12) | (day.day << 7)


class PureFunctions(unittest.TestCase):
    def test_discovery_cannot_reach_a_real_database(self):
        self.assertTrue(things.database.DEFAULT_FILEPATH.startswith(_HOME.name))

    def test_title_hash_matches_the_go_side(self):
        # The same vectors are in server/conformance_unit_test.go.
        self.assertEqual(export_things.title_hash(KEY, "SEED anytime"), "76b5488b7e83e880")
        self.assertEqual(export_things.title_hash(KEY, "Ünïcødé — задача"), "5ea6b9d96912e761")
        self.assertEqual(export_things.title_hash(KEY, None), "d38b42096d80f45f")
        self.assertEqual(export_things.key_id(KEY), "630dcd29")

    def test_things_day(self):
        self.assertEqual(export_things.things_day(132464128), "2021-03-28")
        self.assertEqual(export_things.things_day(packed(dt.date(4001, 1, 1))), "4001-01-01")
        self.assertIsNone(export_things.things_day(None))
        self.assertEqual(export_things.things_day(1791763200.0), "raw:1791763200.0")
        self.assertEqual(export_things.things_day(132464129), "raw:132464129")
        self.assertEqual(export_things.things_day((2021 << 16) | (13 << 12) | (1 << 7)), f"raw:{(2021 << 16) | (13 << 12) | (1 << 7)}")


SCHEMA = """
CREATE TABLE Meta (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE TMArea (uuid TEXT PRIMARY KEY, title TEXT, visible INTEGER, "index" INTEGER);
CREATE TABLE TMTag (uuid TEXT PRIMARY KEY, title TEXT);
CREATE TABLE TMTaskTag (tasks TEXT, tags TEXT);
CREATE TABLE TMChecklistItem (uuid TEXT PRIMARY KEY, task TEXT);
CREATE TABLE TMTask (
    uuid TEXT PRIMARY KEY, type INTEGER, trashed INTEGER DEFAULT 0, title TEXT, notes TEXT,
    status INTEGER DEFAULT 0, area TEXT, project TEXT, heading TEXT, start INTEGER,
    startDate INTEGER, deadline INTEGER, reminderTime INTEGER, stopDate REAL,
    creationDate REAL, userModificationDate REAL, "index" INTEGER DEFAULT 0, todayIndex INTEGER DEFAULT 0,
    rt1_recurrenceRule BLOB, rt1_repeatingTemplate TEXT, deadlineSuppressionDate INTEGER,
    todayIndexReferenceDate INTEGER, startBucket INTEGER DEFAULT 0
);
"""


def make_database(path: Path, today: dt.date, marker: bool = True) -> None:
    yesterday, later = today - dt.timedelta(days=1), today + dt.timedelta(days=3)
    conn = sqlite3.connect(path)
    conn.executescript(SCHEMA)
    conn.execute("INSERT INTO Meta VALUES ('databaseVersion', ?)", (plistlib.dumps(26).decode(),))
    if marker:
        conn.execute("INSERT INTO TMArea (uuid, title) VALUES ('MARKER', ?)", (export_things.MARKER_AREA,))
    rows = [
        # uuid, type, start, startDate, deadline, extra columns
        ("inbox", 0, 0, None, None, {}),
        ("today", 0, 1, packed(today), None, {"todayIndexReferenceDate": packed(today)}),
        ("evening", 0, 1, packed(today), None, {"startBucket": 1}),
        ("anytime", 0, 1, None, None, {"notes": "a private note"}),
        ("upcoming", 0, 2, packed(later), None, {}),
        ("someday", 0, 2, None, None, {}),
        ("arrived", 0, 2, packed(yesterday), None, {}),
        ("inbox-overdue", 0, 0, None, packed(yesterday), {}),
        ("dismissed", 0, 1, None, packed(yesterday), {"deadlineSuppressionDate": packed(today)}),
        ("completed", 0, 1, None, None, {"status": 3, "stopDate": 1.7e9}),
        ("canceled", 0, 1, None, None, {"status": 2, "stopDate": 1.7e9}),
        ("trashed", 0, 1, None, None, {"trashed": 1}),
        ("template", 0, 2, None, None, {"rt1_recurrenceRule": b"<plist/>"}),
        ("instance", 0, 1, packed(today), None, {"rt1_repeatingTemplate": "template"}),
        ("project", 1, 1, None, None, {}),
        ("heading", 2, 1, None, None, {"project": "project"}),
        ("gone-project", 1, 1, None, None, {"trashed": 1}),
        ("in-gone-project", 0, 1, None, None, {"project": "gone-project"}),
    ]
    for uuid, typ, start, start_date, deadline, extra in rows:
        cols = {"uuid": uuid, "type": typ, "title": f"title of {uuid}", "start": start, "startDate": start_date, "deadline": deadline, **extra}
        conn.execute(
            f"INSERT INTO TMTask ({', '.join(cols)}) VALUES ({', '.join('?' * len(cols))})",
            tuple(cols.values()),
        )
    conn.commit()
    conn.close()


class Export(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="export-things-test-")
        self.dir = Path(self.tmp.name)
        self.key_file = self.dir / "key"
        self.key_file.write_text(KEY.hex() + "\n")
        self.db = self.dir / "Things Database.thingsdatabase" / "main.sqlite"
        self.db.parent.mkdir()
        self.out = self.dir / "snapshot"
        self.today = dt.datetime.now(ZoneInfo(ZONE)).date()

    def tearDown(self):
        self.tmp.cleanup()

    def run_export(self):
        env = {**os.environ, "TZ": ZONE, "HOME": _HOME.name}
        return subprocess.run(
            [sys.executable, "-I", str(HERE / "export_things.py"), "--out", str(self.out),
             "--title-key-file", str(self.key_file), "--database", str(self.db)],
            capture_output=True, text=True, env=env, check=False,
        )

    def test_views_and_fields(self):
        make_database(self.db, self.today)
        result = self.run_export()
        self.assertEqual(result.returncode, 0, result.stderr)
        raw = (self.out / "expected.json").read_text(encoding="utf-8")
        snap = json.loads(raw)
        if snap["today"] != self.today.isoformat():
            self.skipTest("the test ran across midnight")
        self.assertNotIn("title of", raw)
        self.assertNotIn("private note", raw)
        self.assertEqual(snap["timezone"], ZONE)
        self.assertEqual(snap["format"], 1)
        self.assertEqual(snap["title_key_id"], "630dcd29")
        self.assertEqual(snap["marker_area"], "MARKER")
        self.assertEqual(snap["database_version"], 26)

        tasks = {t["uuid"]: t for t in snap["tasks"]}
        views = {uuid: t["views"] for uuid, t in tasks.items()}
        self.assertEqual(views, {
            "inbox": ["inbox"],
            "today": ["today", "anytime"],
            "evening": ["today", "anytime"],
            "anytime": ["anytime"],
            "upcoming": ["upcoming"],
            "someday": ["someday"],
            "arrived": ["today"],
            "inbox-overdue": ["inbox", "today"],
            "dismissed": ["anytime"],
            "completed": ["logbook"],
            "canceled": ["logbook"],
            "trashed": ["trash"],
            "template": [],
            "instance": ["today", "anytime"],
            "project": ["anytime"],
            "gone-project": ["trash"],
            "in-gone-project": [],
        })
        self.assertEqual([u for u, t in tasks.items() if t["evening"]], ["evening"])
        self.assertEqual(tasks["project"]["type"], "project")
        self.assertEqual(tasks["anytime"]["title_hash"], export_things.title_hash(KEY, "title of anytime"))
        self.assertEqual(tasks["today"]["app"], {
            "tp": 0, "ss": 0, "tr": False, "st": 1, "sr": self.today.isoformat(), "tir": self.today.isoformat(),
            "dd": None, "dds": None, "sb": 0, "rr": False, "rt": "", "pr": "", "agr": "",
        })
        self.assertTrue(tasks["template"]["app"]["rr"])
        self.assertEqual(tasks["instance"]["app"]["rt"], "template")
        self.assertEqual(tasks["dismissed"]["app"]["dds"], self.today.isoformat())
        self.assertEqual(tasks["in-gone-project"]["app"]["pr"], "gone-project")

        again = self.run_export()
        self.assertNotEqual(again.returncode, 0)
        self.assertIn("each snapshot is taken once", again.stderr)

    def test_refuses_a_database_without_the_marker_area(self):
        make_database(self.db, self.today, marker=False)
        result = self.run_export()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sandbox account only", result.stderr)
        self.assertFalse((self.out / "expected.json").exists())

    def test_refuses_a_bad_key(self):
        make_database(self.db, self.today)
        self.key_file.write_text("00")
        result = self.run_export()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("title key", result.stderr)

    def test_refuses_a_missing_database(self):
        result = self.run_export()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no Things database", result.stderr)


if __name__ == "__main__":
    unittest.main()
