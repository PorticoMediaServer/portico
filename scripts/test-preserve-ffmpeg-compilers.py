#!/usr/bin/env python3
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('preserve', Path(__file__).with_name('preserve-ffmpeg-compilers.py'))
M = importlib.util.module_from_spec(spec); spec.loader.exec_module(M)


class CompilerPreservation(unittest.TestCase):
    def test_committed_pin_contains_exact_same_source_and_destination_digest(self):
        pins = M.lock()
        for row in pins['images'].values():
            self.assertEqual(row['upstream'].split('@')[1], row['mirror'].split('@')[1])
            self.assertNotIn(':latest', row['upstream'])

    def test_corrupt_or_replaced_upstream_index_is_refused_before_copy(self):
        row = M.lock()['images']['linux64']
        with patch.object(M, 'raw', return_value=b'{"manifests": []}'):
            with self.assertRaisesRegex(ValueError, 'bytes/platforms changed'):
                M.verified_manifest(row)

    def test_preservation_cannot_run_from_a_topic_or_pull_request(self):
        for event, ref in [('workflow_dispatch', 'refs/heads/topic'), ('pull_request', 'refs/heads/main')]:
            env = dict(GITHUB_ACTIONS='true', RUNNER_ENVIRONMENT='github-hosted',
                GITHUB_REPOSITORY='PorticoMediaServer/portico', GITHUB_EVENT_NAME=event,
                GITHUB_REF=ref, GITHUB_WORKFLOW_REF=
                'PorticoMediaServer/portico/.github/workflows/ffmpeg-compiler-custody.yml@refs/heads/main')
            with patch.dict(os.environ, env, clear=True), patch('subprocess.run') as execute:
                with self.assertRaises(ValueError):
                    M.preserve('linux64', Path('/not-used'))
                execute.assert_not_called()


if __name__ == '__main__':
    unittest.main()
