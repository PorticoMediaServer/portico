from collections import Counter, defaultdict
import json
from pathlib import Path
import re
import tempfile
import unittest
from unittest.mock import patch

from nightly_lane import select
from plan import COMPONENTS, ROOT, make_plan


def keys(tests):
    return [(t['package'], t['name']) for t in tests]


class NightlyCoverage(unittest.TestCase):
    def test_scheduled_nightly_covers_every_full_merge_parent_exactly_once(self):
        tiers = json.loads((ROOT / 'ci/tiers.json').read_text())
        golden = ('github.com/PorticoMediaServer/portico/server/internal/httpapi',
                  'TestReadPathGoldenEquivalence')
        tests = {golden, (golden[0], 'TestNewCrossCuttingRegression'),
                 ('github.com/PorticoMediaServer/portico/apikit', 'TestOrdinaryAPI')}
        tests.update((t['package'], t['name']) for lane in tiers['nightly_lanes'].values() for t in lane)
        producers = {
            'TestHomeReadingSavedDeferredHTTP': 'packages/client-core/test/home-reading-wire.test.ts',
            'TestConcurrencyDiagnosticsMemoryLimitWire': 'packages/player-core/test/concurrency-memory-limit.test.ts',
            'TestConsoleRegistryProducerRecordsNullableDefaults': 'packages/client-core/test/console-registry-null-defaults.test.ts',
            'TestInvitationAcceptanceProducerContract': 'packages/contracts/test/invitation-producer-contract.test.ts',
            'TestResetFileHTTPProducerContractsAndAuthority': 'packages/contracts/test/reset-producer-contract.test.ts',
        }
        tests.update((golden[0], name) for name in producers)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'ci').mkdir()
            (root / 'ci/tiers.json').write_text(json.dumps(tiers))
            (root / 'ci/durations.json').write_text('{}')
            (root / 'ci/js-durations.json').write_text('{"seconds":{}}')
            for component in COMPONENTS:
                file = root / component / 'test/full.test.ts'
                file.parent.mkdir(parents=True, exist_ok=True)
                file.touch()
            for name in producers.values():
                file = root / name
                file.parent.mkdir(parents=True, exist_ok=True)
                file.touch()
            packages = defaultdict(list)
            grouped = defaultdict(list)
            for package, name in tests:
                grouped[package].append(name)
            for package, names in grouped.items():
                relative = package.split('/portico/', 1)[1]
                folder = root / relative
                folder.mkdir(parents=True, exist_ok=True)
                (folder / 'fixture_test.go').write_text('\n'.join(
                    'func ' + name + '(t *testing.T) {}' for name in sorted(names)))
                packages[relative.split('/')[0]].append(
                    {'ImportPath': package, 'Dir': str(folder), 'TestGoFiles': ['fixture_test.go']})
            def command(args, cwd=None):
                if args[0] == 'python3': return 'portico_rar7z,portico_cb7'
                self.assertEqual(args[:3], ['go', 'list', '-json'])
                return '\n'.join(json.dumps(p) for p in packages[Path(cwd).name])
            with patch('plan.ROOT', root), patch('nightly_lane.ROOT', root), patch('plan.command', side_effect=command):
                merge = make_plan(['packages/client-core/src/changed.ts'], full=True, tier='merge')
                nightly = make_plan([], full=True, tier='nightly')
                lanes = [select(nightly, lane) for lane in tiers['nightly_lanes']]
                golden_lane = select(nightly, 'golden')
            merge_owners = keys(merge['inventory'] + merge['js_owned_producers'])
            nightly_owners = keys(nightly['inventory'] + nightly['js_owned_producers']
                                 + [t for lane in lanes for t in lane])
            self.assertEqual(Counter(nightly_owners), Counter(tests))
            self.assertTrue(set(merge_owners) <= set(nightly_owners))
            self.assertIn(golden, keys(merge['inventory']))
            self.assertNotIn(golden, keys(nightly['inventory']))
            self.assertIn(golden, keys(golden_lane))
            self.assertEqual(merge['js'], nightly['js'])
            self.assertEqual({x['component'] for x in nightly['js']}, set(COMPONENTS))

    def test_active_workflow_records_full_inventory_and_requires_diagnostic_results(self):
        text = (ROOT / '.github/workflows/nightly.yml').read_text()
        self.assertRegex(text, r"(?m)^  schedule:\n    - cron: '17 4 \* \* \*'")
        def job(name):
            match = re.search(r'^  ' + name + r':\n(.*?)(?=^  [\w-]+:\n|\Z)', text, re.M | re.S)
            self.assertIsNotNone(match, name)
            return match[1]
        planner = job('planner')
        self.assertIn('python3 ci/plan.py --full --tier nightly', planner)
        self.assertNotRegex(planner, r'(?m)^          ref:')  # schedule checks out the default main SHA
        diagnostic = job('diagnostic')
        self.assertIn('lane: [pressure, golden]', diagnostic)
        self.assertIn('python3 ci/nightly_lane.py "$LANE"', diagnostic)
        aggregate = job('merge')
        self.assertRegex(aggregate, r'needs: \[[^\]]*\bdiagnostic\b[^\]]*\]')
        self.assertIn('ci/run.py aggregate', aggregate)
        self.assertIn('["diagnostic"]["result"] == "success"', aggregate)


if __name__ == '__main__':
    unittest.main()
