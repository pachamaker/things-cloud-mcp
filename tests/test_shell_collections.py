"""Exercise real shell helpers with a fake curl executable; no sockets or credentials."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

TESTS = Path(__file__).resolve().parent


class ShellCollectionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.addCleanup(self.temp.cleanup)
        curl = self.root / "curl"
        curl.write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
args=sys.argv[1:]
for key in ('--data-raw', '--data-binary'):
    if key in args:
        request=json.loads(args[args.index(key)+1]); break
params=request['params']; offset=params['arguments'].get('offset',0)
with open(os.environ['CALLS'], 'a') as f: f.write(json.dumps(params)+'\\n')
response=json.loads(Path(os.environ['FIXTURE']).read_text())[str(offset)]
print(json.dumps(response))
''')
        curl.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.root) + os.pathsep + os.environ['PATH'],
                        FIXTURE=str(self.root / 'fixture.json'), CALLS=str(self.root / 'calls.jsonl'))

    @staticmethod
    def page(items, more=False, revision="fixture-revision"):
        return {"jsonrpc": "2.0", "id": 1, "result": {"content": [{"type": "text", "text": json.dumps({
            "items": items, "returned": len(items), "total": 2, "has_more": more,
            "next_offset": 1 if more else None, "revision": revision})}]}}

    def shell(self, code, fixture):
        (self.root / 'fixture.json').write_text(json.dumps(fixture))
        return subprocess.run(['bash', '-c', 'set -euo pipefail; source "$1"; ' + code,
                               'test', str(TESTS / 'sandbox-guard.sh')], env=self.env,
                              capture_output=True, text=True, timeout=20)

    def calls(self):
        return [json.loads(x) for x in (self.root / 'calls.jsonl').read_text().splitlines()]

    def test_marker_on_second_page(self):
        result = self.shell('require_sandbox_mcp http://fixture.invalid/mcp', {
            '0': self.page([{'uuid':'a','title':'Other'}], True),
            '1': self.page([{'uuid':'b','title':'MCP E2E SANDBOX'}]),
        })
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls()), 2)
        self.assertEqual(self.calls()[1]['arguments'], {'offset':1, 'revision':'fixture-revision'})

    def test_task_on_second_page_and_filters_preserved(self):
        result = self.shell("mcp_collection_has_uuid http://fixture.invalid/mcp things_list_project_tasks target '{\"project_uuid\":\"project\"}'", {
            '0': self.page([{'uuid':'other'}], True), '1': self.page([{'uuid':'target'}]),
        })
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'true')
        self.assertEqual(self.calls()[1]['arguments'], {'project_uuid':'project','offset':1,'revision':'fixture-revision'})

    def test_marker_absent_refuses(self):
        result = self.shell('require_sandbox_mcp http://fixture.invalid/mcp', {
            '0': self.page([{'uuid':'a','title':'Other'}], True), '1': self.page([{'uuid':'b','title':'Still other'}]),
        })
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Refusing to write', result.stderr)

    def test_stale_second_page_refuses_even_if_marker_on_first(self):
        result = self.shell('require_sandbox_mcp http://fixture.invalid/mcp', {
            '0': self.page([{'uuid':'a','title':'MCP E2E SANDBOX'}], True),
            '1': {'result': {'isError': True, 'content':[{'type':'text','text':'{"error":{"code":"stale_page"}}'}]}},
        })
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Refusing to write', result.stderr)

    def test_nonprogressing_page_refuses(self):
        page = self.page([{'uuid':'a','title':'Other'}], True)
        value=json.loads(page['result']['content'][0]['text']); value['next_offset']=0
        page['result']['content'][0]['text']=json.dumps(value)
        result=self.shell('require_sandbox_mcp http://fixture.invalid/mcp', {'0':page})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.calls()), 1)

    def test_changed_revision_refuses(self):
        result=self.shell('require_sandbox_mcp http://fixture.invalid/mcp', {
            '0':self.page([{'uuid':'a','title':'Other'}],True),
            '1':self.page([{'uuid':'b','title':'MCP E2E SANDBOX'}],revision='changed'),
        })
        self.assertNotEqual(result.returncode, 0)


    def test_missing_task_returns_false_after_all_pages(self):
        result = self.shell('mcp_collection_has_uuid http://fixture.invalid/mcp things_list_today absent', {
            '0': self.page([{'uuid':'a'}], True), '1': self.page([{'uuid':'b'}]),
        })
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'false')
        self.assertEqual(len(self.calls()), 2)

    def test_duplicate_uuid_refuses_guard(self):
        result = self.shell('require_sandbox_mcp http://fixture.invalid/mcp', {
            '0': self.page([{'uuid':'same','title':'Other'}], True),
            '1': self.page([{'uuid':'same','title':'MCP E2E SANDBOX'}]),
        })
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.calls()), 2)

    def test_smoke_helper_accepts_pages_and_rejects_arrays_or_errors(self):
        # Source the real helper definitions without executing the write section.
        helpers = (TESTS / 'test-smoke.sh').read_text().split('# --- smoke tests ---')[0]
        env = dict(self.env, BASE_URL='http://fixture.invalid')
        for value, expected in [({'items':[]}, 'true'), ([], 'false'),
                                ({'error':{'code':'stale_page'}}, 'false')]:
            with self.subTest(value=value):
                result = subprocess.run(['bash', '-c', helpers + '\nis_page "$1"',
                                         'test', json.dumps(value)], env=env,
                                        capture_output=True, text=True, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.strip(), expected)


if __name__ == '__main__':
    unittest.main()
