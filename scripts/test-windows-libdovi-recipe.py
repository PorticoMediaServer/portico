#!/usr/bin/env python3
import json
from pathlib import Path
import runpy
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/configure-windows-libdovi.py'))


class WindowsDoviRecipe(unittest.TestCase):
    def test_checked_source_and_all_licence_inputs_are_preserved(self):
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            recipe = Path(tmp)
            (recipe / 'scripts.d').mkdir()
            (recipe / 'variants').mkdir()
            (recipe / 'scripts.d/zz-final.sh').write_text('ffbuild_depends() {\n    echo libbluray\n}\n')
            (recipe / 'download.sh').write_text('docker run -i image command\n')
            (recipe / 'build.sh').write_text('    make install install-doc\n')
            (recipe / 'variants/windows-install-static.sh').write_text('    cp "$IN"/bin/* "$OUT"/bin\n')
            M['configure'](recipe)
            script = recipe / 'scripts.d/50-portico-libdovi.sh'
            subprocess.run(['bash', '-n', script], check=True)
            for target, expected in [('win64', 0), ('winarm64', 0), ('linux64', 1)]:
                result = subprocess.run(['bash', '-c', 'source "$1"; TARGET="$2"; ffbuild_enabled', 'check', script, target])
                self.assertEqual(result.returncode, expected)
            text = script.read_text()
            lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())['libdovi']
            for expected in (lock['commit'], lock['source'], lock['sha256'], *lock['cargoLocks'].values()):
                self.assertIn(expected, text)
            self.assertIn('--locked --offline', text)
            self.assertIn('--target "$FFBUILD_RUST_TARGET"', text)
            self.assertIn("['inside'](stage, prefix, target)", text)
            self.assertNotIn('@COMMIT@', text)
            self.assertFalse((recipe / 'portico-libdovi').exists())
            self.assertEqual((recipe / 'portico-dovi-inputs/contract.json').read_bytes(), (ROOT / lock['contract']).read_bytes())
            self.assertIn('echo portico-libdovi', (recipe / 'scripts.d/zz-final.sh').read_text())
            self.assertIn('"$PWD/portico-dovi-inputs":/portico-libdovi:ro', (recipe / 'download.sh').read_text())
            self.assertIn('/opt/ffbuild/LICENSES', (recipe / 'build.sh').read_text())
            self.assertIn('/opt/ffbuild/share/doc/ffmpeg/libdovi-build.json', (recipe / 'build.sh').read_text())


if __name__ == '__main__':
    unittest.main()
