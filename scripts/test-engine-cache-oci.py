#!/usr/bin/env python3
"""Cache transport integrity, atomic restore and migration deletion controls."""
import importlib.util
import hashlib
import io
import json
import lzma
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch


def module(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value); return value


OCI = module('engine-cache-oci')
MIGRATE = module('engine-cache-migration')


class OCIControls(unittest.TestCase):
    def test_manifest_rejects_auto_unpack_external_urls_and_named_configs(self):
        for variant in ('valid', 'auto-unpack', 'external-url', 'named-config'):
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temporary:
                manifest = dict(schemaVersion=2, artifactType=OCI.TYPE,
                    config=dict(mediaType='application/vnd.oci.empty.v1+json', size=2,
                                digest='sha256:' + hashlib.sha256(b'{}').hexdigest()),
                    layers=[dict(mediaType=media, size=10, digest='sha256:' + 'a' * 64,
                                 annotations={'org.opencontainers.image.title': name})
                            for name, media in (('receipt.json', OCI.RECEIPT), ('payload.tar', OCI.PAYLOAD))])
                if variant == 'auto-unpack': manifest['layers'][1]['annotations']['io.deis.oras.content.unpack'] = 'true'
                if variant == 'external-url': manifest['layers'][1]['urls'] = ['https://outside.invalid/payload']
                if variant == 'named-config': manifest['config']['annotations'] = {'org.opencontainers.image.title': 'unexpected-file'}
                raw = OCI.encoded(manifest); descriptor = dict(digest='sha256:' + hashlib.sha256(raw).hexdigest(), size=len(raw))
                transport = object.__new__(OCI.Transport)
                transport.repository = 'ghcr.io/porticomediaserver/fixture'; transport.tools = Path(temporary)
                def reader(args, **unused):
                    if '--descriptor' in args: return OCI.encoded(descriptor)
                    Path(args[args.index('--output') + 1]).write_bytes(raw); return b''
                transport.oras = reader
                if variant == 'valid': self.assertIsNotNone(transport.manifest(transport.repository + ':fixture'))
                else:
                    with self.assertRaises(ValueError): transport.manifest(transport.repository + ':fixture')

    def source(self, root):
        source = root / 'source'; source.mkdir()
        name = '50-controlled_' + 'a' * 64 + '.tar.xz'
        (source / name).write_bytes(lzma.compress(b'exact retained source fixture'))
        (source / name).chmod(0o640)
        (source / 'receipt.json').write_text(json.dumps({name: OCI.digest(source / name)}))
        scope = dict(target='windows-x64', kind='sources', namespace='fixture-scope', inputKey='fixture-input')
        return source, name, scope

    def test_source_bytes_modes_and_current_context_survive_atomic_round_trip(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); source, name, scope = self.source(root)
            transport = root / 'transport'; transport.mkdir()
            receipt = OCI.pack(source, scope, transport)
            destination = root / 'restored'; destination.mkdir()
            (destination / 'transport-context.json').write_text('{"current":true}')
            self.assertEqual(OCI.unpack(transport, destination, scope), receipt)
            self.assertEqual((destination / name).read_bytes(), (source / name).read_bytes())
            self.assertEqual((destination / name).stat().st_mode & 0o777, 0o640)
            self.assertEqual(json.loads((destination / 'transport-context.json').read_text()), {'current': True})
            self.assertEqual(OCI.inventory(destination, 'sources'), OCI.inventory(source, 'sources'))

    def test_corrupt_payload_cross_scope_and_release_receipts_refuse_before_restore(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); source, name, scope = self.source(root)
            transport = root / 'transport'; transport.mkdir(); original = OCI.pack(source, scope, transport)
            for changed in (dict(qualified=True), dict(release=True), dict(namespace='different-compiler'), dict(target='linux-arm64')):
                (transport / 'receipt.json').write_text(json.dumps(dict(original, **changed)))
                with self.assertRaises(ValueError): OCI.unpack(transport, root / 'refused', scope)
                self.assertFalse((root / 'refused').exists())
            (transport / 'receipt.json').write_text(json.dumps(original))
            (transport / 'payload.tar').chmod(0o644)  # Deliberate fault injection into the sealed fixture.
            with (transport / 'payload.tar').open('r+b') as stream: stream.write(b'changed')
            with self.assertRaises(ValueError): OCI.unpack(transport, root / 'refused', scope)
            self.assertFalse((root / 'refused').exists())

    def test_unsafe_duplicate_link_and_privileged_mode_members_are_refused(self):
        for variant in ('escape', 'absolute', 'backslash', 'duplicate', 'symlink', 'hardlink', 'setuid'):
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary); transport = root / 'transport'; transport.mkdir()
                name = {'escape': '../escape', 'absolute': '/absolute', 'backslash': 'bad\\name'}.get(variant, 'payload')
                info = tarfile.TarInfo(name); info.mode = 0o4755 if variant == 'setuid' else 0o644
                if variant in ('symlink', 'hardlink'):
                    info.type = tarfile.SYMTYPE if variant == 'symlink' else tarfile.LNKTYPE; info.linkname = '/outside'
                else: info.size = 4
                with tarfile.open(transport / 'payload.tar', 'w') as archive:
                    archive.addfile(info, io.BytesIO(b'data') if info.isfile() else None)
                    if variant == 'duplicate': archive.addfile(info, io.BytesIO(b'data'))
                scope = dict(target='windows-x64', kind='sources', namespace='fixture', inputKey='fixture')
                files = {name: dict(bytes=info.size, mode=info.mode, sha256=hashlib.sha256(b'data' if info.isfile() else b'').hexdigest())}
                receipt = dict(schemaVersion=1, **scope, snapshot=OCI.sha(files), files=files,
                               payloadSHA256=OCI.digest(transport / 'payload.tar'), payloadBytes=(transport / 'payload.tar').stat().st_size,
                               qualified=False, release=False, readyForPublication=False)
                (transport / 'receipt.json').write_text(json.dumps(receipt))
                with self.assertRaises(ValueError): OCI.unpack(transport, root / 'destination', scope)
                self.assertFalse((root / 'escape').exists())
                self.assertFalse((root / 'destination' / 'payload').exists())

    def test_complete_stage_oci_receipts_are_preserved_and_partial_captures_excluded(self):
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, PORTICO_ENGINE_BUILD_CONTEXT='development'):
            root = Path(temporary); directory = root / 'cache'
            prefix = root / 'prefix'; prefix.mkdir()
            api = OCI.module('engine-prefix-cache')
            cache = api.PrefixCache(directory / 'macos', root / 'state')
            cache.begin({'stage': 'inert fixture'}, {'prefix': prefix})
            (prefix / 'lib.a').write_bytes(b'inert library fixture')
            cache.finish({'stage': 'inert fixture'}, {'prefix': prefix})
            partial = directory / 'macos/stages/.stage-interrupted'; partial.mkdir(); (partial / 'index.json').write_text('never restore')
            before = OCI.inventory(directory, 'stages')
            self.assertFalse(any('interrupted' in name for name in before))
            output = root / 'transport'; output.mkdir()
            scope = dict(target='macos-arm64', kind='stages', namespace='compiler', inputKey='inputs')
            OCI.pack(directory, scope, output); OCI.unpack(output, root / 'restored', scope)
            self.assertEqual(OCI.inventory(root / 'restored', 'stages'), before)

    def test_cache_guard_refuses_non_development_and_non_main_writers(self):
        base = dict(GITHUB_ACTIONS='true', RUNNER_ENVIRONMENT='github-hosted', GITHUB_REPOSITORY='PorticoMediaServer/portico',
                    GITHUB_EVENT_NAME='workflow_dispatch', GITHUB_REF='refs/heads/main', PORTICO_ENGINE_BUILD_CONTEXT='development',
                    GITHUB_WORKFLOW_REF='PorticoMediaServer/portico/.github/workflows/ffmpeg-toolchain.yml@refs/heads/main')
        with patch.dict(os.environ, base, clear=True): OCI.guard(write=True)
        for context in ('qualified', 'release', ''):
            with patch.dict(os.environ, dict(base, PORTICO_ENGINE_BUILD_CONTEXT=context), clear=True), self.assertRaises(ValueError): OCI.guard()
        branch = dict(base, GITHUB_REF='refs/heads/topic', GITHUB_WORKFLOW_REF='PorticoMediaServer/portico/.github/workflows/ffmpeg-toolchain.yml@refs/heads/topic')
        with patch.dict(os.environ, branch, clear=True):
            OCI.guard()
            with self.assertRaises(ValueError): OCI.guard(write=True)
            OCI.guard(write=True, fixture=True)

    def test_migration_selects_latest_main_and_never_deletes_without_verified_replacement(self):
        prefix = 'engine-dependencies-v2-macos-arm64-'
        rows = [dict(id=1, key=prefix + 'old', ref='refs/heads/main', created_at='2026-11-01'),
                dict(id=2, key=prefix + 'new', ref='refs/heads/main', created_at='2026-11-02'),
                dict(id=3, key='unrelated-ci', ref='refs/heads/main', created_at='2026-11-03')]
        selection = MIGRATE.select('macos-arm64', rows); self.assertEqual(selection['selected']['id'], 2)
        removed = []
        for receipt in ({}, dict(saved=True, roundTripVerified=False)):
            with self.assertRaises(ValueError): MIGRATE.cleanup(selection, receipt, removed.append)
        self.assertFalse(removed)
        receipt = dict(saved=True, roundTripVerified=True, target='macos-arm64', kind='stages',
                       reference='ghcr.io/porticomediaserver/ffmpeg-payload-cache-macos-arm64@sha256:' + 'a' * 64)
        self.assertEqual(MIGRATE.cleanup(selection, receipt, removed.append), [1, 2])
        self.assertNotIn(3, removed)


if __name__ == '__main__': unittest.main()
