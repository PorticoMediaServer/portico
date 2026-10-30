import hashlib
import importlib.util
import io
import json
import lzma
from pathlib import Path
import struct
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('sonic_bundle', Path(__file__).with_name('sonic-bundle.py'))
bundle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bundle)


class SonicBundleTests(unittest.TestCase):
    def fixture(self, root, target='linux-x64'):
        runtime, model_lock = bundle.locks()
        model = root / 'model'
        model.mkdir()
        (model / 'model.onnx').write_bytes(b'unit fixture, never executed as a model')
        (model / 'LICENSE.md').write_bytes((bundle.ROOT / 'third_party/musicnn/LICENSE.md').read_bytes())
        model_lock['onnx']['bytes'] = (model / 'model.onnx').stat().st_size
        model_lock['onnx']['sha256'] = bundle.digest(model / 'model.onnx')
        descriptor = dict(id=model_lock['modelId'], version=model_lock['onnx']['version'], licence='ISC',
                          sourceCommit=model_lock['sourceCommit'], format='onnx', sha256=model_lock['onnx']['sha256'],
                          bytes=model_lock['onnx']['bytes'], preprocessing='musicnn-logmel-1', input='log_mel',
                          embedding='embedding', tags='tags', frames=187, mels=96, dimensions=200, tagCount=50,
                          dependencies={'tensorflow-cpu': '2.15.1', 'tf2onnx': '1.16.1', 'onnx': '1.16.2',
                                        'onnxruntime': '1.20.1', 'numpy': '1.26.4'})
        (model / 'model.json').write_text(json.dumps(descriptor))
        sdk = root / 'sdk'
        (sdk / 'lib').mkdir(parents=True)
        header = bytearray(64)
        header[:6] = b'\x7fELF\x02\x01'
        struct.pack_into('<H', header, 18, bundle.TARGETS[target])
        helper = root / 'portico-sonic'
        helper.write_bytes(header)
        real = sdk / 'lib/libonnxruntime.so.1.20.1'
        real.write_bytes(header)
        (sdk / 'lib/libonnxruntime.so.1').symlink_to(real.name)
        (sdk / 'lib/libonnxruntime.so').symlink_to(real.name)
        (sdk / 'LICENSE').write_text('Permission is hereby granted, free of charge')
        (sdk / 'ThirdPartyNotices.txt').write_text('original fixture notices')
        lock_patch = patch.object(bundle, 'locks', return_value=(runtime, model_lock))
        lock_patch.start()
        self.addCleanup(lock_patch.stop)
        def needed(file):
            return ['libonnxruntime.so.1', 'libc.so.6'] if file == helper else ['libstdc++.so.6', 'libm.so.6']
        return helper, sdk, model, needed

    def package(self, root, target='linux-x64'):
        helper, sdk, model, needed = self.fixture(root, target)
        archive = root / 'bundle.tar.xz'
        with patch.object(bundle, 'needed', side_effect=needed):
            bundle.package(helper, sdk, model, target, archive, 'a' * 40)
        output = root / 'extracted'
        bundle.extract(archive, output, target)
        return archive, output

    def test_actual_needed_closure_only_and_original_notices_survive_round_trip(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _, output = self.package(root)
            manifest = bundle.verify(output, 'linux-x64')
            self.assertEqual(manifest['externalSystemLibraries'], ['libc.so.6', 'libm.so.6', 'libstdc++.so.6'])
            self.assertEqual((output / 'licenses/sonic/onnxruntime-ThirdPartyNotices.txt').read_bytes(), b'original fixture notices')
            self.assertEqual((output / 'lib/libonnxruntime.so.1').resolve(), output / 'lib/libonnxruntime.so.1.20.1')
            self.assertTrue((output / 'source/portico-sonic/third_party/portico-sonic/main.cpp').is_file())
            self.assertFalse((output / 'include').exists())

    def test_changed_helper_model_alias_and_unlisted_members_are_refused(self):
        for change in ('helper', 'model', 'alias', 'unlisted', 'target'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                _, output = self.package(Path(temporary))
                if change == 'helper':
                    (output / 'portico-sonic').write_bytes(b'changed')
                elif change == 'model':
                    (output / 'musicnn-mtt/model.onnx').write_bytes(b'changed')
                elif change == 'alias':
                    alias = output / 'lib/libonnxruntime.so.1'
                    alias.unlink()
                    alias.symlink_to('/etc/passwd')
                elif change == 'unlisted':
                    (output / 'unlisted').write_bytes(b'extra')
                with self.assertRaises(ValueError):
                    bundle.verify(output, 'linux-arm64' if change == 'target' else 'linux-x64')

    def test_closed_descriptor_uses_exact_numeric_types_and_original_licence(self):
        for field, value in [('frames', True), ('bytes', 1.0), ('dimensions', 201), ('licence', 'NC'), ('dependencies', {})]:
            with self.subTest(field=field), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                _, _, model, _ = self.fixture(root)
                descriptor = json.loads((model / 'model.json').read_text())
                descriptor[field] = value
                (model / 'model.json').write_text(json.dumps(descriptor))
                with self.assertRaises(ValueError):
                    bundle.model_identity(model, bundle.locks()[1])

    def test_archive_escape_links_duplicates_and_special_files_never_publish(self):
        for kind in ('escape', 'link', 'duplicate', 'device'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive = root / 'hostile.tar.xz'
                with tarfile.open(archive, 'w:xz') as stream:
                    member = tarfile.TarInfo('../escape' if kind == 'escape' else 'entry')
                    member.size = 1
                    if kind == 'link':
                        member.type, member.linkname, member.size = tarfile.SYMTYPE, '/etc/passwd', 0
                    elif kind == 'device':
                        member.type, member.size = tarfile.CHRTYPE, 0
                    stream.addfile(member, io.BytesIO(b'x') if member.isfile() else None)
                    if kind == 'duplicate':
                        stream.addfile(member, io.BytesIO(b'x'))
                with self.assertRaises(ValueError):
                    bundle.extract(archive, root / 'out', 'linux-x64')
                self.assertFalse((root / 'out').exists())
                self.assertFalse((root.parent / 'escape').exists())

    def test_unknown_dependency_and_wrong_architecture_never_package(self):
        for kind in ('dependency', 'architecture'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                helper, sdk, model, _ = self.fixture(root)
                with patch.object(bundle, 'needed', return_value=['libunapproved.so']):
                    with self.assertRaises(ValueError):
                        bundle.package(helper, sdk, model, 'linux-arm64' if kind == 'architecture' else 'linux-x64', root / 'out.tar.xz', 'a' * 40)
                self.assertFalse((root / 'out.tar.xz').exists())

    def test_existing_output_and_changed_download_leave_original_untouched(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, output = self.package(root)
            original = (output / 'portico-sonic').read_bytes()
            with self.assertRaises(ValueError):
                bundle.extract(archive, output, 'linux-x64')
            self.assertEqual((output / 'portico-sonic').read_bytes(), original)
            tag = bundle.locks()[0]['releaseTag']
            sums = ('0' * 64 + '  portico-sonic-' + tag + '-linux-x64.tar.xz\n').encode()
            with patch.object(bundle.urllib.request, 'urlopen', side_effect=[io.BytesIO(sums), io.BytesIO(b'changed archive')]):
                with self.assertRaises(ValueError):
                    bundle.fetch(root / 'new', 'linux-x64')
            self.assertFalse((root / 'new').exists())

    def test_installed_union_preserves_original_ffmpeg_guard_and_rejects_changes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _, output = self.package(root)
            ffmpeg = root / 'ffmpeg'
            (ffmpeg / 'bin').mkdir(parents=True)
            (ffmpeg / 'bin/ffmpeg').write_bytes(b'original FFmpeg fixture')
            original = {'target': 'linux-x64', 'files': {'bin/ffmpeg': bundle.digest(ffmpeg / 'bin/ffmpeg')}}
            (ffmpeg / 'toolchain-manifest.json').write_text(json.dumps(original))
            calls = []
            def original_verifier(command, **kwargs):
                self.assertEqual(command[:2], ['python3', str(bundle.ROOT / 'scripts/verify-ffmpeg-manifest.py')])
                verified = Path(command[2])
                paths = {p.relative_to(verified).as_posix() for p in verified.rglob('*') if p.is_file()}
                self.assertEqual(paths, {'bin/ffmpeg', 'toolchain-manifest.json'})
                self.assertEqual(json.loads((verified / 'toolchain-manifest.json').read_text()), original)
                if bundle.digest(verified / 'bin/ffmpeg') != original['files']['bin/ffmpeg']:
                    raise ValueError('original FFmpeg verifier refused changed bytes')
                calls.append(verified)
            with patch.object(bundle.subprocess, 'run', side_effect=original_verifier):
                bundle.install(output, ffmpeg, 'linux-x64')
                self.assertEqual(len(calls), 2)
                bundle.verify_installed(ffmpeg, 'linux-x64')
                (ffmpeg / 'bin/extra').write_bytes(b'undeclared')
                with self.assertRaises(ValueError):
                    bundle.verify_installed(ffmpeg, 'linux-x64')
                (ffmpeg / 'bin/extra').unlink()
                (ffmpeg / 'bin/ffmpeg').write_bytes(b'changed')
                with self.assertRaises(ValueError):
                    bundle.verify_installed(ffmpeg, 'linux-x64')

    def test_install_collision_is_refused_before_any_sonic_file_is_copied(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            _, output = self.package(root)
            ffmpeg = root / 'ffmpeg'
            (ffmpeg / 'bin').mkdir(parents=True)
            original = ffmpeg / 'bin/portico-sonic'
            original.write_bytes(b'keep existing')
            (ffmpeg / 'toolchain-manifest.json').write_text(json.dumps({'target': 'linux-x64'}))
            with patch.object(bundle.subprocess, 'run'):
                with self.assertRaises(ValueError):
                    bundle.install(output, ffmpeg, 'linux-x64')
            self.assertEqual(original.read_bytes(), b'keep existing')
            self.assertEqual([p.name for p in (ffmpeg / 'bin').iterdir()], ['portico-sonic'])

    def test_genuine_ffmpeg_verifier_accepts_composition_and_refuses_tamper_extra_and_symlinks(self):
        # This uses the real unchanged verifier, not the mocked boundary above.
        # Tiny ARM inventory bytes exercise that verifier's structural/hash
        # contract; they do not claim real media or native ARM conformance.
        for change in ('none', 'tamper', 'extra', 'member-link', 'parent-link'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                _, output = self.package(root, 'linux-arm64')
                ffmpeg = root / 'ffmpeg'
                ffmpeg.mkdir()
                names = ['bin/ffmpeg', 'bin/ffprobe', 'bin/portico-disc', 'corresponding-source.tar.xz',
                         'NOTICE.md', 'sources.lock.json', 'requirements.v1.json', 'portico-dovi-profile.v1.json',
                         'portico-disc-build.json', 'LICENSES/portico-disc.txt']
                for name in names:
                    file = ffmpeg / name
                    file.parent.mkdir(parents=True, exist_ok=True)
                    file.write_bytes(b'synthetic structural inventory fixture: ' + name.encode())
                manifest = {'schemaVersion': 1, 'licenseMode': 'gpl3', 'target': 'linux-arm64',
                            'files': {name: bundle.digest(ffmpeg / name) for name in names}}
                (ffmpeg / 'toolchain-manifest.json').write_text(json.dumps(manifest))
                bundle.install(output, ffmpeg, 'linux-arm64')
                if change == 'none':
                    bundle.verify_installed(ffmpeg, 'linux-arm64')
                    continue
                if change == 'tamper':
                    (ffmpeg / 'bin/ffmpeg').write_bytes(b'changed')
                elif change == 'extra':
                    (ffmpeg / 'extra').write_bytes(b'unlisted')
                elif change == 'member-link':
                    file = ffmpeg / 'NOTICE.md'
                    outside = root / 'same-notice'
                    file.rename(outside)
                    file.symlink_to(outside)
                elif change == 'parent-link':
                    directory = ffmpeg / 'LICENSES'
                    outside = root / 'same-licences'
                    directory.rename(outside)
                    directory.symlink_to(outside, target_is_directory=True)
                with self.assertRaises((ValueError, bundle.subprocess.CalledProcessError)):
                    bundle.verify_installed(ffmpeg, 'linux-arm64')


    def model_export(self, root):
        _, _, model, _ = self.fixture(root)
        conformance = root / 'conformance.json'
        conformance.write_text(json.dumps({
            'modelSHA256': bundle.locks()[1]['onnx']['sha256'],
            'reference': 'original-tensorflow-checkpoint',
            'cases': [[0.0] * 250 for _ in range(3)],
        }))
        archive = root / 'model.tar.xz'
        bundle.model_package(archive, model, conformance)
        return archive, conformance

    def test_model_lock_declares_one_release_tag(self):
        text = (bundle.ROOT / 'third_party/musicnn/model.lock.json').read_text()
        self.assertEqual(text.count('"releaseTag"'), 1)

    def test_reviewed_model_export_round_trips_and_never_replaces(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            bundle.model_extract(archive, root / 'exported')
            self.assertEqual(bundle.model_verify(root / 'exported/musicnn-mtt')['sha256'], bundle.locks()[1]['onnx']['sha256'])
            self.assertEqual(json.loads((root / 'exported/model.lock.json').read_text()), bundle.locks()[1])
            self.assertEqual(sorted(p.name for p in (root / 'exported/musicnn-mtt').iterdir()),
                             ['LICENSE.md', 'model.json', 'model.onnx'])
            self.assertEqual(sorted(p.name for p in (root / 'exported').iterdir()),
                             ['conformance.json', 'model.lock.json', 'musicnn-mtt'])
            with self.assertRaises(ValueError):
                bundle.model_extract(archive, root / 'exported')

    def test_conformance_reference_must_be_the_original_checkpoint(self):
        changes = {
            'reference': lambda r: r.update(reference='fixture'),
            'count': lambda r: r.update(cases=r['cases'][:2]),
            'short': lambda r: r.update(cases=[[0.0] * 249 for _ in range(3)]),
            'nonfinite': lambda r: r['cases'][0].__setitem__(0, float('nan')),
            'integer': lambda r: r['cases'][0].__setitem__(0, 0),
            'hash': lambda r: r.update(modelSHA256='0' * 64),
        }
        for name, change in changes.items():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                _, conformance = self.model_export(root)
                reference = json.loads(conformance.read_text())
                change(reference)
                conformance.write_text(json.dumps(reference))
                with self.assertRaises(ValueError):
                    bundle.model_reference(bundle.locks()[1], conformance)

    def test_model_package_refuses_replacement_and_an_unvalidated_reference(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, conformance = self.model_export(root)
            model = root / 'model'
            with self.assertRaises(ValueError):
                bundle.model_package(archive, model, conformance)
            malformed = root / 'malformed.json'
            malformed.write_text(json.dumps({'modelSHA256': bundle.locks()[1]['onnx']['sha256']}))
            with self.assertRaises(ValueError):
                bundle.model_package(root / 'refused.tar.xz', model, malformed)
            self.assertFalse((root / 'refused.tar.xz').exists())

    def test_model_archive_refuses_extension_headers_before_their_payload(self):
        for kind in (b'L', b'x', b'K', b'g', b'1', b'2'):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                header = bytearray(512)
                name = b'././@LongLink'
                header[:len(name)] = name
                header[124:136] = b'99999999999\0'
                header[156:157] = kind
                header[257:262] = b'ustar'
                archive = root / 'extension.tar.xz'
                with lzma.open(archive, 'wb') as stream:
                    stream.write(bytes(header))
                    stream.write(bytes(512))
                with self.assertRaises(ValueError):
                    bundle.model_extract(archive, root / 'out')
                self.assertFalse((root / 'out').exists())

    def test_model_archive_rejects_headers_that_are_not_the_pinned_container(self):
        for change in ('checksum', 'prefix', 'version', 'magic'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                header = bytearray(512)
                header[:12] = b'conformance\0'
                header[124:136] = b'00000000000\0'
                header[156:157] = b'0'
                header[257:263] = b'ustar\0'
                header[263:265] = b'00'
                header[148:156] = b'        '
                total = sum(header)
                header[148:156] = ('%06o' % total).encode() + b'\0 '
                if change == 'checksum':
                    header[148:156] = b'000000\0 '
                elif change == 'prefix':
                    header[345:349] = b'etc/'
                    header[148:156] = b'        '
                    header[148:156] = ('%06o' % sum(header)).encode() + b'\0 '
                elif change == 'version':
                    header[263:265] = b'99'
                    header[148:156] = b'        '
                    header[148:156] = ('%06o' % sum(header)).encode() + b'\0 '
                else:
                    header[257:263] = b'ustar '
                    header[148:156] = b'        '
                    header[148:156] = ('%06o' % sum(header)).encode() + b'\0 '
                archive = root / 'header.tar.xz'
                with lzma.open(archive, 'wb') as stream:
                    stream.write(bytes(header))
                    stream.write(bytes(512))
                    stream.write(bytes(512))
                with self.assertRaises(ValueError):
                    bundle.model_extract(archive, root / 'out')
                self.assertFalse((root / 'out').exists())

    def test_model_archive_refuses_payload_after_the_end_marker(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            data = lzma.open(archive, 'rb').read()
            trailing = root / 'trailing.tar.xz'
            with lzma.open(trailing, 'wb') as stream:
                stream.write(data + b'extra payload')
            with self.assertRaises(ValueError):
                bundle.model_extract(trailing, root / 'out')
            self.assertFalse((root / 'out').exists())

    def test_model_stream_budget_breach_is_not_an_end_of_file(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            stream = bundle.ModelStream(archive, output=2)
            try:
                self.assertEqual(len(stream.read(2)), 2)
                with self.assertRaises(ValueError):
                    stream.read(1)
            finally:
                stream.close()
            truncated = root / 'truncated.tar.xz'
            truncated.write_bytes(archive.read_bytes()[:8])
            stream = bundle.ModelStream(truncated)
            try:
                with self.assertRaises(ValueError):
                    stream.read(512)
            finally:
                stream.close()

    def test_model_stream_refuses_a_dict_beyond_its_memory_limit(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            stream = bundle.ModelStream(archive, memory=1024)
            try:
                with self.assertRaises(Exception):
                    stream.read(512)
            finally:
                stream.close()

    def test_model_stream_accepts_same_chunk_zero_padding(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            padded = root / 'same-chunk-padding.tar.xz'
            padded.write_bytes(archive.read_bytes() + bytes(4))
            bundle.model_extract(padded, root / 'out')
            self.assertTrue((root / 'out/musicnn-mtt/model.onnx').is_file())

    def test_model_stream_refuses_trailing_bytes_in_the_same_chunk(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            trailing = root / 'same-chunk.tar.xz'
            trailing.write_bytes(archive.read_bytes() + b'X')
            with self.assertRaises(ValueError):
                bundle.model_extract(trailing, root / 'out')
            self.assertFalse((root / 'out').exists())

    def test_model_stream_tail_probes_consume_the_compressed_budget(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            data = archive.read_bytes()
            padded = root / 'unbounded-padding.tar.xz'
            padded.write_bytes(data + bytes(4096))
            stream = bundle.ModelStream(padded, compressed=len(data))
            try:
                while stream.read(1 << 20):
                    pass
                with self.assertRaises(ValueError):
                    stream.finish()
            finally:
                stream.close()

    def test_model_stream_refuses_a_second_compressed_stream(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            first = archive.read_bytes()
            second = lzma.compress(b'additional stream')
            # Exercise both decoder.unused_data and a second stream reached by
            # finish() after the decoder's first 64 KiB input chunk.
            for padding in (0, 1 << 16):
                with self.subTest(padding=padding):
                    joined = root / f'joined-{padding}.tar.xz'
                    joined.write_bytes(first + bytes(padding) + second)
                    output = root / f'out-{padding}'
                    with self.assertRaises(ValueError):
                        bundle.model_extract(joined, output)
                    self.assertFalse(output.exists())

    def test_model_archive_requires_the_full_end_of_archive_trailer(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            with lzma.open(archive, 'rb') as source:
                raw = source.read()
            # tarfile pads a record to 10 KiB. Removing only its last blocks
            # leaves the real two-block trailer intact, so locate the trailer
            # immediately after the five member payloads before truncating it.
            end = 0
            members = 0
            while raw[end:end + 512] != bytes(512):
                _, size = bundle.model_header(raw[end:end + 512])
                end += 512 + ((size + 511) // 512) * 512
                members += 1
            self.assertEqual(members, 5)
            self.assertEqual(raw[end:end + 1024], bytes(1024))
            for blocks in (0, 1):
                stripped = root / f'no-trailer-{blocks}.tar.xz'
                with lzma.open(stripped, 'wb') as sink:
                    sink.write(raw[:end + blocks * 512])
                with self.assertRaises(ValueError):
                    bundle.model_extract(stripped, root / f'out-{blocks}')
                self.assertFalse((root / f'out-{blocks}').exists())

    def test_model_publish_leaves_an_existing_destination_untouched(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            stage = root / 'stage'
            (stage / 'musicnn-mtt').mkdir(parents=True)
            (stage / 'conformance.json').write_text('{}')
            output = root / 'destination'
            output.mkdir()
            (output / 'keep').write_text('existing')
            with self.assertRaises(ValueError):
                bundle.model_publish(stage, output)
            self.assertEqual((output / 'keep').read_text(), 'existing')
            self.assertTrue((stage / 'conformance.json').is_file())

    def test_model_archive_refuses_unexpected_names_immediately(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / 'many.tar.xz'
            with tarfile.open(archive, 'w:xz') as stream:
                for index in range(64):
                    member = tarfile.TarInfo(f'musicnn-mtt/member-{index}')
                    member.size = 0
                    stream.addfile(member)
            with self.assertRaises(ValueError):
                bundle.model_extract(archive, root / 'out')
            self.assertFalse((root / 'out').exists())

    def test_model_archive_refuses_extra_members_changed_bytes_and_changed_pin(self):
        for change in ('extra', 'bytes', 'pin'):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive, _ = self.model_export(root)
                unpacked = root / 'unpacked'
                bundle.model_extract(archive, unpacked)
                if change == 'extra':
                    (unpacked / 'extra').write_bytes(b'unlisted')
                elif change == 'bytes':
                    (unpacked / 'musicnn-mtt/model.onnx').write_bytes(b'changed')
                else:
                    (unpacked / 'model.lock.json').write_text(json.dumps({'modelId': 'other'}))
                changed = root / 'changed.tar.xz'
                with tarfile.open(changed, 'w:xz') as stream:
                    for item in sorted(unpacked.rglob('*')):
                        if item.is_file():
                            stream.add(item, item.relative_to(unpacked).as_posix())
                with self.assertRaises(ValueError):
                    bundle.model_extract(changed, root / 'rejected')
                self.assertFalse((root / 'rejected').exists())

    def test_model_fetch_distinguishes_absence_from_a_broken_release(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with patch.object(bundle, 'model_release_present', return_value=False):
                with self.assertRaisesRegex(ValueError, 'release is absent'):
                    bundle.model_fetch(root / 'absent')
            self.assertFalse((root / 'absent').exists())

    def test_model_fetch_fails_closed_when_an_existing_release_lacks_assets(self):
        missing = bundle.urllib.error.HTTPError('u', 404, 'Not Found', None, None)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            with patch.object(bundle, 'model_release_present', return_value=True):
                with patch.object(bundle.urllib.request, 'urlopen', side_effect=missing):
                    with self.assertRaisesRegex(ValueError, 'without a checksum list'):
                        bundle.model_fetch(root / 'no-checksums')
            archive, _ = self.model_export(root)
            name = bundle.model_name(bundle.locks()[1])
            sums = (bundle.digest(archive) + '  ' + name + '\n').encode()
            with patch.object(bundle, 'model_release_present', return_value=True):
                with patch.object(bundle.urllib.request, 'urlopen', side_effect=[io.BytesIO(sums), missing]):
                    with self.assertRaisesRegex(ValueError, 'without its pinned archive'):
                        bundle.model_fetch(root / 'no-archive')
            self.assertFalse((root / 'no-checksums').exists())
            self.assertFalse((root / 'no-archive').exists())

    def test_model_fetch_verifies_the_published_checksum(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive, _ = self.model_export(root)
            name = bundle.model_name(bundle.locks()[1])
            sums = (bundle.digest(archive) + '  ' + name + '\n').encode()
            with patch.object(bundle, 'model_release_present', return_value=True):
                with patch.object(bundle.urllib.request, 'urlopen', side_effect=[io.BytesIO(sums), io.BytesIO(archive.read_bytes())]):
                    bundle.model_fetch(root / 'fetched')
                self.assertTrue((root / 'fetched/musicnn-mtt/model.onnx').is_file())
                broken = ('0' * 64 + '  ' + name + '\n').encode()
                with patch.object(bundle.urllib.request, 'urlopen', side_effect=[io.BytesIO(broken), io.BytesIO(archive.read_bytes())]):
                    with self.assertRaises(ValueError):
                        bundle.model_fetch(root / 'other')
            self.assertFalse((root / 'other').exists())

if __name__ == '__main__':
    unittest.main()
