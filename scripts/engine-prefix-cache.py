#!/usr/bin/env python3
"""Content-addressed OCI dependency deltas. Cache receipts never qualify an engine."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import tarfile
import tempfile

SCHEMA = 1
LIMIT = 16 * 1024**3


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def encoded(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def identity(value):
    return hashlib.sha256(encoded(value)).hexdigest()


def atomic(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    pending = path.with_suffix('.new')
    pending.write_bytes(encoded(value) + b'\n'); pending.replace(path)


def excluded(name):
    # FFmpeg itself always compiles fresh. Its prepared source stays checked by
    # the ordinary producer, outside dependency cache identity and transport.
    parts = PurePosixPath(name).parts
    return (parts[0] == 'packages' and len(parts) > 1 and
            (parts[1].startswith('portico-ffmpeg-source-') or parts[1] == 'ffmpeg.done'))


def inventory(roots):
    result = {}
    canonical = {name: path.resolve() for name, path in roots.items()}
    if any(not re.fullmatch('[a-z][a-z0-9-]*', name) for name in roots):
        raise ValueError('fixed dependency cache root names required')
    for name, root in roots.items():
        if root.is_symlink():
            raise ValueError('dependency cache root cannot be a link')
        if not root.exists():
            continue
        for path in [root, *sorted(root.rglob('*'))]:
            relative = path.relative_to(root).as_posix()
            key = name if relative == '.' else name + '/' + relative
            if excluded(key):
                continue
            mode = path.lstat().st_mode
            if stat.S_ISREG(mode):
                result[key] = dict(kind='file', bytes=path.stat().st_size, mode=stat.S_IMODE(mode), sha256=digest(path))
            elif stat.S_ISDIR(mode):
                result[key] = dict(kind='directory', mode=stat.S_IMODE(mode))
            elif stat.S_ISLNK(mode):
                destination = path.resolve(strict=True)
                target = next((label + '/' + destination.relative_to(base).as_posix()
                               for label, base in canonical.items() if destination.is_relative_to(base)), None)
                if target is None or not (destination.is_file() or destination.is_dir()):
                    raise ValueError('dependency link escapes cached roots: ' + key)
                # Normalize aliases such as /var -> /private/var before relpath.
                link = os.path.relpath(destination, path.parent.resolve())
                result[key] = dict(kind='symlink', target=link, cacheTarget=target)
            else:
                raise ValueError('special dependency output: ' + key)
            if len(result) > 500000:
                raise ValueError('dependency cache file bound exceeded')
    return result


def descriptor(path, media):
    return dict(mediaType=media, digest='sha256:' + digest(path), size=path.stat().st_size)


def checked_blob(layout, entry):
    value = entry.get('digest', '')
    if not re.fullmatch('sha256:[a-f0-9]{64}', value):
        raise ValueError('invalid dependency OCI blob identity')
    path = layout / 'blobs/sha256' / value[7:]
    if path.is_symlink() or not path.is_file() or path.stat().st_size != entry.get('size') or digest(path) != value[7:]:
        raise ValueError('dependency OCI blob changed')
    return path


class PrefixCache:
    def __init__(self, directory, state):
        self.directory, self.state = Path(directory), Path(state)
        if self.directory.is_symlink() or self.state.is_symlink():
            raise ValueError('owned dependency cache/state directories required')
        (self.directory / 'stages').mkdir(parents=True, exist_ok=True)
        self.state.mkdir(parents=True, exist_ok=True)

    def begin(self, inputs, roots):
        roots = {name: Path(path) for name, path in roots.items()}
        before = inventory(roots)
        contract = dict(schemaVersion=SCHEMA, inputs=inputs,
                        rootPaths={name: str(path.resolve()) for name, path in roots.items()},
                        beforeSHA256=identity(before), cacheImplementationSHA256=digest(Path(__file__)))
        key = identity(contract)
        layout = self.directory / 'stages' / key
        if layout.exists() or layout.is_symlink():
            receipt = self.restore(layout, key, contract, roots)
            atomic(self.state / 'last.json', dict(key=key, hit=True, receipt=receipt))
            return dict(key=key, hit=True, receipt=receipt)
        atomic(self.state / 'active.json', dict(key=key, contract=contract, before=before))
        return dict(key=key, hit=False)

    def finish(self, inputs, roots):
        roots = {name: Path(path) for name, path in roots.items()}
        active = json.loads((self.state / 'active.json').read_text())
        if (active['contract']['inputs'] != inputs or active['contract']['rootPaths'] !=
                {name: str(path.resolve()) for name, path in roots.items()}):
            raise ValueError('dependency inputs changed during stage')
        after = inventory(roots)
        changed = {name: value for name, value in after.items() if active['before'].get(name) != value}
        removed = sorted(set(active['before']) - set(after), reverse=True)
        total = sum(value.get('bytes', 0) for value in changed.values())
        if total > LIMIT or shutil.disk_usage(self.directory).free < total + 1024**3:
            raise ValueError('dependency cache size/free-space bound exceeded')
        receipt = dict(schemaVersion=SCHEMA, key=active['key'], contract=active['contract'],
                       outputs=changed, removed=removed, afterSHA256=identity(after),
                       qualified=False, release=False)
        pending = Path(tempfile.mkdtemp(prefix='.stage-', dir=self.directory))
        try:
            blobs = pending / 'blobs/sha256'; blobs.mkdir(parents=True)
            layer = pending / 'layer.tar.gz'
            with layer.open('wb') as raw, gzip.GzipFile(fileobj=raw, mode='wb', compresslevel=1, mtime=0) as compressed:
                with tarfile.open(fileobj=compressed, mode='w|') as output:
                    for name, value in sorted(changed.items()):
                        info = tarfile.TarInfo(name); info.uid = info.gid = info.mtime = 0
                        info.mode = value.get('mode', 0o777)
                        if value['kind'] == 'directory':
                            info.type = tarfile.DIRTYPE; output.addfile(info)
                        elif value['kind'] == 'symlink':
                            info.type = tarfile.SYMTYPE
                            info.linkname = os.path.relpath(value['cacheTarget'], str(PurePosixPath(name).parent))
                            output.addfile(info)
                        else:
                            root, *parts = PurePosixPath(name).parts
                            path = roots[root].joinpath(*parts); info.size = value['bytes']
                            with path.open('rb') as stream: output.addfile(info, stream)
            if inventory(roots) != after:
                raise ValueError('dependency outputs changed during cache capture')
            layer_entry = descriptor(layer, 'application/vnd.oci.image.layer.v1.tar+gzip')
            layer.rename(blobs / layer_entry['digest'][7:])
            config = pending / 'receipt.json'; config.write_bytes(encoded(receipt))
            config_entry = descriptor(config, 'application/vnd.portico.engine-stage.v1+json')
            config.rename(blobs / config_entry['digest'][7:])
            manifest = pending / 'manifest.json'
            manifest.write_bytes(encoded(dict(schemaVersion=2, mediaType='application/vnd.oci.image.manifest.v1+json',
                                              config=config_entry, layers=[layer_entry])))
            manifest_entry = descriptor(manifest, 'application/vnd.oci.image.manifest.v1+json')
            manifest.rename(blobs / manifest_entry['digest'][7:])
            atomic(pending / 'oci-layout', {'imageLayoutVersion': '1.0.0'})
            atomic(pending / 'index.json', dict(schemaVersion=2, manifests=[manifest_entry]))
            destination = self.directory / 'stages' / active['key']
            if destination.exists():
                raise ValueError('immutable dependency cache key already exists')
            pending.rename(destination)
        finally:
            if pending.exists(): shutil.rmtree(pending)
        (self.state / 'active.json').unlink()
        atomic(self.state / 'last.json', dict(key=active['key'], hit=False, receipt=receipt))
        return receipt

    @staticmethod
    def safe_parent(path, root):
        for parent in path.parents:
            if parent == root.parent: break
            if parent.is_symlink(): raise ValueError('dependency output traverses a directory alias')

    def restore(self, layout, key, contract, roots):
        if layout.is_symlink() or json.loads((layout / 'oci-layout').read_text()) != {'imageLayoutVersion': '1.0.0'}:
            raise ValueError('invalid dependency OCI layout')
        index = json.loads((layout / 'index.json').read_text())
        if index.get('schemaVersion') != 2 or len(index.get('manifests', [])) != 1:
            raise ValueError('one exact dependency OCI manifest required')
        manifest = json.loads(checked_blob(layout, index['manifests'][0]).read_text())
        if len(manifest.get('layers', [])) != 1:
            raise ValueError('one dependency delta layer required')
        receipt = json.loads(checked_blob(layout, manifest['config']).read_text())
        if (receipt.get('key') != key or receipt.get('contract') != contract or
                receipt.get('qualified') is not False or receipt.get('release') is not False):
            raise ValueError('dependency receipt differs from current exact inputs')
        layer = checked_blob(layout, manifest['layers'][0])
        with tempfile.TemporaryDirectory(prefix='.restore-', dir=self.state) as temporary:
            expanded = Path(temporary)
            with tarfile.open(layer, 'r:gz') as source:
                members = source.getmembers()
                if len(members) != len(receipt['outputs']) or sum(x.size for x in members) > LIMIT:
                    raise ValueError('dependency delta inventory/size changed')
                for member in members:
                    name = member.name
                    if (name not in receipt['outputs'] or PurePosixPath(name).parts[0] not in roots or
                            '..' in PurePosixPath(name).parts or name.startswith('/') or member.islnk() or
                            not (member.isfile() or member.isdir() or member.issym())):
                        raise ValueError('unexpected dependency delta entry')
                source.extractall(expanded, filter='data')
            for name, value in receipt['outputs'].items():
                path = expanded / name
                if value['kind'] == 'file':
                    if type(value['mode']) is not int or value['mode'] & ~0o777:
                        raise ValueError('unsafe cached file mode')
                    if not path.is_symlink() and path.is_file(): path.chmod(value['mode'])
                    if path.is_symlink() or not path.is_file() or digest(path) != value['sha256'] or path.stat().st_size != value['bytes'] or stat.S_IMODE(path.stat().st_mode) != value['mode']:
                        raise ValueError('restored dependency bytes/mode changed')
                elif value['kind'] == 'symlink':
                    expected = os.path.relpath(value['cacheTarget'], str(PurePosixPath(name).parent))
                    if not path.is_symlink() or os.readlink(path) != expected:
                        raise ValueError('restored dependency link changed')
                elif not path.is_dir() or path.is_symlink():
                    raise ValueError('restored dependency directory changed')
            for name in receipt['removed']:
                parts = PurePosixPath(name).parts
                if not parts or parts[0] not in roots or '..' in parts or name.startswith('/'):
                    raise ValueError('unsafe removed dependency output')
                path = roots[parts[0]].joinpath(*parts[1:])
                self.safe_parent(path, roots[parts[0]])
                if path.is_symlink() or path.is_file(): path.unlink()
                elif path.is_dir(): path.rmdir()
            for name, value in sorted(receipt['outputs'].items(), key=lambda item: (item[1]['kind'] != 'directory', item[0])):
                parts = PurePosixPath(name).parts
                path = roots[parts[0]].joinpath(*parts[1:])
                self.safe_parent(path, roots[parts[0]])
                if value['kind'] == 'directory':
                    if path.is_symlink(): path.unlink()
                    path.mkdir(parents=True, exist_ok=True); path.chmod(value['mode']); continue
                path.parent.mkdir(parents=True, exist_ok=True)
                if path.is_symlink() or path.is_file(): path.unlink()
                if value['kind'] == 'symlink': path.symlink_to(value['target'])
                else: shutil.copy2(expanded / name, path)
            if identity(inventory(roots)) != receipt['afterSHA256']:
                raise ValueError('dependency prefix/source receipt does not match restored output')
        return receipt
