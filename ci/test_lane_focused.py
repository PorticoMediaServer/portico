import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import lane_focused as lane


class LaneTests(unittest.TestCase):
    def config(self, **changes):
        return lane.validated({'INPUT_SOURCE_REF': 'lane/test', **{'INPUT_' + k.upper(): v for k, v in changes.items()}})

    def test_inputs_are_data_and_fail_closed(self):
        for changes in ({'source_ref': '--upload-pack=evil'}, {'source_ref': 'x;id'}, {'packages': './x $(id)'},
                        {'packages': '../server'}, {'packages': '-exec=evil'}, {'count': '0'}, {'shards': '65'},
                        {'runner': 'self-hosted'}, {'targets': 'connections'}, {'mode': 'capture', 'targets': 'unknown'}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.config(**changes)
        cfg = self.config(run='TestX/$(id)|`id`', packages='./... ./internal/store/writer')
        self.assertEqual(cfg['run'], 'TestX/$(id)|`id`') # safe argv, never shell/eval
        self.assertEqual(self.config(source_ref='a' * 40)['fetch_ref'], 'a' * 40)
        self.assertEqual(self.config(mode='capture', targets='hn-browse')['targets'], ['hn-browse'])
        self.assertEqual(self.config(mode='capture', targets='hn-fts-read')['targets'], ['hn-fts-read'])
        self.assertEqual(self.config(mode='harness', targets='ec5-memory')['targets'], ['ec5-memory'])
        self.assertEqual(self.config(mode='capture', targets='hn-n1-preflights')['targets'], ['hn-n1-preflights'])

    def test_auto_splits_a_single_large_package_and_exact_lists(self):
        units = [dict(Package='same/package', Name=f'Test{i}') for i in range(76)]
        bins = lane.split_units(units, 'auto')
        self.assertEqual(len(bins), 4)
        self.assertEqual(sorted(u['Name'] for b in bins for u in b), sorted(u['Name'] for u in units))
        self.assertLessEqual(max(map(len, bins)), 25)
        self.assertEqual(len(lane.split_units(units, 64)), 64)
        self.assertEqual(len(lane.split_units(units[:2], 64)), 2)

    def test_exact_list_reaches_argv_without_running_other_package_tests(self):
        cfg = self.config(tests='TestA')
        unit = dict(Name='TestA', Package='pkg', run='.', flags=[])
        p = dict(config=cfg, tags='', parents={'pkg': ['TestA', 'TestB']}, shards=[[unit]])
        argv = lane.test_command(p, 'pkg', [unit])
        self.assertIn('-run=^(TestA)$', argv)
        self.assertIn('-skip=^(TestB)$', argv)
        # The subtest matcher is unchanged, but the exact parent restriction
        # still excludes unselected roots on the SAME shard.
        unit['run'] = './child'
        self.assertIn('-run=./child', lane.test_command(p, 'pkg', [unit]))
        self.assertIn('-skip=^(TestB)$', lane.test_command(p, 'pkg', [unit]))

    def events(self, path, names, end='pass'):
        values = [dict(Action='run', Test=n) for n in names]
        values += [dict(Action='pass', Test=n) for n in names]
        values += [dict(Action=end)]
        path.write_text(''.join(json.dumps(v) + '\n' for v in values))

    def test_terminal_guards_are_independent(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / 'events'
            self.events(p, ['TestA'])
            self.assertTrue(lane.collect_events(p, ['TestA'], True, 0)['ok'])
            self.assertFalse(lane.collect_events(p, ['TestA', 'TestB'], True, 0)['ok'])
            self.assertFalse(lane.collect_events(p, ['TestA'], True, 1)['ok'])
            self.assertFalse(lane.collect_events(p, ['TestB'], False, 0)['ok'])
            self.events(p, ['TestA'], 'fail')
            self.assertFalse(lane.collect_events(p, ['TestA'], True, 0)['ok'])
            self.events(p, [], 'skip')
            self.assertTrue(lane.collect_events(p, [], True, 0)['ok'])
            self.assertFalse(lane.collect_events(p, ['TestA'], True, 0)['ok'])

    def test_missing_shard_never_passes_combined_summary(self):
        with tempfile.TemporaryDirectory() as d, patch.dict(os.environ, {}, clear=True):
            root = Path(d); plan = root / 'plan.json'; results = root / 'results'; results.mkdir()
            p = dict(source_sha='a' * 40, config={'mode': 'tests'}, shards=[[{'Name': 'TestA'}], [{'Name': 'TestB'}]], run_url='https://example/run')
            lane.write(plan, p)
            r = dict(index=0, source_sha=p['source_sha'], plan_sha256=lane.digest(plan.read_bytes()), ok=True, complete=True,
                     packages=[dict(root_events=1, passed=1, skipped=0)], evidence=[])
            lane.write(results / '0/result.json', r)
            self.assertEqual(lane.summarize(root / 'summary', plan, results), 1)
            r['index'] = 1; lane.write(results / '1/result.json', r)
            self.assertEqual(lane.summarize(root / 'summary', plan, results), 0)
            r['complete'] = False; lane.write(results / '1/result.json', r)
            self.assertEqual(lane.summarize(root / 'summary', plan, results), 1)

    def test_owned_readonly_module_scratch_is_removed_without_following_links(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d); scratch = root / 'home'; cache = scratch / 'go/pkg/mod/example'
            cache.mkdir(parents=True); (cache / 'file').write_text('cache')
            outside = root / 'preserve'; outside.mkdir(); (outside / 'file').write_text('original')
            (scratch / 'link').symlink_to(outside, target_is_directory=True)
            cache.chmod(0o555)
            lane.remove_disposable(scratch)
            self.assertFalse(scratch.exists())
            self.assertEqual((outside / 'file').read_text(), 'original')

    def test_auth_and_workflow_command_channels_not_forwarded(self):
        with tempfile.TemporaryDirectory() as d, patch.dict(os.environ, {'GITHUB_TOKEN': 'fake', 'GH_TOKEN': 'fake', 'GITHUB_OUTPUT': 'fake', 'ACTIONS_RUNTIME_TOKEN': 'fake', 'GOFLAGS': '-exec=evil'}):
            env = lane.clean_env(Path(d))
            self.assertFalse(any(k.startswith(('GITHUB_', 'ACTIONS_', 'RUNNER_', 'INPUT_')) for k in env))
            self.assertNotIn('GH_TOKEN', env)
            self.assertNotIn('GOFLAGS', env)


if __name__ == '__main__':
    unittest.main()
