#!/usr/bin/env python3
"""Receipt-preserving OCI transport for development dependency/source caches."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
TYPE = 'application/vnd.portico.development-cache.v1'
RECEIPT = TYPE + '.receipt+json'
PAYLOAD = TYPE + '.payload.v1.tar'
LIMIT = 40 * 1024**3
TARGETS = ('windows-x64', 'linux-arm64', 'linux-x64', 'macos-arm64')


def module(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / (name + '.py'))
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value); return value


def encoded(value): return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def sha(value): return hashlib.sha256(encoded(value)).hexdigest()


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()


def guard(write=False, fixture=False):
    env = os.environ
    if (env.get('PORTICO_ENGINE_BUILD_CONTEXT') != 'development' or env.get('GITHUB_ACTIONS') != 'true' or
            env.get('RUNNER_ENVIRONMENT') != 'github-hosted' or env.get('GITHUB_REPOSITORY') != 'PorticoMediaServer/portico' or
            env.get('GITHUB_EVENT_NAME') != 'workflow_dispatch' or not env.get('GITHUB_REF', '').startswith('refs/heads/') or
            env.get('GITHUB_WORKFLOW_REF') != 'PorticoMediaServer/portico/.github/workflows/ffmpeg-toolchain.yml@' + env.get('GITHUB_REF', '')):
        raise ValueError('OCI caches require the dedicated hosted development workflow')
    if write and not fixture and env['GITHUB_REF'] != 'refs/heads/main':
        raise ValueError('production cache payload writes are main-only')


def identity(target, kind, directory):
    if target not in TARGETS or kind not in ('stages', 'sources'): raise ValueError('unknown cache scope')
    if kind == 'sources':
        if target not in ('windows-x64', 'linux-arm64'): raise ValueError('source snapshots are BtbN target-specific')
        namespace = 'engine-downloads-v2-' + target
        return dict(target=target, kind=kind, namespace=namespace,
                    inputKey=namespace + '-' + digest(ROOT / 'third_party/ffmpeg/sources.lock.json'))
    value = json.loads((directory / 'transport-context.json').read_text())
    if (value.get('schemaVersion') != 2 or value.get('target') != target or value.get('qualified') is not False or value.get('release') is not False or
            not re.fullmatch('engine-dependencies-v2-' + re.escape(target) + '-[a-f0-9]{64}-', value.get('namespace', '')) or
            not re.fullmatch(re.escape(value['namespace']) + '[a-f0-9]{64}', value.get('inputKey', ''))):
        raise ValueError('exact development compiler/dependency cache context required')
    return {name: value[name] for name in ('target', 'namespace', 'inputKey')} | dict(kind=kind)


def roots(directory, kind):
    if directory.is_symlink(): raise ValueError('linked cache root')
    if kind == 'sources':
        receipt = directory / 'receipt.json'
        if not receipt.exists(): return []
        if receipt.is_symlink(): raise ValueError('linked source receipt')
        entries = json.loads(receipt.read_text())
        api = module('engine-download-cache')
        if not isinstance(entries, dict) or len(entries) > 1000: raise ValueError('bounded source inventory required')
        result = [receipt]
        for name, expected in entries.items():
            path = directory / name
            if (not api.NAME.fullmatch(name) or path.is_symlink() or not path.is_file() or
                    not re.fullmatch('[a-f0-9]{64}', str(expected)) or digest(path) != expected):
                raise ValueError('source snapshot bytes/name changed')
            result.append(path)
        if sum(p.stat().st_size for p in result) > api.LIMIT: raise ValueError('source snapshot size bound')
        return result if entries else []
    result = []
    for path in sorted(directory.glob('*/stages/*')):
        if not re.fullmatch('[a-f0-9]{64}', path.name): continue  # Never capture incomplete .stage-*.
        if path.is_symlink() or not path.is_dir(): raise ValueError('linked or invalid stage checkpoint')
        if not (path / 'index.json').is_file(): continue
        if path.parent.parent.name not in ('macos', 'linux-native', 'linux-dovi'):
            raise ValueError('unknown stage checkpoint family')
        result.append(path)
    return result


def inventory(directory, kind):
    selected = roots(directory, kind); values = {}
    for root in selected:
        paths = [root] + sorted(root.rglob('*')) if root.is_dir() else [root]
        for path in paths:
            if path.is_symlink(): raise ValueError('linked cache checkpoint entry')
            for parent in path.parents:
                if parent == directory: break
                if parent.is_symlink(): raise ValueError('checkpoint traverses a link')
            info = path.stat(); name = path.relative_to(directory).as_posix()
            if stat.S_ISDIR(info.st_mode): continue
            if not stat.S_ISREG(info.st_mode): raise ValueError('special cache checkpoint entry')
            values[name] = dict(sha256=digest(path), bytes=info.st_size, mode=stat.S_IMODE(info.st_mode))
    if len(values) > 500000 or sum(v['bytes'] for v in values.values()) > LIMIT:
        raise ValueError('cache checkpoint inventory bound')
    if kind == 'stages':
        api = module('engine-prefix-cache')
        for root in selected:
            if json.loads((root / 'oci-layout').read_text()) != {'imageLayoutVersion': '1.0.0'}: raise ValueError('stage OCI layout changed')
            index = json.loads((root / 'index.json').read_text())
            if index.get('schemaVersion') != 2 or len(index.get('manifests', [])) != 1: raise ValueError('stage OCI index changed')
            manifest = json.loads(api.checked_blob(root, index['manifests'][0]).read_text())
            if len(manifest.get('layers', [])) != 1: raise ValueError('one exact stage delta required')
            receipt = json.loads(api.checked_blob(root, manifest['config']).read_text())
            api.checked_blob(root, manifest['layers'][0])
            if receipt.get('key') != root.name or receipt.get('qualified') is not False or receipt.get('release') is not False:
                raise ValueError('qualified/release receipt cannot enter development transport')
    return values


def pack(directory, scope, output):
    files = inventory(directory, scope['kind'])
    if not files: return None
    total = sum(v['bytes'] for v in files.values())
    if shutil.disk_usage(output).free < total + 1024**3: raise ValueError('cache capture needs disk headroom')
    payload = output / 'payload.tar'
    with tarfile.open(payload, 'w', format=tarfile.PAX_FORMAT) as archive:
        for name, entry in sorted(files.items()):
            info = tarfile.TarInfo(name); info.size = entry['bytes']; info.mode = entry['mode']; info.mtime = 0
            with (directory / name).open('rb') as stream: archive.addfile(info, stream)
    if inventory(directory, scope['kind']) != files: raise ValueError('cache changed during capture')
    payload.chmod(0o444)
    receipt = dict(schemaVersion=1, **scope, snapshot=sha(files), files=files,
                   payloadSHA256=digest(payload), payloadBytes=payload.stat().st_size,
                   qualified=False, release=False, readyForPublication=False)
    (output / 'receipt.json').write_bytes(encoded(receipt))
    return receipt


def unpack(transport, directory, scope, exact_input=False):
    receipt = json.loads((transport / 'receipt.json').read_text()); payload = transport / 'payload.tar'
    if (receipt.get('schemaVersion') != 1 or receipt.get('qualified') is not False or receipt.get('release') is not False or
            receipt.get('readyForPublication') is not False or
            any(receipt.get(k) != scope[k] for k in ('target', 'kind', 'namespace')) or
            (exact_input and receipt.get('inputKey') != scope['inputKey']) or
            receipt.get('payloadBytes') != payload.stat().st_size or payload.stat().st_size > LIMIT + 1024**3 or
            digest(payload) != receipt.get('payloadSHA256')):
        raise ValueError('cache transport scope/payload changed')
    files = receipt.get('files')
    if not isinstance(files, dict) or len(files) > 500000 or sha(files) != receipt.get('snapshot'):
        raise ValueError('cache transport inventory changed')
    directory.mkdir(parents=True, exist_ok=True)
    if directory.is_symlink() or set(p.name for p in directory.iterdir()) - {'transport-context.json'}:
        raise ValueError('cache restore requires a fresh owned destination')
    with tempfile.TemporaryDirectory(prefix='.oci-restore-', dir=directory.parent) as temporary:
        expanded = Path(temporary) / 'expanded'; expanded.mkdir()
        with tarfile.open(payload, 'r:') as archive:
            seen = set(); total = 0
            for item in archive:
                path = PurePosixPath(item.name); entry = files.get(item.name)
                if (not entry or item.name in seen or path.is_absolute() or '..' in path.parts or '\\' in item.name or
                        not item.isfile() or item.size != entry.get('bytes') or item.mode != entry.get('mode') or item.mode & ~0o777):
                    raise ValueError('unsafe/changed cache transport member')
                seen.add(item.name); total += item.size
                if total > LIMIT: raise ValueError('cache expansion size bound')
                destination = expanded.joinpath(*path.parts); destination.parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(item) as source, destination.open('xb') as output: shutil.copyfileobj(source, output, 1024**2)
                destination.chmod(item.mode)
                if digest(destination) != entry.get('sha256'): raise ValueError('cache member SHA-256 changed')
            if seen != set(files): raise ValueError('cache transport inventory incomplete')
        if inventory(expanded, scope['kind']) != files: raise ValueError('restored cache receipt differs')
        current = directory / 'transport-context.json'
        if current.exists(): shutil.copyfile(current, expanded / current.name)
        backup = Path(temporary) / 'previous'
        directory.rename(backup)
        try: expanded.rename(directory)
        except BaseException:
            backup.rename(directory); raise
    return receipt


class Transport:
    def __init__(self, target, fixture=False):
        self.fixture = fixture; self.target = target
        if target not in TARGETS: raise ValueError('unknown cache target')
        guard(fixture=fixture)
        base = 'ffmpeg-payload-cache-control-' if fixture else 'ffmpeg-payload-cache-'
        self.repository = 'ghcr.io/porticomediaserver/' + base + target
        self.tools = Path(os.environ['RUNNER_TEMP']) / 'engine-cache-tools'

    def oras(self, args, cwd=None, missing=False):
        command = [str(self.tools / 'oras'), *args, '--registry-config', str(self.tools / 'auth.json')]
        result = subprocess.run(command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if result.returncode:
            if missing and any(x in result.stderr.lower() for x in (b'manifest unknown', b'manifest_unknown', b'name unknown', b'name_unknown', b'not found')): return None
            raise RuntimeError('ORAS failed: ' + result.stderr.decode(errors='replace')[-1500:])
        return result.stdout

    def manifest(self, ref, missing=False):
        if not ref.startswith((self.repository + ':', self.repository + '@')):
            raise ValueError('cache reference escaped the target repository')
        raw = self.oras(['manifest', 'fetch', '--descriptor', ref], missing=missing)
        if raw is None: return None
        descriptor = json.loads(raw); digest_value = descriptor.get('digest', '')
        if not re.fullmatch('sha256:[a-f0-9]{64}', digest_value) or not 0 < descriptor.get('size', 0) < 1024**2:
            raise ValueError('cache OCI descriptor changed')
        exact = self.repository + '@' + digest_value
        with tempfile.TemporaryDirectory(prefix='manifest-', dir=self.tools) as temporary:
            output = Path(temporary) / 'manifest.json'
            self.oras(['manifest', 'fetch', exact, '--output', str(output)])
            data = output.read_bytes()
        if hashlib.sha256(data).hexdigest() != digest_value[7:] or len(data) != descriptor['size']:
            raise ValueError('cache OCI manifest changed')
        manifest = json.loads(data)
        if manifest.get('schemaVersion') != 2 or manifest.get('artifactType') != TYPE or len(manifest.get('layers', [])) != 2:
            raise ValueError('unexpected cache OCI artifact')
        config = manifest.get('config', {})
        if (manifest.get('subject') is not None or config.get('size') != 2 or
                config.get('digest') != 'sha256:' + hashlib.sha256(b'{}').hexdigest() or
                config.get('annotations') or config.get('urls')):
            raise ValueError('cache OCI config/subject changed')
        layers = {layer.get('annotations', {}).get('org.opencontainers.image.title'): layer for layer in manifest['layers']}
        if set(layers) != {'receipt.json', 'payload.tar'}: raise ValueError('cache OCI file names changed')
        for name, media, maximum in (('receipt.json', RECEIPT, 64 * 1024**2), ('payload.tar', PAYLOAD, LIMIT + 1024**3)):
            layer = layers[name]
            if (layer.get('mediaType') != media or
                    layer.get('annotations') != {'org.opencontainers.image.title': name} or layer.get('urls') or
                    not 0 < layer.get('size', 0) <= maximum or not re.fullmatch('sha256:[a-f0-9]{64}', layer.get('digest', ''))):
                raise ValueError('cache OCI layer inventory changed')
        return exact, manifest

    def aliases(self, scope):
        return [scope['kind'] + '-input-' + sha(scope['inputKey']), scope['kind'] + '-scope-' + sha(scope['namespace'])]

    def pull(self, exact, directory, scope, work, exact_input=False):
        manifest = self.manifest(exact)[1]
        if shutil.disk_usage(work).free < 2 * sum(x['size'] for x in manifest['layers']) + 1024**3:
            raise ValueError('cache restore needs disk headroom')
        with tempfile.TemporaryDirectory(prefix='pull-', dir=work) as temporary:
            temp = Path(temporary)
            self.oras(['pull', exact, '--output', str(temp)])
            for layer in manifest['layers']:
                path = temp / layer['annotations']['org.opencontainers.image.title']
                if path.is_symlink() or not path.is_file() or path.stat().st_size != layer['size'] or digest(path) != layer['digest'][7:]:
                    raise ValueError('downloaded OCI cache blob changed')
            if {p.name for p in temp.iterdir()} != {'receipt.json', 'payload.tar'}: raise ValueError('unexpected OCI files')
            return unpack(temp, directory, scope, exact_input=exact_input)

    def restore(self, directory, scope, work):
        for index, tag in enumerate(self.aliases(scope)):
            resolved = self.manifest(self.repository + ':' + tag, missing=True)
            if resolved:
                receipt = self.pull(resolved[0], directory, scope, work, exact_input=index == 0)
                return dict(hit=True, reference=resolved[0], snapshot=receipt['snapshot'], inputKeyMatched=receipt['inputKey'] == scope['inputKey'])
        return dict(hit=False, snapshot='empty')

    def save(self, directory, scope, work, before=None):
        guard(write=True, fixture=self.fixture)
        files = inventory(directory, scope['kind'])
        if not files or sha(files) == before: return dict(saved=False, snapshot=sha(files) if files else 'empty')
        with tempfile.TemporaryDirectory(prefix='push-', dir=work) as temporary:
            temp = Path(temporary); receipt = pack(directory, scope, temp)
            tag = scope['kind'] + '-content-' + sha(dict(scope=scope, snapshot=receipt['snapshot']))
            resolved = self.manifest(self.repository + ':' + tag, missing=True)
            if resolved is None:
                self.oras(['push', '--artifact-type', TYPE, self.repository + ':' + tag,
                           '--annotation', 'org.opencontainers.image.source=https://github.com/PorticoMediaServer/portico',
                           'receipt.json:' + RECEIPT, 'payload.tar:' + PAYLOAD], cwd=temp)
                resolved = self.manifest(self.repository + ':' + tag)
            # A successful round-trip, not an upload exit code, permits migration cleanup.
            verification = temp / 'verified'
            restored = self.pull(resolved[0], verification, scope, temp, exact_input=True)
            if restored['files'] != receipt['files']: raise ValueError('immutable cache content tag differs')
            self.oras(['tag', resolved[0], *self.aliases(scope)])
            return dict(saved=True, reference=resolved[0], snapshot=receipt['snapshot'], payloadBytes=receipt['payloadBytes'], roundTripVerified=True)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=('restore', 'save', 'fingerprint'))
    p.add_argument('--target', choices=TARGETS, required=True); p.add_argument('--kind', choices=('stages', 'sources'), required=True)
    p.add_argument('--directory', type=Path, required=True); p.add_argument('--work', type=Path, required=True)
    p.add_argument('--before'); p.add_argument('--output', type=Path)
    a = p.parse_args(); guard(); a.work.mkdir(parents=True, exist_ok=True)
    scope = identity(a.target, a.kind, a.directory)
    if a.action == 'fingerprint': value = dict(snapshot=sha(inventory(a.directory, a.kind)))
    elif a.action == 'restore': value = Transport(a.target).restore(a.directory, scope, a.work)
    else: value = Transport(a.target).save(a.directory, scope, a.work, a.before)
    value.update(target=a.target, kind=a.kind, qualified=False, release=False)
    (a.work / (a.kind + '-' + a.action + '.json')).write_text(json.dumps(value, indent=2) + '\n')
    if a.output:
        with a.output.open('a') as output:
            for key in ('hit', 'saved', 'snapshot'):
                if key in value: output.write(key + '=' + str(value[key]).lower() + '\n')
    print(json.dumps(value))


if __name__ == '__main__': main()
