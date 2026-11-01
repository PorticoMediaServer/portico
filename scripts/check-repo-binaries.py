#!/usr/bin/env python3
"""Reject built executables and files over 50 MiB; notice files over 1 MiB."""
import argparse
import html
import os
import subprocess
import sys
from pathlib import Path

NOTICE_LIMIT = 1024 * 1024
LIMIT = 50 * 1024 * 1024


def check_file(root, name):
    path = root / name
    if not path.is_file():
        return None, None
    with path.open('rb') as stream:
        magic = stream.read(4)
    if magic in [bytes.fromhex(v) for v in ['7f454c46', 'cffaedfe', 'cefaedfe', 'feedfacf', 'feedface', 'cafebabe']] or magic.startswith(b'MZ'):
        return 'built executable in Git: ' + name, None
    size = path.stat().st_size
    notice = f'large tracked file: {name} ({size} bytes; notice above 1 MiB)' if size > NOTICE_LIMIT else None
    error = f'file exceeds Git size limit: {name} ({size} bytes; limit {LIMIT})' if size > LIMIT else None
    return error, notice


def report_notices(notices):
    for message in notices:
        # Git filenames may contain workflow command delimiters or newlines.
        escaped = message.replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
        print('::notice::' + escaped)
    summary = os.environ.get('GITHUB_STEP_SUMMARY')
    if notices and summary:
        with Path(summary).open('a') as stream:
            stream.write('\n### Tracked files over 1 MiB\n\n')
            stream.write('Large files may have a real reason to exist. The size check fails only above 50 MiB.\n\n<ul>\n')
            for message in notices:
                stream.write('<li><code>' + html.escape(message) + '</code></li>\n')
            stream.write('</ul>\n')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--staged', action='store_true')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    command = ['git', 'diff', '--cached', '--name-only', '--diff-filter=AM', '-z'] if args.staged else ['git', 'ls-files', '-z']
    names = [name for name in subprocess.check_output(command, cwd=root).decode().split('\0') if name]
    errors, notices = [], []
    for name in names:
        error, notice = check_file(root, name)
        if error:
            errors.append(error)
        if notice:
            notices.append(notice)
    report_notices(notices)
    for message in errors:
        print(message, file=sys.stderr)
    if errors:
        return 1
    print(f'Binary/size policy passed for {len(names)} tracked paths.')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
