#!/usr/bin/env python3
"""Prove checkout bytes match the sealed Git contract; never normalize hashes."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
FILES = ('third_party/ffmpeg/requirements.v1.json', 'third_party/ffmpeg/sources.lock.json',
         'fixtures/discs/manifest.json', 'fixtures/discs/dovi/manifest.json',
         'packages/reader-core/assets/OFL.txt')


def blob(name):
    return subprocess.check_output(['git', '-C', str(ROOT), 'show', 'HEAD:' + name])


def inspect():
    records = {}
    for name in FILES:
        checked, raw = blob(name), (ROOT / name).read_bytes()
        records[name] = dict(gitSHA256=hashlib.sha256(checked).hexdigest(),
            checkoutSHA256=hashlib.sha256(raw).hexdigest(), matches=raw == checked,
            crlf=raw.count(b'\r\n'), lf=raw.count(b'\n'))
    config = subprocess.run(['git', '-C', str(ROOT), 'config', '--show-origin', '--get', 'core.autocrlf'],
                            capture_output=True, text=True)
    return dict(autocrlf=config.stdout.strip(), files=records, release=False, qualified=False)


class CheckoutBytes(unittest.TestCase):
    def test_native_contract_files_are_exact_git_bytes(self):
        report = inspect()
        self.assertEqual([name for name, row in report['files'].items() if not row['matches']], [], report)

    def test_real_git_crlf_filter_reproduces_the_hash_mismatch_and_lf_checkout_fixes_it(self):
        name = FILES[0]; expected = blob(name)
        self.assertIn(b'\n', expected); self.assertNotIn(b'\r\n', expected)
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            root = Path(temporary)
            for setting in ('true', 'false'):
                destination = root / setting; destination.mkdir()
                subprocess.run(['git', '-C', str(ROOT), '-c', 'core.autocrlf=' + setting,
                    'checkout-index', '--prefix=' + destination.as_posix() + '/', '--', name], check=True)
                actual = (destination / name).read_bytes()
                if setting == 'true':
                    self.assertIn(b'\r\n', actual)
                    self.assertNotEqual(hashlib.sha256(actual).hexdigest(), hashlib.sha256(expected).hexdigest())
                else:
                    self.assertEqual(actual, expected)


if __name__ == '__main__':
    if '--inspect' in sys.argv:
        parser = argparse.ArgumentParser(description=__doc__)
        parser.add_argument('--inspect', action='store_true')
        parser.add_argument('--report', type=Path, required=True)
        args = parser.parse_args(); result = inspect()
        args.report.write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result))
    else:
        unittest.main()
