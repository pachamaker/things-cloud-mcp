import importlib.util
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import Mock

spec = importlib.util.spec_from_file_location("rollback", Path(__file__).with_name("sandbox-rollback.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class RollbackProbeTests(unittest.TestCase):
    def test_pages_continue_with_revision_and_offset(self):
        client = Mock()
        client.call.side_effect = [
            {"items": [{"uuid": "a"}], "returned": 1, "total": 2, "has_more": True, "next_offset": 1, "revision": "rev"},
            {"items": [{"uuid": "b"}], "returned": 1, "total": 2, "has_more": False},
        ]
        self.assertEqual(probe.collection(client, "things_list_all_tasks", True), [{"uuid": "a"}, {"uuid": "b"}])
        self.assertEqual(client.call.call_args_list[1].args[1], {"offset": 1, "revision": "rev"})

    def test_duplicate_or_incomplete_pages_fail(self):
        for uuids, total in [(["a", "a"], 2), (["a"], 2)]:
            with self.subTest(uuids=uuids, total=total):
                client = Mock()
                client.call.return_value = {"items": [{"uuid": x} for x in uuids], "returned": len(uuids), "total": total, "has_more": False}
                with self.assertRaises(probe.ProbeError):
                    probe.collection(client, "things_list_all_tasks", True)

    def test_old_server_must_return_array(self):
        client = Mock()
        client.call.return_value = {"items": []}
        with self.assertRaises(probe.ProbeError):
            probe.collection(client, "things_list_all_tasks", False)
        client.call.return_value = [{"uuid": "seed"}]
        self.assertEqual(probe.collection(client, "things_list_all_tasks", False), [{"uuid": "seed"}])

    def test_write_call_is_refused_before_transport(self):
        client = probe.MCP.__new__(probe.MCP)
        client.rpc = Mock()
        with self.assertRaises(probe.ProbeError):
            client.call("things_create_task", {})
        client.rpc.assert_not_called()

    def test_fingerprint_detects_data_and_schema_changes(self):
        with tempfile.TemporaryDirectory() as root:
            database = Path(root) / "mirror.db"
            with sqlite3.connect(database) as db:
                for name in ["areas", "tags", "tasks", "checklist_items", "task_tags", "area_tags"]:
                    db.execute(f'CREATE TABLE "{name}" (uuid TEXT, note TEXT)')
                db.execute("INSERT INTO tasks VALUES ('seed', 'synthetic note')")
                db.execute("CREATE TABLE sync_state (last_sync_at INTEGER)")
                db.execute("INSERT INTO sync_state VALUES (1)")
            first = probe.fingerprint(database)
            with sqlite3.connect(database) as db:
                db.execute("UPDATE sync_state SET last_sync_at=2")
            self.assertEqual(probe.fingerprint(database), first)
            with sqlite3.connect(database) as db:
                db.execute("UPDATE tasks SET note='changed'")
            changed = probe.fingerprint(database)
            self.assertNotEqual(changed, first)
            with sqlite3.connect(database) as db:
                db.execute("ALTER TABLE tasks ADD COLUMN new_field TEXT")
            self.assertNotEqual(probe.fingerprint(database), changed)


if __name__ == "__main__":
    unittest.main()
