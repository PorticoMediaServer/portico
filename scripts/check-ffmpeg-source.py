#!/usr/bin/env python3
"""Run the source/patch authority check on an exact published runner archive."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--archive-commit', required=True)
    parser.add_argument('--archive-rejections', action='store_true')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    os.environ['PORTICO_RUNNER_SOURCE_COMMIT'] = args.archive_commit
    spec = importlib.util.spec_from_file_location('ffmpeg_build_input_check',
                                                 root / 'scripts/check-ffmpeg-build-inputs.py')
    check = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(check)
    authority = check.verify(root)
    negatives = []
    if args.archive_rejections:
        if (root / '.git').exists():
            raise ValueError('Source tamper negatives are only permitted inside the disposable runner archive')
        del os.environ['PORTICO_RUNNER_SOURCE_COMMIT']
        try:
            check.verify(root)
        except ValueError as error:
            if 'runner-attested' not in str(error):
                raise
            negatives.append('missing runner commit refused')
        else:
            raise ValueError('Unattested source archive was accepted')
        finally:
            os.environ['PORTICO_RUNNER_SOURCE_COMMIT'] = args.archive_commit
        os.environ['PORTICO_RUNNER_SOURCE_COMMIT'] = '0' * 40
        try:
            check.verify(root)
        except ValueError as error:
            if 'runner job directory' not in str(error):
                raise
            negatives.append('wrong runner commit refused')
        else:
            raise ValueError('Different source commit was accepted')
        finally:
            os.environ['PORTICO_RUNNER_SOURCE_COMMIT'] = args.archive_commit
        header = root / 'third_party/portico-disc/mpegts_dovi.h'
        original = header.read_bytes()
        try:
            header.write_bytes(original + b'\nsource-tamper-negative\n')
            try:
                check.verify(root)
            except ValueError as error:
                if 'source/fixture input changed' not in str(error):
                    raise
                negatives.append('changed checked header refused')
            else:
                raise ValueError('Changed checked header was accepted')
        finally:
            header.write_bytes(original)
        check.verify(root)
    with tempfile.TemporaryDirectory(prefix='jellyfin-source-check-', dir=os.environ['TMPDIR']) as tmp:
        tree = Path(tmp) / 'ffmpeg'
        subprocess.run(['python3', str(root / 'scripts/prepare-ffmpeg-source.py'), str(tree)], check=True)
        receipt_file = tree / 'portico-source-receipt.json'
        receipt = json.loads(receipt_file.read_text())
        lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
        if (receipt['sourceCommit'] != lock['ffmpeg']['commit'] or len(receipt['patches']) != 100 or
                receipt['postSeriesPatches'] != lock['ffmpeg']['postSeriesPatches']):
            raise ValueError('Prepared source does not match the canonical patch contract')
        print(json.dumps({'passed': True, 'buildSourceCommit': authority['buildSourceCommit'],
            'checkedMain': authority['checkedMain'], 'sourceCommit': receipt['sourceCommit'],
            'upstreamPatches': len(receipt['patches']), 'postSeriesPatches': receipt['postSeriesPatches'],
            'sourceReceiptSHA256': hashlib.sha256(receipt_file.read_bytes()).hexdigest(),
            'verifiedMainInputs': len(authority['verifiedInputFiles']), 'negativeChecks': negatives,
            'scope': 'Actual source preparation and archive authority only; no target binary or HW1 qualification.'}, indent=2))


if __name__ == '__main__':
    main()
