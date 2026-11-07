"""Small synthetic protocol controls. No real SQLite or population evidence."""
import contextlib
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import qual_fixture as q


SOURCE = 'a' * 40
SCHEMA = 'b' * 64


def digest(data):
    return hashlib.sha256(data).hexdigest()


def synthetic(directory):
    # Intentionally NOT a SQLite database; these tests exercise transport only.
    image = b'synthetic protocol bytes, not a population database\n'
    m = dict(version=q.VERSION, source_commit=SOURCE, schema_sha256=SCHEMA,
             engine=q.ENGINE, engine_source='synthetic source identity', go_version=q.GO,
             actual_policy=dict(Engine=q.ENGINE, JournalMode='wal', ForeignKeys=1,
                                Synchronous=2, AutoCheckpoint=0, CacheSize=-1024,
                                BusyTimeout=0, MmapSize=0, QueryOnly=0, TempStore=1),
             dqs_ddl=0, dqs_dml=0, database_sha256=digest(image), database_bytes=len(image),
             facts=dict(version=q.SHAPE, seed=0, residents=2, library_id=7,
                        library_key='qualification-movies', first_id=1000000001, last_id=1000000002,
                        counts={'media': 2}, logical_sha256='c' * 64))
    m['facts']['counts'] = {name: 2 for name in ('catalog_subjects', 'catalog_entities', 'catalog_assets',
        'catalog_asset_links', 'catalog_scalar_contributions', 'catalog_asset_contributions', 'current_movies')}
    m['facts']['counts'].update({name: 1 for name in ('libraries', 'catalog_libraries', 'catalog_kinds',
        'catalog_membership_clock', 'catalog_age_item_counts')})
    directory.mkdir(exist_ok=True)
    (directory / 'population.sqlite').write_bytes(image)
    q.atomic_json(directory / 'population.json', m)
    return m


class Inputs(unittest.TestCase):
    def test_branch_and_full_sha(self):
        self.assertEqual(q.source_ref('lane/ql-fixture'), 'refs/heads/lane/ql-fixture')
        self.assertEqual(q.source_ref('refs/heads/main'), 'refs/heads/main')
        self.assertEqual(q.source_ref(SOURCE.upper()), SOURCE)

    def test_references_and_injection_fail_before_subprocess(self):
        for value in ('', '-main', 'refs/tags/v1', 'a..b', 'a//b', 'a/.b',
                      'a.lock/b', 'a/', 'a.', 'a\nb', 'a b', '$(id)',
                      'main;id', '`id`', SOURCE + '^', 'a' * 201):
            with self.subTest(value=value), mock.patch.object(q.subprocess, 'Popen') as popen:
                with self.assertRaises(ValueError):
                    q.inputs({'INPUT_SOURCE_REF': value})
                popen.assert_not_called()

    def test_bounds_and_zero_seed(self):
        cfg = q.inputs({'INPUT_SOURCE_REF': 'main', 'INPUT_SEED': '0'})
        self.assertEqual((cfg['seed'], cfg['residents']), (0, 1000000))
        for field, value in (('SEED', str(2**32)), ('SEED', '-1'), ('SEED', '1;id'),
                             ('RESIDENTS', '0'), ('RESIDENTS', '1000001'),
                             ('TIMEOUT_MINUTES', '91'), ('TIMEOUT_MINUTES', '0'),
                             ('PURPOSE', 'release')):
            with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                q.inputs({'INPUT_SOURCE_REF': 'main', 'INPUT_' + field: value})
        self.assertEqual(q.inputs({'INPUT_SOURCE_REF': 'main', 'INPUT_SEED': str(2**32-1),
                                   'INPUT_RESIDENTS': '1'})['seed'], 2**32-1)


class Protocol(unittest.TestCase):
    def setUp(self):
        # One task-scoped directory on disk, cleaned after each tiny control.
        self.temporary = tempfile.TemporaryDirectory(prefix='qual-protocol-', dir=Path.cwd())
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.artifact = self.root / 'artifact'
        self.manifest = synthetic(self.artifact)

    def verify(self, **kwargs):
        return q.validate_artifact(self.artifact, source=SOURCE, schema=SCHEMA,
                                   seed=0, residents=2, **kwargs)

    def rewrite(self):
        q.atomic_json(self.artifact / 'population.json', self.manifest)

    def test_pinned_manifest_and_streamed_image(self):
        pin = q.file_hash(self.artifact / 'population.json')
        m, actual = self.verify(manifest_sha=pin)
        self.assertEqual((actual, m['facts']['seed']), (pin, 0))
        self.manifest['engine_source'] = 'different synthetic identity'
        self.rewrite()
        with self.assertRaisesRegex(ValueError, 'manifest digest'):
            self.verify(manifest_sha=pin)

    def test_image_hash_and_size(self):
        path = self.artifact / 'population.sqlite'
        data = path.read_bytes()
        path.write_bytes(data.replace(b'bytes', b'BYTES'))
        with self.assertRaisesRegex(ValueError, 'image digest'):
            self.verify()
        path.write_bytes(data + b'x')
        with self.assertRaisesRegex(ValueError, 'length'):
            self.verify()

    def test_sidecars_and_links_refused(self):
        for suffix in ('-wal', '-shm'):
            path = self.artifact / ('population.sqlite' + suffix)
            path.symlink_to('missing')  # Even a dangling sidecar must fail.
            with self.assertRaisesRegex(ValueError, 'sidecar'):
                self.verify()
            path.unlink()
        image = self.artifact / 'population.sqlite'
        real = self.root / 'real'
        image.rename(real)
        image.symlink_to(real)
        with self.assertRaisesRegex(ValueError, 'file type'):
            self.verify()

    def test_closed_versioned_fields_and_numeric_types(self):
        original = json.dumps(self.manifest)
        mutations = [lambda m: m.update(extra='field'),
                     lambda m: m['facts'].update(extra=1),
                     lambda m: m.update(version='future'),
                     lambda m: m.update(source_commit='d' * 40),
                     lambda m: m.update(schema_sha256='e' * 64),
                     lambda m: m.update(go_version='go1.27.1'),
                     lambda m: m.update(dqs_ddl=1),
                     lambda m: m.update(dqs_dml=False),
                     lambda m: m['facts'].update(seed=True),
                     lambda m: m['facts'].update(residents=3),
                     lambda m: m['facts'].update(last_id=999),
                     lambda m: m['facts'].update(counts={'media': -1}),
                     lambda m: m['actual_policy'].update(Synchronous=1),
                     lambda m: m['actual_policy'].update(CacheSize=-2048),
                     lambda m: m['facts']['counts'].pop('catalog_entities')]
        for change in mutations:
            self.manifest = json.loads(original)
            change(self.manifest)
            self.rewrite()
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.verify()

    def test_duplicate_json_field_rejected(self):
        path = self.artifact / 'population.json'
        path.write_text(path.read_text().replace('"seed": 0', '"seed": 1, "seed": 0'))
        with self.assertRaisesRegex(ValueError, 'duplicate'):
            self.verify()

    def test_publication_uses_rename_and_refuses_reuse(self):
        output = self.root / 'output'
        output.mkdir()
        inode = (self.artifact / 'population.sqlite').stat().st_ino
        receipt = {'status': 'failed'}
        q.publish(self.artifact, output, receipt, source=SOURCE, schema=SCHEMA, seed=0, residents=2)
        self.assertEqual(receipt['status'], 'complete')
        self.assertEqual((output / 'population.sqlite').stat().st_ino, inode)
        synthetic(self.artifact)
        with self.assertRaisesRegex(ValueError, 'already exists'):
            q.publish(self.artifact, output, {}, source=SOURCE, schema=SCHEMA, seed=0, residents=2)

    def test_failed_validation_never_publishes(self):
        (self.artifact / 'population.sqlite-wal').write_bytes(b'partial')
        output = self.root / 'output'
        output.mkdir()
        receipt = {'status': 'failed'}
        with self.assertRaises(ValueError):
            q.publish(self.artifact, output, receipt, source=SOURCE, schema=SCHEMA, seed=0, residents=2)
        self.assertEqual(receipt['status'], 'failed')
        self.assertEqual(list(output.iterdir()), [])

    def test_failed_target_with_files_never_complete(self):
        source = self.root / 'source'
        module = source / 'server'
        schema = module / 'internal/store/schema'
        schema.mkdir(parents=True)
        (module / 'go.mod').write_text('module synthetic\n\ngo 1.26.10\nrequire (\nmodernc.org/sqlite v1.58.0\n)\n')
        (module / 'go.sum').write_text('synthetic\n')
        (schema / 'schema.sql').write_bytes(b'synthetic schema')
        schema_hash = q.file_hash(schema / 'schema.sql')
        (schema / 'schema.go').write_text(f'const SHA256 = "{schema_hash}"\n')
        output, work = self.root / 'result', self.root / 'work'
        env = dict(INPUT_SOURCE_REF=SOURCE, INPUT_RESIDENTS='2', GITHUB_ACTIONS='true',
                   RUNNER_ENVIRONMENT='github-hosted', RUNNER_OS='Linux',
                   GITHUB_RUN_ID='123', GITHUB_RUN_ATTEMPT='1', GITHUB_REPOSITORY='synthetic/protocol')

        def fake_command(argv, cwd, child_env, report, name, timeout):
            path = report / (name + '.log')
            text = ''
            if name in ('source-sha', 'actual-head'):
                text = SOURCE
            elif name == 'source-archive':
                (work / 'source.tar').write_bytes(b'synthetic source archive')
            elif name == 'go-version':
                text = 'go version go1.26.10 linux/amd64'
            elif name == 'build':
                self.assertEqual(argv, ['go', 'test', '-p', '1', '-c', '-o', str(work / 'writer.test'),
                                        './internal/store/writer'])
                self.assertEqual(cwd, module)
                (work / 'writer.test').write_bytes(b'synthetic binary identity')
            elif name == 'binary-build-info':
                text = str(work / 'writer.test') + ': go1.26.10\n\tpath\tgithub.com/PorticoMediaServer/portico/server/internal/store/writer.test\n'
            elif name == 'sqlite-build-dependency':
                text = '\nmodernc.org/sqlite/lib v1.58.0 ORIGINAL\ngithub.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite BUNDLED\n'
            elif name == 'export':
                self.assertEqual(argv, [str(work / 'writer.test'), '-test.run=^TestExportQualificationPopulation$',
                                        '-test.count=1', '-test.timeout=60m', '-test.v'])
                self.assertEqual(cwd, module / 'internal/store/writer')
                self.assertEqual(timeout, 3630)
                config = json.loads((work / 'export-config.json').read_text())
                self.assertEqual(set(config), {'output_dir', 'source_commit', 'seed', 'residents'})
                self.assertEqual(config['seed'], 0)
                self.assertTrue(Path(config['output_dir']).is_absolute())
                self.assertEqual(child_env['PORTICO_QUAL_FIXTURE_HOSTED'], '1')
                synthetic(Path(config['output_dir']))
                path.write_text('synthetic failed target\n')
                raise ValueError('synthetic target failed after writing files')
            path.write_text(text)
            return path

        with mock.patch.dict(os.environ, env, clear=True), mock.patch.object(q, 'command', side_effect=fake_command), contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaisesRegex(ValueError, 'synthetic target failed'):
                q.produce(source, output, work)
        receipt = json.loads((output / 'report/receipt.json').read_text())
        self.assertEqual(receipt['status'], 'failed')
        self.assertIn('NOT qualification', receipt['qualification'])
        self.assertTrue((output / 'report/export.log').exists())
        self.assertFalse((output / 'population.sqlite').exists())
        self.assertFalse(work.exists())

    def test_environment_removes_auth_and_file_commands(self):
        env = dict(PATH='/usr/bin', GITHUB_TOKEN='synthetic', GH_TOKEN='synthetic',
                   GITHUB_OUTPUT='synthetic', ACTIONS_RUNTIME_TOKEN='synthetic',
                   INPUT_SOURCE_REF='synthetic', GOFLAGS='-tags=release', GOTOOLCHAIN='auto')
        with mock.patch.dict(os.environ, env, clear=True):
            actual = q.clean_env(self.root)
        self.assertFalse(set(env) - {'PATH', 'GOFLAGS', 'GOTOOLCHAIN'} & set(actual))
        self.assertEqual(actual['GOTOOLCHAIN'], 'local')
        self.assertEqual(actual['GOFLAGS'], '-mod=readonly')

    def test_setup_failure_receipt_is_failed_and_preserves_existing(self):
        output = self.root / 'setup-failure'
        with contextlib.redirect_stdout(io.StringIO()):
            q.failure_receipt(output)
            before = (output / 'report/receipt.json').read_bytes()
            q.failure_receipt(output)
        self.assertEqual(before, (output / 'report/receipt.json').read_bytes())
        self.assertEqual(json.loads(before)['status'], 'failed')

    def test_test_binary_dependency_omission_uses_selected_build_evidence(self):
        # Real Go1.26 test buildinfo has no dep rows; absence is not corruption.
        metadata = '/private/writer.test: go1.26.10\n\tpath\tgithub.com/PorticoMediaServer/portico/server/internal/store/writer.test\n\tmod\tserver\t(devel)\n'
        selected = 'modernc.org/sqlite/lib v1.58.0 ORIGINAL\ngithub.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite BUNDLED\n'
        q.validate_sqlite_build_identity(metadata, selected)
        for selected in ('', 'modernc.org/sqlite/lib v1.58.0 ORIGINAL',
                         selected.replace('ORIGINAL', 'REPLACED'), selected.replace('v1.58.0', 'v1.57.0'),
                         selected + 'modernc.org/sqlite ROOT_DRIVER\n'):
            with self.subTest(selected=selected), self.assertRaises(ValueError):
                q.validate_sqlite_build_identity(metadata, selected)
        with self.assertRaises(ValueError):
            q.validate_sqlite_build_identity(metadata.replace('go1.26.10', 'go1.27.1'),
                                            'modernc.org/sqlite/lib v1.58.0 ORIGINAL\ngithub.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite BUNDLED\n')

    def test_command_uses_argv_and_timeout(self):
        report = self.root / 'report'
        report.mkdir()
        process = mock.Mock(pid=1234)
        process.wait.side_effect = [subprocess.TimeoutExpired('synthetic', 1), -9]
        with mock.patch.object(q.subprocess, 'Popen', return_value=process) as popen, mock.patch.object(q.os, 'killpg') as kill:
            with self.assertRaises(subprocess.TimeoutExpired):
                q.command(['synthetic', 'literal;$(id)'], self.root, {}, report, 'control', 1)
        self.assertEqual(popen.call_args.args[0], ['synthetic', 'literal;$(id)'])
        self.assertNotIn('shell', popen.call_args.kwargs)
        kill.assert_called_once_with(1234, q.signal.SIGKILL)


if __name__ == '__main__':
    unittest.main()
