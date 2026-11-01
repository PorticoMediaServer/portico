#!/usr/bin/env python3
"""Inert archive/command fixtures, not real ARM compilation or qualification."""
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/repeat-btbn-ffmpeg.py'))
IMAGE = 'sha256:' + 'a' * 64


class ArmRepeat(unittest.TestCase):
    def fixture(self, root, mutation='none'):
        recipe = root / 'recipe'; recipe.mkdir()
        source = root / 'prepared.tar.xz'; source.write_bytes(b'controlled source bytes, not an engine archive')
        shutil.copyfile(source, recipe / 'portico-ffmpeg-source.tar.xz')
        script = '''#!/bin/bash
set -e
IMAGE=must-not-be-used
docker(){ return 1; }
if docker info -f fixture; then :; fi
printf '%s\\n' "$IMAGE" >> calls.txt
python3 - <<'PYFIXTURE'
import io,pathlib,tarfile
root=pathlib.Path('.');count=len((root/'calls.txt').read_text().splitlines())
(root/'artifacts').mkdir(exist_ok=True)
with tarfile.open(root/'artifacts/fixture.tar.xz','w:xz') as out:
 for name in ('ffmpeg','ffprobe','portico-disc'):
  data=b'INERT transport fixture: '+name.encode()
  if MUTATION=='second-output' and count==2:data+=b'changed'
  member=tarfile.TarInfo('fixture/bin/'+name);member.size=len(data);out.addfile(member,io.BytesIO(data))
if MUTATION=='source':(root/'portico-ffmpeg-source.tar.xz').write_bytes(b'changed source')
PYFIXTURE
'''.replace('MUTATION', repr(mutation))
        (recipe / 'build.sh').write_text(script)
        M['configure'](recipe)
        return recipe, source

    def build(self, recipe, source, work):
        with patch.dict(M['build'].__globals__, image_identity=lambda *args: ('fixture/arm:latest', IMAGE)):
            return M['build'](recipe, source, work)

    def test_two_actual_fixture_producer_invocations_use_one_digest_and_seal_matching_transport_hashes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); recipe, source = self.fixture(root)
            prefix = self.build(recipe, source, root / 'repeat')
            self.assertEqual((recipe / 'calls.txt').read_text().splitlines(), [IMAGE, IMAGE])
            rows = json.loads((prefix / 'repeat-build.json').read_text())
            self.assertEqual([row['round'] for row in rows], [1, 2])
            self.assertEqual(rows[0]['binaries'], rows[1]['binaries'])
            self.assertEqual({row['compilerImageId'] for row in rows}, {IMAGE})
            self.assertEqual((prefix / 'bin/ffmpeg').stat().st_mode & 0o777, 0o755)
            report = json.loads((root / 'repeat/repeat-compilation.json').read_text())
            self.assertFalse(report['qualified']); self.assertFalse(report['fullCompilerAcceptance'])
            self.assertFalse(report['release'])

    def test_changed_source_or_second_output_and_stale_artifacts_refuse_without_a_receipt(self):
        for mutation in ('source', 'second-output', 'stale'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary); recipe, source = self.fixture(root, mutation)
                if mutation == 'stale':
                    (recipe / 'artifacts').mkdir(); (recipe / 'artifacts/stale.tar.xz').write_bytes(b'stale')
                with self.assertRaises(ValueError):
                    self.build(recipe, source, root / 'repeat')
                self.assertFalse((root / 'repeat/repeat-compilation.json').exists())

    def test_wrong_image_platform_architecture_or_malformed_digest_refuses(self):
        base = dict(Id=IMAGE, Architecture='arm64', Os='linux')
        for key, value in [('Architecture', 'amd64'), ('Os', 'windows'), ('Id', 'latest')]:
            data = dict(base, **{key: value})
            with patch('subprocess.check_output', side_effect=['fixture/arm:latest', json.dumps([data])]):
                with self.assertRaises(ValueError):
                    M['image_identity'](ROOT, os.environ)

    def test_runtime_guard_rejects_non_digest_override_before_any_fixture_producer(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); recipe, _ = self.fixture(root)
            result = subprocess.run(['bash', './build.sh'], cwd=recipe,
                env=dict(os.environ, PORTICO_ENGINE_REPEAT_IMAGE='latest; echo bad'), capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((recipe / 'calls.txt').exists())


if __name__ == '__main__':
    unittest.main()
