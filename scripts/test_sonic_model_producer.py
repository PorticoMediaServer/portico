import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(file))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


producer = load('sonic_model_producer', 'check-sonic-model-producer.py')
exporter = load('sonic_model_exporter', 'export-musicnn-onnx.py')


class RetainedModelTests(unittest.TestCase):
    def git(self, root, *args):
        return subprocess.run(['git', '-c', 'user.name=Justin Ehler', '-c', 'user.email=justin@justinehler.com', *args],
                              cwd=root, check=True, capture_output=True, text=True).stdout.strip()

    def fixture(self, root):
        self.git(root, 'init', '-b', 'main')
        for name in producer.INPUTS:
            file = root / name
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_text('reviewed exporter input: ' + name)
        lock = root / 'third_party/musicnn/model.lock.json'
        lock.write_text(json.dumps({'sourceCommit': 'original', 'onnx': {'version': 'one', 'sha256': 'a' * 64, 'bytes': 100}}))
        self.git(root, 'add', '.')
        self.git(root, 'commit', '-m', 'Reviewed original exporter')
        sha = self.git(root, 'rev-parse', 'HEAD')
        record = {'path': '.github/workflows/sonic-toolchain.yml', 'event': 'workflow_dispatch',
                  'head_branch': 'main', 'head_sha': sha,
                  'repository': {'full_name': 'PorticoMediaServer/portico-server'},
                  'head_repository': {'full_name': 'PorticoMediaServer/portico-server'}}
        lock.write_text(json.dumps({'sourceCommit': 'original', 'onnx': {'version': 'one', 'sha256': 'b' * 64, 'bytes': 101}}))
        self.git(root, 'add', '.')
        self.git(root, 'commit', '-m', 'Pin parity-validated output bytes')
        return record

    def test_same_main_exporter_can_resume_after_only_byte_pin_changes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            record = self.fixture(root)
            producer.check(record, 'PorticoMediaServer/portico-server', root)

    def test_foreign_fork_workflow_branch_event_and_unknown_commit_are_refused(self):
        for field, value in [('head_branch', 'unreviewed'), ('event', 'pull_request'),
                             ('path', '.github/workflows/other.yml'), ('head_sha', 'f' * 40),
                             ('repository', {'full_name': 'foreign/repo'}),
                             ('head_repository', {'full_name': 'fork/repo'})]:
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                record = self.fixture(root)
                record[field] = value
                with self.assertRaises(ValueError):
                    producer.check(record, 'PorticoMediaServer/portico-server', root)

    def test_changed_exporter_weights_and_unrelated_commit_cannot_resume(self):
        for kind in ('exporter', 'weights', 'unrelated'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                record = self.fixture(root)
                if kind == 'exporter':
                    (root / producer.INPUTS[1]).write_text('changed exporter')
                elif kind == 'weights':
                    path = root / 'third_party/musicnn/model.lock.json'
                    lock = json.loads(path.read_text())
                    lock['sourceCommit'] = 'different weights'
                    path.write_text(json.dumps(lock))
                else:
                    tree = self.git(root, 'rev-parse', 'HEAD^{tree}')
                    record['head_sha'] = self.git(root, 'commit-tree', tree, '-m', 'Unrelated producer')
                with self.assertRaises(ValueError):
                    producer.check(record, 'PorticoMediaServer/portico-server', root)

    def test_candidate_is_explicit_and_never_accepts_a_different_model_version(self):
        data = b'fixture converted bytes'
        lock = {'onnx': {'version': 'approved', 'sha256': hashlib.sha256(data).hexdigest(), 'bytes': len(data)}}
        exporter.validate_export_pin(lock, 'approved', data)
        with self.assertRaises(ValueError):
            exporter.validate_export_pin(lock, 'approved', data + b'changed')
        exporter.validate_export_pin(lock, 'approved', data + b'changed', candidate=True)
        for candidate in (False, True):
            with self.assertRaises(ValueError):
                exporter.validate_export_pin(lock, 'unapproved version', data, candidate=candidate)


if __name__ == '__main__':
    unittest.main()
