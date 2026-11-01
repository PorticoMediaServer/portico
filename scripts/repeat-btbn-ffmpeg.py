#!/usr/bin/env python3
"""Two fresh Linux ARM terminal compiles with one actual dependency image."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
TOOLS = ('ffmpeg', 'ffprobe', 'portico-disc')


def module(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / (name + '.py'))
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value)
    return value


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def configure(recipe):
    path = recipe / 'build.sh'; text = path.read_text()
    marker = 'if docker info -f '
    guard = '''if [[ -n "${PORTICO_ENGINE_REPEAT_IMAGE:-}" ]]; then
    [[ "$PORTICO_ENGINE_REPEAT_IMAGE" =~ ^sha256:[a-f0-9]{64}$ ]] || exit 1
    IMAGE="$PORTICO_ENGINE_REPEAT_IMAGE"
fi

'''
    if text.count(marker) != 1 or 'PORTICO_ENGINE_REPEAT_IMAGE' in text:
        raise ValueError('pinned BtbN runtime image selection boundary changed')
    path.write_text(text.replace(marker, guard + marker, 1))


def image_identity(recipe, env):
    factory = module('engine-final-cache').shell_setup() + 'printf "%s" "$IMAGE"\n'
    name = subprocess.check_output(['bash', '-c', factory, 'repeat-image', 'linuxarm64'],
                                   cwd=recipe, env=env, text=True).strip()
    if not re.fullmatch(r'[a-z0-9][a-z0-9./:_-]+', name):
        raise ValueError('invalid actual dependency image name')
    values = json.loads(subprocess.check_output(['docker', 'image', 'inspect', name], env=env, text=True))
    if (len(values) != 1 or values[0].get('Os') != 'linux' or values[0].get('Architecture') != 'arm64' or
            not re.fullmatch(r'sha256:[a-f0-9]{64}', str(values[0].get('Id')))):
        raise ValueError('actual native ARM dependency image identity required')
    return name, values[0]['Id']


def build(recipe, source, work):
    if 'IMAGE="$PORTICO_ENGINE_REPEAT_IMAGE"' not in (recipe / 'build.sh').read_text():
        raise ValueError('explicit immutable runtime image guard required')
    if work.exists() or work.is_symlink():
        raise ValueError('fresh repeat-build directory required')
    work.mkdir()
    env = dict(os.environ)
    name, identity = image_identity(recipe, env)
    env['PORTICO_ENGINE_REPEAT_IMAGE'] = identity
    source_sha = digest(source)
    if source_sha != digest(recipe / 'portico-ffmpeg-source.tar.xz'):
        raise ValueError('consumed source differs from the checked prepared archive')
    artifacts = recipe / 'artifacts'
    if artifacts.exists() and list(artifacts.iterdir()):
        raise ValueError('stale raw artifacts cannot count as a compile round')
    cache = module('engine-final-cache'); extractor = module('fetch-engine')
    fingerprints, prefixes, records = [], [], []
    for index in (1, 2):
        if digest(recipe / 'portico-ffmpeg-source.tar.xz') != source_sha:
            raise ValueError('consumed source changed before a compile round')
        log = work / ('compile-' + str(index) + '.log')
        with log.open('wb') as output:
            subprocess.run(['bash', './build.sh', 'linuxarm64', 'gpl', '8.1'], cwd=recipe,
                           env=env, stdout=output, stderr=subprocess.STDOUT, check=True)
        archives = list(artifacts.iterdir())
        if len(archives) != 1 or archives[0].is_symlink() or not archives[0].is_file() or not archives[0].name.endswith('.tar.xz'):
            raise ValueError('exactly one fresh ARM raw artifact required')
        archive = work / ('round-' + str(index) + '.tar.xz'); archives[0].rename(archive)
        expanded = work / ('round-' + str(index)); extractor.extract(archive, expanded)
        roots = list(expanded.iterdir())
        if len(roots) != 1 or roots[0].is_symlink() or not roots[0].is_dir():
            raise ValueError('one raw engine package root required')
        prefix = roots[0]
        fingerprint = cache.round_fingerprint(prefix, recipe / 'portico-ffmpeg-source.tar.xz', 'linux-arm64')
        if fingerprint['sourceArchiveSHA256'] != source_sha:
            raise ValueError('source changed during a compile round')
        for tool in TOOLS:
            (prefix / 'bin' / tool).chmod(0o755)
        prefixes.append(prefix); fingerprints.append(fingerprint)
        records.append(dict(round=index, rawArchiveSHA256=digest(archive), compileLogSHA256=digest(log)))
    rounds = cache.repeat_rounds(*prefixes, recipe / 'portico-ffmpeg-source.tar.xz', fingerprints[0], 'linux-arm64')
    if digest(source) != source_sha:
        raise ValueError('checked prepared source changed during the repeat')
    for row, record in zip(rounds, records):
        row.update(compilerImageId=identity, **{key: value for key, value in record.items() if key != 'round'})
    (prefixes[1] / 'repeat-build.json').write_text(json.dumps(rounds, indent=2) + '\n')
    (work / 'repeat-compilation.json').write_text(json.dumps(dict(target='linux-arm64',
        release=False, qualified=False, fullCompilerAcceptance=False, imageName=name, imageId=identity,
        sourceArchiveSHA256=source_sha, rounds=records, binaryFingerprints=rounds), indent=2) + '\n')
    return prefixes[1]


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--recipe', type=Path, required=True)
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--work', type=Path, required=True)
    args = parser.parse_args()
    print(build(args.recipe.resolve(), args.source.resolve(), args.work.absolute()))
