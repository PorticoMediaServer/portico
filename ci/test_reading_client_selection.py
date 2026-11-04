import json
from pathlib import Path
import unittest
from unittest.mock import patch

from plan import ROOT, go_matrix, make_plan
import source_readers


class ReadingClientSelection(unittest.TestCase):
    def test_client_and_reader_changes_select_the_tagged_http_boundary(self):
        for path in ['packages/client-core/src/index.ts', 'packages/reader-core/src/index.ts',
                     'packages/client-core/test/reading-http.integration.mjs']:
            selected = source_readers.select([path])
            self.assertIn('TestReadingHTTPClientPublication', selected.get('server/internal/httpapi', {}), path)

    def test_tag_is_enabled_when_listing_the_actual_test_inventory(self):
        def command(args, cwd=ROOT):
            if args[0] == 'python3':
                return 'portico_rar7z,portico_cb7'
            if Path(cwd).name != 'server':
                return ''
            files = ['reading_client_integration_test.go'] if 'reading_client_integration' in args[args.index('-tags') + 1] else []
            return json.dumps({'ImportPath': 'github.com/PorticoMediaServer/portico/server/internal/httpapi',
                               'Dir': str(ROOT / 'server/internal/httpapi'), 'TestGoFiles': files})
        with patch('plan.command', side_effect=command), patch('plan.components', return_value=([], False)):
            plan = make_plan(['packages/client-core/test/reading-http.integration.mjs'])
        self.assertIn('reading_client_integration', plan['tags'].split(','))
        self.assertIn('TestReadingHTTPClientPublication', [t['name'] for t in plan['inventory']])

    def test_node_setup_is_selected_only_for_the_owning_shard(self):
        plan = {'shards': [[{'name': 'TestOrdinary'}], [{'name': 'TestReadingHTTPClientPublication'}]]}
        self.assertEqual(go_matrix(plan), {'include': [{'index': 0}, {'index': 1, 'reading_client': True}]})

    def test_dependency_setup_builds_contracts_at_its_own_package_directory(self):
        import os, re, subprocess, tempfile, textwrap
        for filename in ['merge.yml', 'nightly.yml']:
            text = (ROOT / '.github/workflows' / filename).read_text()
            blocks = re.findall(r'^      - name: Install real reading-client integration dependencies\n.*?        run: \|\n((?:          [^\n]*\n)+)', text, re.M | re.S)
            self.assertEqual(len(blocks), 1 if filename == 'merge.yml' else 2)
            for block in blocks:
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory); contracts = root / 'packages/contracts'; contracts.mkdir(parents=True)
                    tools = root / 'bin'; tools.mkdir(); trace = root / 'commands.txt'
                    npm = tools / 'npm'
                    npm.write_text('#!/bin/sh\nprintf "%s|%s\\n" "$PWD" "$*" >> "$TRACE"\ncase "$*" in *--workspace*) exit 64;; esac\n')
                    npm.chmod(0o700)
                    env = dict(os.environ, PATH=str(tools) + ':' + os.environ['PATH'], TRACE=str(trace))
                    subprocess.run(['bash', '-ec', textwrap.dedent(block)], cwd=root, env=env, check=True)
                    lines = trace.read_text().splitlines()
                    self.assertEqual(len(lines), 2)
                    self.assertTrue(lines[0].startswith(str(root) + '|ci '))
                    self.assertEqual(lines[1], str(contracts) + '|run build')
