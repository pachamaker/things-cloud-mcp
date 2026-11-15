#!/usr/bin/env python3
"""Collect one MCP list for shell assertions; abort rather than use partial data."""
import json
import subprocess
import sys
import time


def collect(endpoint, tool, filters):
    if not tool.startswith('things_list_') or not isinstance(filters, dict):
        raise ValueError('Expected list tool and filter object')
    items, seen, offset, revision, total = [], set(), 0, None, None
    args = dict(filters)
    if 'offset' in args or 'revision' in args:
        raise ValueError('Collection must start at offset zero')
    while True:
        request = {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call',
                   'params': {'name': tool, 'arguments': args}}
        response = subprocess.run(['curl', '-fsS', '--max-time', '60', endpoint,
                                   '-X', 'POST', '-H', 'Content-Type: application/json',
                                   '-H', 'Accept: application/json, text/event-stream',
                                   '--data-raw', json.dumps(request)], capture_output=True, text=True)
        if response.returncode:
            raise ValueError('Collection transport failed')
        try:
            envelope = json.loads(response.stdout)
        except json.JSONDecodeError:
            envelope = next(json.loads(line[5:].strip()) for line in response.stdout.splitlines()
                            if line.startswith('data:') and json.loads(line[5:].strip()).get('id') == 1)
        result = envelope['result']
        if result.get('isError'):
            raise ValueError('Collection tool failed; restart the test')
        page = json.loads(result['content'][0]['text'])
        batch = page['items']
        if (not isinstance(batch, list) or not all(isinstance(x, dict) and isinstance(x.get('uuid'), str) for x in batch)
                or type(page['returned']) is not int or page['returned'] != len(batch)
                or type(page['total']) is not int or page['total'] < 0
                or type(page['has_more']) is not bool or not isinstance(page['revision'], str) or not page['revision']):
            raise ValueError('Invalid collection page')
        if revision is None:
            revision, total = page['revision'], page['total']
        if page['revision'] != revision or page['total'] != total:
            raise ValueError('Collection changed; restart the test')
        for item in batch:
            if item['uuid'] in seen:
                raise ValueError('Duplicate collection item')
            seen.add(item['uuid'])
            items.append(item)
        if len(items) > total:
            raise ValueError('Collection exceeds total')
        if not page['has_more']:
            if len(items) != total or page['next_offset'] is not None:
                raise ValueError('Incomplete collection')
            return {'items': items}
        next_offset = page['next_offset']
        if (not batch or type(next_offset) is not int or next_offset != offset + len(batch)
                or next_offset >= total):
            raise ValueError('Collection pagination made no progress')
        offset = next_offset
        args = dict(filters, offset=offset, revision=revision)
        time.sleep(1)  # Respect cloud request pacing, as the shell suites do.


def main():
    try:
        endpoint, tool, raw = sys.argv[1:]
        print(json.dumps(collect(endpoint, tool, json.loads(raw))))
        return 0
    except (ValueError, KeyError, TypeError, AttributeError, IndexError, StopIteration, OSError):
        # Raw HTTP/tool responses may contain task data; fail closed without echoing them.
        print('Cannot read complete MCP collection; refusing partial results.', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
