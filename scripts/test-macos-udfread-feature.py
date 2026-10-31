"""Feature-probe refusal controls; mocked protocol output is not native proof."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('features', Path(__file__).with_name('verify-ffmpeg-features.py'))
features = importlib.util.module_from_spec(spec)
spec.loader.exec_module(features)


class MacUdfreadTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1])
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        rows = []
        for identity in features.bluray.IDS:
            path = self.root / identity
            path.write_bytes(b'disposable protocol transport fixture')
            rows.append({'id': identity, 'path': identity,
                         'files': {'.': features.disc.digest(path)}})
        manifest = self.root / 'manifest.json'
        manifest.write_text(json.dumps({'fixtures': rows}))
        self.lock = {'discHelper': {'fixtureManifest': 'manifest.json',
                                   'fixtureManifestSHA256': features.disc.digest(manifest)}}

    def response(self, args):
        if '-h' in args:
            return 0, ' -min_playlist_length <int> (from 0 to INT_MAX) (default 180)', ''
        if '-min_playlist_length' not in args:
            return 1, '', '0 usable playlists'
        if args[args.index('-min_playlist_length') + 1] == '-1':
            return 1, '', 'out of range'
        return 0, '0, 0, 0, 1, 1, ' + 'a' * 64, ''

    def test_both_mac_targets_require_the_same_frame_through_the_actual_iso_reader(self):
        for target in ('macos-x64', 'macos-arm64'):
            with self.subTest(target=target), patch.object(features.bluray, 'run_tool', side_effect=self.response) as run:
                result = features.macos_udfread(Path('transport-ffmpeg'), target, self.lock, self.root)
                self.assertEqual({row['id'] for row in result['fixtures']}, set(features.bluray.IDS))
                self.assertTrue(all(row['minimum60Decoded'] for row in result['fixtures']))
                self.assertTrue(any('bluray:' + str(self.root / 'bluray-udf250-iso') in call.args[0]
                                    for call in run.call_args_list))

    def test_folder_only_link_missing_iso_and_changed_iso_fail(self):
        def folder_only(args):
            if any(arg.endswith('bluray-udf250-iso') for arg in args) and '-min_playlist_length' in args:
                return 1, '', 'UDF reader unavailable'
            return self.response(args)
        with patch.object(features.bluray, 'run_tool', side_effect=folder_only):
            with self.assertRaisesRegex(ValueError, 'did not decode'):
                features.macos_udfread(Path('transport-ffmpeg'), 'macos-arm64', self.lock, self.root)
        iso = self.root / 'bluray-udf250-iso'
        iso.write_text('changed fixture')
        with self.assertRaisesRegex(ValueError, 'inventory/hash changed'):
            features.macos_udfread(Path('transport-ffmpeg'), 'macos-x64', self.lock, self.root)
        iso.unlink()
        with self.assertRaisesRegex(ValueError, 'fixture missing'):
            features.macos_udfread(Path('transport-ffmpeg'), 'macos-x64', self.lock, self.root)

    def test_non_mac_targets_keep_their_existing_probes(self):
        with patch.object(features.bluray, 'run_tool', side_effect=AssertionError('unexpected Mac probe')):
            for target in ('windows-x64', 'windows-arm64', 'linux-x64', 'linux-arm64'):
                self.assertIsNone(features.macos_udfread(Path('unused'), target, {}, self.root))


if __name__ == '__main__':
    unittest.main()
