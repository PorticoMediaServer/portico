#!/usr/bin/env python3
"""Exercise the approved Intel-only GMP patch without compiling GMP."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class MacGmpPic(unittest.TestCase):
    def test_exact_checked_patch_preserves_every_non_intel_configure_flag(self):
        entry = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())['macosRecipe']['intelGmpPicPatch']
        patch = ROOT / entry['path']
        self.assertEqual(hashlib.sha256(patch.read_bytes()).hexdigest(), entry['sha256'])
        lines = patch.read_text().splitlines(keepends=True)
        # Reconstruct the unchanged context of this one reviewed upstream hunk.
        original = ''.join(line[1:] for line in lines if line.startswith((' ', '-')) and not line.startswith('---'))
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            recipe = Path(tmp)
            script = recipe / 'build-ffmpeg'
            script.write_text(original)
            subprocess.run(['patch', '--batch', '--fuzz=0', '-p1', '-i', patch], cwd=recipe,
                           check=True, stdout=subprocess.DEVNULL)
            subprocess.run(['bash', '-n', script], check=True)
            for os_name, arch, pic in [('darwin', 'x86_64', True), ('darwin', 'arm64', False),
                                       ('linux', 'x86_64', False)]:
                trace = recipe / 'configure-args'
                command = ('TARGET_OS="$1"; TARGET_ARCH="$2"; WORKSPACE="/prefix with spaces"; MJOBS=2; '
                    'CURRENT_PACKAGE_VERSION=6.3.0; download(){ :; }; '
                    'execute(){ if [[ "$1" == ./configure ]]; then printf "%s\\0" "$@" > "$TRACE"; fi; }; ')
                command += 'TRACE="$3"; source "$4"'
                subprocess.run(['bash', '-c', command, 'control', os_name, arch, trace, script], check=True)
                expected = ['./configure', '--prefix=/prefix with spaces', '--disable-shared', '--enable-static']
                if pic:
                    expected.append('--with-pic')
                self.assertEqual(trace.read_bytes().decode().rstrip('\0').split('\0'), expected)
            self.assertNotIn('--disable-assembly', script.read_text())
            self.assertNotIn('read_only_relocs', script.read_text())

    def test_source_builder_applies_patch_only_to_native_intel_mac_target(self):
        text = (ROOT / 'scripts/build-ffmpeg.sh').read_text()
        start = text.index('  # Reviewed PIC assembly')
        end = text.index('  # Build tools and downloads', start)
        block = text[start:end]
        with tempfile.TemporaryDirectory(dir=ROOT) as tmp:
            for target in ('macos-x64', 'macos-arm64'):
                trace = Path(tmp) / target
                command = 'set -e; ROOT="$1"; WORK="$2"; TARGET="$3"; TRACE="$4"; git(){ printf "%s\\n" "$*" >> "$TRACE"; };\n'
                subprocess.run(['bash', '-c', command + block, 'control', ROOT, tmp, target, trace], check=True)
                self.assertEqual(trace.exists(), target == 'macos-x64')
                if trace.exists():
                    self.assertEqual(len(trace.read_text().splitlines()), 2)
            fake_root = Path(tmp) / 'other-source'
            lock = fake_root / 'third_party/ffmpeg/sources.lock.json'
            lock.parent.mkdir(parents=True)
            other = fake_root / 'different.patch'
            other.write_bytes(b'different checked input than the consumed patch')
            lock.write_text(json.dumps({'macosRecipe': {'intelGmpPicPatch': {
                'path': 'different.patch', 'target': 'macos-x64',
                'sha256': hashlib.sha256(other.read_bytes()).hexdigest()}}}))
            trace = Path(tmp) / 'refused'
            result = subprocess.run(['bash', '-c', command + block, 'control', fake_root,
                                     tmp, 'macos-x64', trace], capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(trace.exists())


if __name__ == '__main__':
    unittest.main()
