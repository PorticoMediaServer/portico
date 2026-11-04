import unittest

import source_readers as readers


class DirectSnapshotSelection(unittest.TestCase):
    def test_importable_named_directories_select_the_ratchet(self):
        for name in ['testdata', 'node_modules', '.hidden']:
            path = f'server/internal/area/{name}/bridge.go'
            with self.subTest(path=path):
                selected = readers.select([path]).get('server/internal/dbwork', {})
                self.assertIn('TestDirectBeginSnapshotCensus', selected)
                if name == 'testdata':
                    self.assertNotIn('TestWriteCensus', selected)

    def test_workspace_sources_and_resolution_changes_select_the_ratchet(self):
        for path in ['apikit/bridge.go', 'server/go.mod', 'server/go.sum',
                     'server/.hidden/module/go.mod', 'go.work', 'go.work.sum']:
            with self.subTest(path=path):
                selected = readers.select([path]).get('server/internal/dbwork', {})
                self.assertIn('TestDirectBeginSnapshotCensus', selected)

    def test_test_sources_and_non_go_fixture_data_do_not_select_it(self):
        for path in ['server/internal/area/testdata/bridge_test.go',
                     'server/internal/area/testdata/fixture.sql', 'server/README.md']:
            with self.subTest(path=path):
                selected = readers.select([path]).get('server/internal/dbwork', {})
                self.assertNotIn('TestDirectBeginSnapshotCensus', selected)


if __name__ == '__main__':
    unittest.main()
