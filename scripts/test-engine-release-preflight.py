#!/usr/bin/env python3
"""Controlled metadata fixtures only; no engine binaries or qualification grants."""
import hashlib
import json
from pathlib import Path
import runpy
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/inspect-engine-release.py'))
SHA = 'a' * 40


def write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2) + '\n')


class ReleasePreflight(unittest.TestCase):
    def fixture(self, inputs, target):
        directory = inputs / target
        native = directory / 'native'; gates = native / 'gates'
        binaries = {name: hashlib.sha256(name.encode()).hexdigest() for name in M['TOOLS']}
        repeat = [{'round': index, 'sourceArchiveSHA256': 'b' * 64, 'binaries': binaries} for index in (1, 2)]
        write(native / 'repeat-build.json', repeat)
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        files = {'bin/' + name + ('.exe' if target.startswith('windows-') else ''): sha for name, sha in binaries.items()}
        files.update({'repeat-build.json': M['digest'](native / 'repeat-build.json'),
                      'sources.lock.json': M['digest'](ROOT / 'third_party/ffmpeg/sources.lock.json'),
                      'requirements.v1.json': M['digest'](ROOT / 'third_party/ffmpeg/requirements.v1.json'),
                      'corresponding-source.tar.xz': 'c' * 64, 'LICENSES/metadata-fixture.txt': 'd' * 64})
        manifest = {'target': target, 'licenseMode': 'gpl3', 'buildId': lock['releaseTag'],
                    'ffmpegVersion': lock['ffmpeg']['version'], 'files': files}
        write(native / 'toolchain-manifest.json', manifest)
        manifest_sha = M['digest'](native / 'toolchain-manifest.json')
        identity = {'target': target, 'engineSourceSHA': SHA, 'runId': 1, 'runAttempt': 1,
                    'context': 'github-actions', 'qualified': False, 'release': False}
        write(directory / 'product/transfer.json', dict(identity, constructionPassed=True,
            toolchainManifestSHA256=manifest_sha, correspondingSourceSHA256='c' * 64, bundleArchiveSHA256='e' * 64))
        write(native / 'cold-native.json', dict(identity, nativeEngineChecks=True))
        names = M['gate_names'](target)
        reports = {}
        for name in names:
            if name != 'verify-ffmpeg-manifest.py':
                path = gates / (name + '.json'); write(path, {'controlledMetadataFixture': True})
                reports[name] = M['digest'](path)
        write(gates / 'native-engine.json', {'target': target, 'context': 'github-actions', 'release': False,
            'nativeEngineChecks': True, 'fullCompilerAcceptance': True, 'toolchainManifestSHA256': manifest_sha,
            'qualifierScripts': {name: M['digest'](ROOT / 'scripts' / name) for name in names}, 'reports': reports})
        return directory

    def inspect(self, inputs):
        return M['inspect_set'](inputs, SHA, 1, 1)

    def test_complete_shaped_receipts_are_only_a_preflight_never_a_publication_grant(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for target in M['REQUIRED']:
                self.fixture(root, target)
            report = self.inspect(root)
            self.assertTrue(report['preflightPassed'])
            self.assertEqual(report['context'], 'read-only-inspection')
            self.assertFalse(report['readyForPublication'])
            self.assertFalse(report['qualified'])
            self.assertFalse(report['release'])

    def test_optional_workflow_success_cannot_promote_its_failed_target(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for target in (*M['REQUIRED'], *M['OPTIONAL']):
                self.fixture(root, target)
            path = root / 'macos-x64/product/transfer.json'
            data = json.loads(path.read_text()); data.update(constructionPassed=False, workflowConclusion='success'); write(path, data)
            report = self.inspect(root)
            self.assertTrue(report['preflightPassed'])
            self.assertFalse(report['targets']['macos-x64']['receiptChecksPassed'])
            self.assertFalse(report['readyForPublication'])

    def test_missing_mandatory_target_and_each_incomplete_or_mixed_receipt_refuse(self):
        mutations = [('product/transfer.json', 'engineSourceSHA', 'f' * 40),
                     ('product/transfer.json', 'runAttempt', 2), ('product/transfer.json', 'runId', True),
                     ('native/cold-native.json', 'nativeEngineChecks', False),
                     ('native/cold-native.json', 'qualified', True),
                     ('native/gates/native-engine.json', 'fullCompilerAcceptance', False),
                     ('native/gates/native-engine.json', 'qualifierScripts', {})]
        for relative, key, value in mutations:
            with self.subTest(key=key), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary); directory = self.fixture(root, 'linux-x64')
                path = directory / relative; data = json.loads(path.read_text()); data[key] = value; write(path, data)
                report = self.inspect(root)
                self.assertFalse(report['preflightPassed'])
                self.assertFalse(report['targets']['linux-x64']['receiptChecksPassed'])
                self.assertTrue(report['targets']['windows-x64']['holds'])

    def test_report_and_repeat_hash_mutations_refuse(self):
        for relative in ('native/gates/verify-ffmpeg-recipes.py.json', 'native/repeat-build.json'):
            with self.subTest(relative=relative), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary); directory = self.fixture(root, 'linux-x64')
                write(directory / relative, {'changed': True})
                self.assertFalse(self.inspect(root)['targets']['linux-x64']['receiptChecksPassed'])

    def test_duplicate_fields_and_linked_evidence_refuse(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); directory = self.fixture(root, 'linux-x64')
            path = directory / 'product/transfer.json'; path.write_text('{"runId":1,"runId":2}')
            self.assertFalse(self.inspect(root)['targets']['linux-x64']['receiptChecksPassed'])
            path.unlink(); outside = root / 'outside.json'; write(outside, {})
            path.symlink_to(outside)
            self.assertFalse(self.inspect(root)['targets']['linux-x64']['receiptChecksPassed'])


if __name__ == '__main__':
    unittest.main()
