#!/usr/bin/env python3
"""The one version and build number a Portico release stamps into every product.

Until v0.1.0 every product shares one version (DECISIONS, "Versions", 1 Nov 2026): internal
releases are 0.0.N, chosen by the person who starts the release workflow. The build number is
the release commit's position on main (`git rev-list --count`), the same for every product. Main
is append-only, so it only rises, and the same commit always gets the same number.

  release-version.py identity --version 0.0.1 --commit <sha> [--existing v0.0.1,...]
      Checks the version and that the commit is on origin/main; prints version=, tag= and
      build= lines (for $GITHUB_OUTPUT).
  release-version.py stamp --version 0.0.1 --build N --product vega|desktop
      Writes the version into that product's manifest in this checkout (never committed).
      Android and Apple take theirs as build arguments instead (see RELEASING.md).
"""
import argparse
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
# Lifting this guard is the v0.1.0 decision; until then a release is 0.0.N.
ALLOWED = re.compile(r'^0\.0\.(0|[1-9][0-9]*)$')
TAG = re.compile(r'^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:[-+].*)?$')
ANDROID_MAX_VERSION_CODE = 2_100_000_000


def fail(message):
    raise SystemExit('release-version: ' + message)


def parse(version):
    return tuple(int(part) for part in version.split('.'))


def check_version(version, existing):
    if not ALLOWED.match(version):
        fail(f'{version!r} is not 0.0.N; until v0.1.0 every release is 0.0.N')
    released = []
    for tag in existing:
        match = TAG.match(tag)
        if match:
            released.append(tuple(int(match.group(i)) for i in (1, 2, 3)))
    if released and parse(version) <= max(released):
        highest = '.'.join(map(str, max(released)))
        fail(f'{version} is not higher than the highest existing release or tag ({highest})')
    return version


def git(*args):
    return subprocess.run(['git', '-C', str(ROOT), *args], check=True, capture_output=True, text=True).stdout.strip()


def build_number(commit, main_ref='origin/main'):
    if subprocess.run(['git', '-C', str(ROOT), 'merge-base', '--is-ancestor', commit, main_ref]).returncode != 0:
        fail(f'{commit} is not on {main_ref}; a release is built only from main')
    number = int(git('rev-list', '--count', commit))
    if not 1 <= number <= ANDROID_MAX_VERSION_CODE:
        fail(f'build number {number} is outside 1..{ANDROID_MAX_VERSION_CODE}')
    return number


def stamp(product, version, build, root=ROOT):
    if product == 'vega':
        path = root / 'apps/vega/manifest.toml'
        text = path.read_text()
        # Only the [package] table's version; [os.version] and module versions stay.
        pattern = re.compile(r'(\[package\][^\[]*?\nversion = )"[^"]*"', re.S)
        new, count = pattern.subn(lambda m: m.group(1) + json.dumps(version), text, count=1)
        if count != 1:
            fail(f'{path} has no [package] version')
        path.write_text(new)
    elif product == 'desktop':
        path = root / 'apps/desktop/src-tauri/tauri.conf.json'
        conf = json.loads(path.read_text())
        conf['version'] = version
        path.write_text(json.dumps(conf, indent=2) + '\n')
    else:
        fail(f'unknown product {product!r}')
    return path


def main(argv=None):
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest='command', required=True)
    identity = sub.add_parser('identity')
    identity.add_argument('--version', required=True)
    identity.add_argument('--commit', required=True)
    identity.add_argument('--existing', default='', help='comma-separated existing tags and release tag names')
    identity.add_argument('--main-ref', default='origin/main')
    stamping = sub.add_parser('stamp')
    stamping.add_argument('--version', required=True)
    stamping.add_argument('--build', required=True, type=int)
    stamping.add_argument('--product', required=True, choices=['vega', 'desktop'])
    args = parser.parse_args(argv)
    if args.command == 'identity':
        version = check_version(args.version, [t for t in args.existing.split(',') if t])
        build = build_number(args.commit, args.main_ref)
        print(f'version={version}\ntag=v{version}\nbuild={build}')
    else:
        if not ALLOWED.match(args.version):
            fail(f'{args.version!r} is not 0.0.N')
        print(stamp(args.product, args.version, args.build))


if __name__ == '__main__':
    sys.exit(main())
