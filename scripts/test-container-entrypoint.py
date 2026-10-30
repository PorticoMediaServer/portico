#!/usr/bin/env python3
"""Exercise identity, device groups and timezone validation in an owned container."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import uuid

ROOT = Path(__file__).resolve().parents[1]
IMAGE = os.environ.get('PORTICO_ENTRYPOINT_TEST_IMAGE') or json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())['linuxSourceBuild']['compilerImage']


class EntrypointTests(unittest.TestCase):
    def run_container(self, environment, command):
        with tempfile.TemporaryDirectory(prefix='entrypoint-', dir=os.environ['TMPDIR']) as temporary:
            config = Path(temporary) / 'config'
            config.mkdir(mode=0o777)
            config.chmod(0o777)
            name = 'portico-hardware-entrypoint-' + uuid.uuid4().hex[:12]
            argv = ['docker', 'run', '--rm', '--pull=never', '--name', name,
                    '--label', 'portico.lane=hardware-r29', '--network', 'none',
                    '--cpus', '1', '--memory', '128m', '--memory-swap', '128m',
                    '--group-add', '1234', '-v', str(config) + ':/config',
                    '-v', str(ROOT / 'packaging/docker/entrypoint.sh') + ':/entrypoint:ro']
            for key, value in environment.items():
                argv += ['-e', key + '=' + value]
            try:
                return subprocess.run(argv + [IMAGE, 'sh', '-c', command],
                                      capture_output=True, text=True, timeout=30)
            finally:
                subprocess.run(['docker', 'rm', '-f', name], capture_output=True, timeout=15)

    def test_rootful_identity_umask_timezone_and_actual_device_gid(self):
        # This is a synthetic character node for group discovery, not a GPU.
        result = self.run_container({'PUID': '1001', 'PGID': '1002', 'UMASK': '0027', 'TZ': 'Etc/UTC'},
            '''set -eu; mkdir -p /dev/dri; mknod /dev/dri/renderD128 c 1 3; chgrp 2345 /dev/dri/renderD128
exec /entrypoint sh -c 'test "$(id -u)" = 1001; test "$(id -g)" = 1002; test "$(umask)" = 0027; test "$TZ" = Etc/UTC; id -G | tr " " "\\n" | grep -qx 1234; id -G | tr " " "\\n" | grep -qx 2345; echo PASS' ''')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'PASS')

    def test_timezone_traversal_refused(self):
        result = self.run_container({'TZ': '../etc/passwd'}, 'exec /entrypoint true')
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn('TZ must be an IANA time zone', result.stderr)

    def test_invalid_identity_refused(self):
        result = self.run_container({'PUID': 'invalid'}, 'exec /entrypoint true')
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn('PUID and PGID must be numeric', result.stderr)


if __name__ == '__main__':
    unittest.main()
