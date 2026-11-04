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
