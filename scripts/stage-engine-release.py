#!/usr/bin/env python3
"""Stage exact main-run Actions artifacts. Read-only intake, never qualification."""
import argparse
from datetime import datetime, timedelta, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = 'PorticoMediaServer/portico'
WORKFLOW = '.github/workflows/ffmpeg-toolchain.yml'
REQUIRED = ('windows-x64', 'linux-x64', 'linux-arm64', 'macos-arm64')
OPTIONAL = ('macos-x64',)
LIMITS = {'product': 3 * 1024**3, 'native': 64 * 1024**2}
SHA = re.compile(r'^[a-f0-9]{64}$')
spec = importlib.util.spec_from_file_location('engine_archive', ROOT / 'scripts/fetch-engine.py')
archive_tools = importlib.util.module_from_spec(spec)
spec.loader.exec_module(archive_tools)


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def unique(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate JSON field: ' + key)
        result[key] = value
    return result


def write(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')


def utc(value):
    parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    if parsed.utcoffset() != timedelta(0):
        raise ValueError('UTC timestamp required')
    return parsed


def validate_locator(locator):
    if (not isinstance(locator, dict) or locator.get('schemaVersion') != 1 or
            locator.get('repository') != REPOSITORY or
            not re.fullmatch(r'[a-f0-9]{40}', str(locator.get('sourceSHA'))) or
            any(type(locator.get(k)) is not int or locator[k] <= 0 for k in ('runId', 'runAttempt'))):
        raise ValueError('exact repository/source/run/attempt locator required')
    targets = locator.get('targets')
    if (not isinstance(targets, dict) or not set(REQUIRED) <= targets.keys() or
            set(targets) - set((*REQUIRED, *OPTIONAL))):
        raise ValueError('four mandatory targets and optional Intel only')
    seen = set()
    for target, entries in targets.items():
        if not isinstance(entries, dict) or set(entries) != {'product', 'native'}:
            raise ValueError('exact product/native artifact pair required: ' + target)
        for pin in entries.values():
            if (not isinstance(pin, dict) or set(pin) != {'artifactId', 'sha256'} or
                    type(pin['artifactId']) is not int or pin['artifactId'] <= 0 or
                    not isinstance(pin['sha256'], str) or not SHA.fullmatch(pin['sha256']) or
                    pin['artifactId'] in seen):
                raise ValueError('unique exact artifact ID and SHA-256 required')
            seen.add(pin['artifactId'])


def validate_run(locator, run):
    if (run.get('id') != locator['runId'] or type(run.get('id')) is not int or
            run.get('run_attempt') != locator['runAttempt'] or type(run.get('run_attempt')) is not int or
            run.get('head_sha') != locator['sourceSHA'] or run.get('head_branch') != 'main' or
            run.get('event') != 'workflow_dispatch' or run.get('path') != WORKFLOW or
            run.get('status') != 'completed' or run.get('conclusion') != 'success' or
            run.get('repository', {}).get('full_name') != REPOSITORY or
            run.get('head_repository', {}).get('full_name') != REPOSITORY):
        raise ValueError('completed successful same-repository main toolchain attempt required')


def validate_artifact(locator, run, target, kind, metadata, now):
    pin = locator['targets'][target][kind]
    name = 'engine-cold-' + ('native-' if kind == 'native' else '') + target + '-' + locator['sourceSHA'] + '-NOT-FOR-USE'
    created, expires = utc(metadata['created_at']), utc(metadata['expires_at'])
    origin = metadata.get('workflow_run', {})
    if (metadata.get('id') != pin['artifactId'] or type(metadata.get('id')) is not int or
            metadata.get('name') != name or metadata.get('expired') is not False or
            metadata.get('digest') != 'sha256:' + pin['sha256'] or
            type(metadata.get('size_in_bytes')) is not int or not 0 < metadata['size_in_bytes'] <= LIMITS[kind] or
            origin.get('id') != locator['runId'] or type(origin.get('id')) is not int or
            origin.get('head_sha') != locator['sourceSHA'] or origin.get('head_branch') != 'main' or
            not utc(run['run_started_at']) <= created <= utc(run['updated_at']) or
            not created <= now < expires or not timedelta(0) < expires - created <= timedelta(days=14)):
        raise ValueError('artifact identity/digest/attempt time/retention mismatch: ' + target + '/' + kind)


def request(endpoint, destination, limit):
    """Fixed GitHub API paths only; bounded stdout, no discovery or retries."""
    command = ['gh', 'api', '--method', 'GET', 'repos/' + REPOSITORY + '/' + endpoint]
    with destination.open('xb') as output:
        process = subprocess.Popen(command, stdout=subprocess.PIPE)
        try:
            total = 0
            while chunk := process.stdout.read(1024**2):
                total += len(chunk)
                if total > limit:
                    raise ValueError('GitHub response exceeds intake bound')
                output.write(chunk)
            if process.wait() != 0:
                raise ValueError('GitHub exact artifact request failed')
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()


def extract(path, output, kind):
    with zipfile.ZipFile(path) as source:
        entries, seen, files, total = [], set(), set(), 0
        for member in source.infolist():
            name = archive_tools.safe_name(member.orig_filename)
            mode = (member.external_attr >> 16) & 0o170000
            if name.casefold() in seen or mode not in (0, 0o100000, 0o040000) or member.flag_bits & 1:
                raise ValueError('duplicate, linked, special or encrypted artifact member')
            if member.is_dir() and (kind == 'product' or name != 'gates'):
                raise ValueError('unexpected artifact directory')
            seen.add(name.casefold())
            if not member.is_dir():
                files.add(name)
            total += member.file_size
            if len(seen) > 256 or total > LIMITS[kind]:
                raise ValueError('artifact expansion exceeds intake bound')
            entries.append((member, name))
        if kind == 'product':
            if files != {'NOT-FOR-USE.txt', 'bundle.tar', 'transfer.json'}:
                raise ValueError('unexpected construction artifact inventory')
        elif (not {'cold-native.json', 'gates/native-engine.json'} <= files or
              any(not ((len(Path(name).parts) == 1 and name.endswith('.json')) or
                       (len(Path(name).parts) == 2 and name.startswith('gates/') and name.endswith(('.json', '.log'))))
                  for name in files)):
            raise ValueError('unexpected native artifact inventory')
        folded_files = {name.casefold() for name in files}
        if any(parent.as_posix().casefold() in folded_files for _, name in entries
               for parent in Path(name).parents if parent.as_posix() != '.'):
            raise ValueError('artifact file/directory collision')
        output.mkdir()
        for member, name in entries:
            path = output / name
            if member.is_dir():
                path.mkdir(parents=True, exist_ok=True)
                continue
            path.parent.mkdir(parents=True, exist_ok=True)
            with source.open(member) as inp, path.open('xb') as out:
                shutil.copyfileobj(inp, out, 1024**2)
            path.chmod(0o444)


def stage(locator, output, transport=request, now=None):
    validate_locator(locator)
    if output.exists() or any(p.is_symlink() for p in (output, *output.parents)):
        raise ValueError('fresh output without linked parents required')
    output.parent.mkdir(parents=True, exist_ok=True)
    now = now or datetime.now(timezone.utc)
    with tempfile.TemporaryDirectory(prefix='engine-release-intake-', dir=output.parent) as temporary:
        work = Path(temporary)
        staged = work / 'staged'; staged.mkdir()
        inputs = staged / 'inputs'; inputs.mkdir()
        raw = staged / 'actions'; raw.mkdir()
        def metadata(endpoint, name):
            path = raw / name
            transport(endpoint, path, 1024**2)
            return json.loads(path.read_text(), object_pairs_hook=unique)
        run = metadata(f"actions/runs/{locator['runId']}/attempts/{locator['runAttempt']}", 'run.json')
        validate_run(locator, run)
        artifacts = []
        for target, entries in locator['targets'].items():
            directory = inputs / target; directory.mkdir()
            for kind, pin in entries.items():
                label = target + '-' + kind
                endpoint = 'actions/artifacts/' + str(pin['artifactId'])
                record = metadata(endpoint, label + '.json')
                validate_artifact(locator, run, target, kind, record, now)
                archive = raw / (label + '.zip')
                transport(endpoint + '/zip', archive, LIMITS[kind])
                if (archive.is_symlink() or not archive.is_file() or archive.stat().st_size != record['size_in_bytes'] or
                        digest(archive) != pin['sha256']):
                    raise ValueError('download bytes differ from exact artifact pin')
                extract(archive, directory / kind, kind)
                artifacts.append(dict(target=target, kind=kind, artifactId=pin['artifactId'], sha256=pin['sha256']))
        write(staged / 'intake.json', dict(schemaVersion=1, kind='engine-release-artifact-intake',
            context='read-only-intake', qualified=False, release=False, readyForPublication=False,
            locator=locator, artifacts=artifacts, boundary='Byte intake only; no engine/source/licence or compiler acceptance granted.'))
        staged.rename(output)
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--locator', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.locator.stat().st_size > 1024**2:
        parser.error('locator exceeds bound')
    print(stage(json.loads(args.locator.read_text(), object_pairs_hook=unique), args.output.absolute()))
