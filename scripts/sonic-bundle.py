#!/usr/bin/env python3
"""Build, verify and fetch the pinned Linux Sonic distribution (no installation)."""
import argparse
import ctypes
import errno
import hashlib
import io
import json
import lzma
import math
import os
import platform
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import struct
import subprocess
import tarfile
import tempfile
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
MAX_ARCHIVE = 128 << 20
MAX_CONTENT = 256 << 20
MAX_MODEL = 64 << 20
# These remain the distribution's ordinary OS runtime, as in the FFmpeg bundle.
SYSTEM = {'libc.so.6', 'libm.so.6', 'libdl.so.2', 'libpthread.so.0', 'librt.so.1',
          'libstdc++.so.6', 'libgcc_s.so.1', 'ld-linux-x86-64.so.2', 'ld-linux-aarch64.so.1'}
TARGETS = {'linux-x64': 62, 'linux-arm64': 183}


def locks():
    return (json.loads((ROOT / 'third_party/portico-sonic/runtime.lock.json').read_text()),
            json.loads((ROOT / 'third_party/musicnn/model.lock.json').read_text()))


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            h.update(block)
    return h.hexdigest()


def safe_path(value):
    path = PurePosixPath(value)
    if not value or path.is_absolute() or '..' in path.parts or str(path) != value or '\\' in value:
        raise ValueError('invalid bundle member path')
    return path


def elf(path, target):
    with path.open('rb') as stream:
        header = stream.read(64)
    if (len(header) != 64 or header[:6] != b'\x7fELF\x02\x01'
            or struct.unpack_from('<H', header, 18)[0] != TARGETS[target]):
        raise ValueError('wrong helper/library ELF target')


def model_identity(directory, lock):
    descriptor = json.loads((directory / 'model.json').read_text())
    expected = {'id': lock['modelId'], 'version': lock['onnx']['version'], 'licence': 'ISC',
                'sourceCommit': lock['sourceCommit'], 'format': 'onnx',
                'sha256': lock['onnx']['sha256'], 'bytes': lock['onnx']['bytes'],
                'preprocessing': 'musicnn-logmel-1', 'input': 'log_mel',
                'embedding': 'embedding', 'tags': 'tags', 'frames': 187, 'mels': 96,
                'dimensions': 200, 'tagCount': 50}
    for key, value in expected.items():
        if type(descriptor.get(key)) is not type(value) or descriptor[key] != value:
            raise ValueError('model descriptor identity differs from approved model')
    for name in ('model.onnx', 'model.json', 'LICENSE.md'):
        file = directory / name
        if file.is_symlink() or not file.is_file():
            raise ValueError('model member is not a regular file')
    if ((directory / 'model.onnx').stat().st_size != expected['bytes']
            or digest(directory / 'model.onnx') != expected['sha256']
            or digest(directory / 'LICENSE.md') != lock['licenseSHA256']):
        raise ValueError('model or original ISC licence bytes differ')
    dependencies = descriptor.get('dependencies')
    approved = {'tensorflow-cpu': '2.15.1', 'tf2onnx': '1.16.1', 'onnx': '1.16.2',
                'onnxruntime': '1.20.1', 'numpy': '1.26.4'}
    if dependencies != approved:
        raise ValueError('unapproved model converter provenance')
    return descriptor


def needed(file):
    result = subprocess.run(['readelf', '-d', str(file)], check=True, capture_output=True, text=True)
    return re.findall(r'\(NEEDED\).*?\[([^\]]+)\]', result.stdout)


def package(helper, sdk, model, target, output, source_revision):
    runtime, model_lock = locks()
    if not re.fullmatch(r'[0-9a-f]{40}', source_revision):
        raise ValueError('full source revision required')
    if output.exists():
        raise ValueError('refusing to replace a published bundle')
    if helper.is_symlink() or not helper.is_file():
        raise ValueError('helper must be a regular file')
    elf(helper, target)
    identity = model_identity(model, model_lock)
    sdk_lib = (sdk / 'lib').resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix='sonic-package-', dir=output.parent) as temporary:
        stage = Path(temporary) / 'bundle'
        stage.mkdir()
        shutil.copy2(helper, stage / 'portico-sonic')
        (stage / 'portico-sonic').chmod(0o755)
        external, visited = set(), set()
        pending = needed(helper)
        while pending:
            name = pending.pop()
            if name in visited:
                continue
            visited.add(name)
            if name in SYSTEM:
                external.add(name)
                continue
            if not re.fullmatch(r'libonnxruntime\.so(?:\.[0-9]+)*', name):
                raise ValueError('unapproved non-system runtime dependency')
            file = (sdk_lib / name).resolve(strict=True)
            if file.parent != sdk_lib or not file.is_file():
                raise ValueError('runtime dependency escapes verified SDK')
            elf(file, target)
            lib = stage / 'lib'
            lib.mkdir(exist_ok=True)
            shutil.copy2(file, lib / file.name)
            (lib / file.name).chmod(0o755)
            # Keep only aliases of a library actually in the NEEDED closure.
            for alias in sorted(sdk_lib.iterdir()):
                if alias.is_symlink() and alias.resolve(strict=True) == file:
                    if not re.fullmatch(r'libonnxruntime\.so(?:\.[0-9]+)*', alias.name):
                        raise ValueError('unexpected SDK library alias')
                    (lib / alias.name).symlink_to(file.name)
            pending.extend(needed(file))
        if not (stage / 'lib').is_dir():
            raise ValueError('helper does not use the verified ONNX runtime')
        shutil.copytree(model, stage / 'musicnn-mtt', ignore=shutil.ignore_patterns('*conformance*'))
        if {p.name for p in (stage / 'musicnn-mtt').iterdir()} != {'model.onnx', 'model.json', 'LICENSE.md'}:
            raise ValueError('unexpected model package member')
        legal = stage / 'licenses/sonic'
        legal.mkdir(parents=True)
        for source, name in [(sdk / 'LICENSE', 'onnxruntime-LICENSE'),
                             (sdk / 'ThirdPartyNotices.txt', 'onnxruntime-ThirdPartyNotices.txt'),
                             (ROOT / 'LICENSE', 'portico-GPLv3'),
                             (ROOT / 'THIRD-PARTY-NOTICES.md', 'THIRD-PARTY-NOTICES.md')]:
            shutil.copyfile(source, legal / name)
        if b'Permission is hereby granted, free of charge' not in (legal / 'onnxruntime-LICENSE').read_bytes():
            raise ValueError('original ONNX MIT licence missing')
        sources = ['third_party/portico-sonic/main.cpp', 'third_party/portico-sonic/CMakeLists.txt',
                   'third_party/portico-sonic/README.md', 'third_party/portico-sonic/runtime.lock.json',
                   'scripts/prepare-sonic-runtime.py', 'scripts/build-sonic-runtime.sh']
        for name in sources:
            dest = stage / 'source/portico-sonic' / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / name, dest)
        entries = []
        for file in sorted(stage.rglob('*')):
            if file.is_symlink():
                entries.append({'path': file.relative_to(stage).as_posix(), 'link': os.readlink(file)})
            elif file.is_file():
                # tarfile's safe data filter strips group/other write bits.
                # Seal portable modes before hashing, independent of umask.
                executable = file.name == 'portico-sonic' or file.parent.name == 'lib' or file.name == 'build-sonic-runtime.sh'
                file.chmod(0o755 if executable else 0o644)
                entries.append({'path': file.relative_to(stage).as_posix(), 'bytes': file.stat().st_size,
                                'sha256': digest(file), 'mode': stat.S_IMODE(file.stat().st_mode)})
        manifest = {'schema': 1, 'target': target, 'releaseTag': runtime['releaseTag'],
                    'sourceRevision': source_revision, 'runtimeVersion': runtime['version'],
                    'model': {k: identity[k] for k in ('id', 'version', 'sha256', 'bytes')},
                    'externalSystemLibraries': sorted(external), 'files': entries}
        (stage / 'sonic-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        verify(stage, target)
        epoch = int(os.environ.get('SOURCE_DATE_EPOCH', '0'))
        with tarfile.open(output, 'w:xz') as archive:
            for file in sorted(stage.rglob('*')):
                info = archive.gettarinfo(str(file), file.relative_to(stage).as_posix())
                info.uid = info.gid = 0
                info.uname = info.gname = ''
                info.mtime = epoch
                if info.isfile():
                    with file.open('rb') as stream:
                        archive.addfile(info, stream)
                else:
                    archive.addfile(info)


def verify(directory, target, additional_files=()):
    runtime, model_lock = locks()
    manifest_file = directory / 'sonic-manifest.json'
    if manifest_file.is_symlink() or manifest_file.stat().st_size > 1 << 20:
        raise ValueError('invalid Sonic manifest')
    manifest = json.loads(manifest_file.read_text())
    if (manifest.get('schema') != 1 or manifest.get('target') != target
            or manifest.get('releaseTag') != runtime['releaseTag']
            or manifest.get('runtimeVersion') != runtime['version']
            or not re.fullmatch(r'[0-9a-f]{40}', manifest.get('sourceRevision', ''))
            or not set(manifest.get('externalSystemLibraries', [])) <= SYSTEM):
        raise ValueError('wrong Sonic bundle identity')
    paths = set()
    for entry in manifest['files']:
        relative = safe_path(entry['path'])
        if str(relative) in paths:
            raise ValueError('duplicate manifest member')
        paths.add(str(relative))
        file = directory / relative
        # Only local library aliases are permitted; all parents are real dirs.
        if any((directory / PurePosixPath(*relative.parts[:i])).is_symlink() for i in range(1, len(relative.parts))):
            raise ValueError('symlinked bundle parent')
        if 'link' in entry:
            link = entry['link']
            if (relative.parent != PurePosixPath('lib') or not re.fullmatch(r'libonnxruntime\.so(?:\.[0-9]+)*', link)
                    or not file.is_symlink() or os.readlink(file) != link or not file.resolve().is_file()
                    or file.resolve().parent != (directory / 'lib').resolve()):
                raise ValueError('invalid runtime library alias')
        elif (file.is_symlink() or not file.is_file() or file.stat().st_size != entry['bytes']
              or digest(file) != entry['sha256'] or stat.S_IMODE(file.stat().st_mode) != entry['mode']):
            raise ValueError('bundle member changed')
    actual = {p.relative_to(directory).as_posix() for p in directory.rglob('*') if p.is_file() or p.is_symlink()}
    if paths | {'sonic-manifest.json'} | set(additional_files) != actual:
        raise ValueError('unlisted or missing bundle members')
    identity = model_identity(directory / 'musicnn-mtt', model_lock)
    if manifest['model'] != {k: identity[k] for k in ('id', 'version', 'sha256', 'bytes')}:
        raise ValueError('model manifest mismatch')
    elf(directory / 'portico-sonic', target)
    if not os.access(directory / 'portico-sonic', os.X_OK):
        raise ValueError('helper not executable')
    libraries = list((directory / 'lib').glob('*'))
    if not libraries:
        raise ValueError('runtime closure missing')
    for file in libraries:
        if not file.is_symlink():
            elf(file, target)
    for name in ('onnxruntime-LICENSE', 'onnxruntime-ThirdPartyNotices.txt', 'portico-GPLv3', 'THIRD-PARTY-NOTICES.md'):
        if not (directory / 'licenses/sonic' / name).is_file():
            raise ValueError('required legal notice missing')
    return manifest


def verify_installed(ffmpeg, target):
    """Verify both sealed inventories; never edit/extend the FFmpeg manifest.

    Sonic lives beside bin/ffmpeg. A regular hardlink projection lets the unchanged
    standalone FFmpeg verifier check its original inventory, while the final
    exact union below rejects any undeclared composite-package member.
    """
    original = json.loads((ffmpeg / 'toolchain-manifest.json').read_text())
    if original.get('target') != target:
        raise ValueError('Sonic and FFmpeg targets differ')
    names = set(original['files']) | {'toolchain-manifest.json'}
    extra_bin = []
    for name in names:
        safe_path(name)
        if name.startswith('bin/'):
            extra_bin.append(name[4:])
    manifest = verify(ffmpeg / 'bin', target, extra_bin)
    sonic_names = {'bin/' + entry['path'] for entry in manifest['files']} | {'bin/sonic-manifest.json'}
    if names & sonic_names:
        raise ValueError('Sonic collides with sealed FFmpeg members')
    actual = {p.relative_to(ffmpeg).as_posix() for p in ffmpeg.rglob('*') if p.is_file() or p.is_symlink()}
    if actual != names | sonic_names:
        raise ValueError('composite toolchain inventory mismatch')
    with tempfile.TemporaryDirectory(prefix='ffmpeg-inventory-', dir=ffmpeg.parent) as temporary:
        projection = Path(temporary)
        for name in names:
            source = ffmpeg / name
            relative = safe_path(name)
            if (source.is_symlink() or not source.is_file()
                    or any((ffmpeg / PurePosixPath(*relative.parts[:i])).is_symlink() for i in range(1, len(relative.parts)))):
                raise ValueError('FFmpeg member missing or symlinked')
            dest = projection / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            os.link(source, dest, follow_symlinks=False)
        subprocess.run(['python3', str(ROOT / 'scripts/verify-ffmpeg-manifest.py'), str(projection)], check=True,
                       stdout=subprocess.DEVNULL)
    return manifest


def install(bundle, ffmpeg, target):
    manifest = verify(bundle, target)
    if json.loads((ffmpeg / 'toolchain-manifest.json').read_text()).get('target') != target:
        raise ValueError('Sonic and FFmpeg targets differ')
    subprocess.run(['python3', str(ROOT / 'scripts/verify-ffmpeg-manifest.py'), str(ffmpeg)], check=True,
                   stdout=subprocess.DEVNULL)
    files = [entry['path'] for entry in manifest['files']] + ['sonic-manifest.json']
    for name in files:
        dest = ffmpeg / 'bin' / name
        if dest.exists() or dest.is_symlink():
            raise ValueError('Sonic installation would replace an existing member')
    for name in files:
        source, dest = bundle / name, ffmpeg / 'bin' / name
        dest.parent.mkdir(parents=True, exist_ok=True)
        if source.is_symlink():
            dest.symlink_to(os.readlink(source))
        else:
            shutil.copy2(source, dest)
    verify_installed(ffmpeg, target)


def extract(archive, output, target):
    if output.exists() or output.is_symlink():
        raise ValueError('output already exists')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='sonic-extract-', dir=output.parent) as temporary:
        stage = Path(temporary) / 'bundle'
        stage.mkdir()
        with tarfile.open(archive) as source:
            members = source.getmembers()
            if len(members) > 512 or sum(m.size for m in members) > MAX_CONTENT:
                raise ValueError('unbounded Sonic archive')
            seen = set()
            for member in members:
                path = safe_path(member.name)
                if member.name in seen or not (member.isfile() or member.isdir() or member.issym()):
                    raise ValueError('duplicate or unsupported Sonic archive member')
                seen.add(member.name)
                if member.issym() and (path.parent != PurePosixPath('lib') or not re.fullmatch(r'libonnxruntime\.so(?:\.[0-9]+)*', member.linkname)):
                    raise ValueError('escaping Sonic archive alias')
            source.extractall(stage, filter='data')
        verify(stage, target)
        if output.exists() or output.is_symlink():
            raise ValueError('output appeared during verification')
        stage.rename(output)


def fetch(output, target):
    runtime, _ = locks()
    tag = runtime['releaseTag']
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,100}', tag):
        raise ValueError('invalid release tag')
    name = f'portico-sonic-{tag}-{target}.tar.xz'
    base = 'https://github.com/PorticoMediaServer/portico-server/releases/download/' + tag + '/'
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='sonic-fetch-', dir=output.parent) as temporary:
        with urllib.request.urlopen(base + 'checksums.sha256', timeout=60) as response:
            sums = response.read((64 << 10) + 1).decode('ascii')
        if len(sums) > 64 << 10:
            raise ValueError('unbounded checksum list')
        matches = [line.split()[0] for line in sums.splitlines()
                   if len(line.split()) == 2 and line.split()[1].removeprefix('./') == name]
        if len(matches) != 1 or not re.fullmatch('[0-9a-f]{64}', matches[0]):
            raise ValueError('missing unique bundle checksum')
        archive = Path(temporary) / name
        size = 0
        with urllib.request.urlopen(base + name, timeout=120) as response, archive.open('wb') as stream:
            while block := response.read(1 << 20):
                size += len(block)
                if size > MAX_ARCHIVE:
                    raise ValueError('unbounded Sonic download')
                stream.write(block)
        if digest(archive) != matches[0]:
            raise ValueError('Sonic download checksum mismatch')
        extract(archive, output, target)


def model_release(model_lock):
    tag = model_lock.get('releaseTag')
    if not isinstance(tag, str) or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,100}', tag):
        raise ValueError('invalid model release tag')
    return tag


def model_name(model_lock):
    version = model_lock['onnx']['version']
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,100}', version):
        raise ValueError('invalid model version')
    return 'musicnn-mtt-' + version + '.tar.xz'


MODEL_REFERENCE = 'original-tensorflow-checkpoint'
MODEL_API = 'https://api.github.com/repos/PorticoMediaServer/portico-server/releases/tags/'


def model_reference(model_lock, path):
    """The exact original-checkpoint reference check-sonic-bundle-conformance.py needs."""
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 64 << 10:
        raise ValueError('invalid model conformance reference')
    reference = json.loads(path.read_text())
    if not isinstance(reference, dict):
        raise ValueError('invalid model conformance reference')
    cases = reference.get('cases')
    if (reference.get('modelSHA256') != model_lock['onnx']['sha256']
            or reference.get('reference') != MODEL_REFERENCE
            or not isinstance(cases, list) or len(cases) != 3):
        raise ValueError('conformance reference is not bound to the original checkpoint and this model')
    for case in cases:
        if not isinstance(case, list) or len(case) != 250:
            raise ValueError('invalid original model outputs')
        if not all(type(value) is float and math.isfinite(value) for value in case):
            raise ValueError('invalid original model outputs')
    return reference


def model_package(output, model, conformance):
    """Publish the exact reviewed model trio once, with its reference and pin."""
    _, model_lock = locks()
    model_identity(model, model_lock)
    model_reference(model_lock, conformance)
    members = [(f'musicnn-mtt/{name}', model / name, 0o644) for name in ('model.onnx', 'model.json', 'LICENSE.md')]
    members += [('conformance.json', conformance, 0o644), ('model.lock.json', None, 0o644)]
    epoch = int(os.environ.get('SOURCE_DATE_EPOCH', '0'))
    output.parent.mkdir(parents=True, exist_ok=True)
    try:
        handle = output.open('xb')
    except FileExistsError:
        raise ValueError('refusing to replace a published model export') from None
    except OSError:
        raise ValueError('could not create the published model export') from None
    try:
        with handle:
            with tarfile.open(fileobj=handle, mode='w:xz', format=tarfile.USTAR_FORMAT) as archive:
                for name, source, mode in members:
                    if source is None:
                        stream = io.BytesIO(json.dumps(model_lock, indent=2).encode() + b'\n')
                        size = stream.getbuffer().nbytes
                    else:
                        stream, size = source.open('rb'), source.stat().st_size
                    info = tarfile.TarInfo(name)
                    info.size, info.mode, info.mtime = size, mode, epoch
                    info.uid = info.gid = 0
                    info.uname = info.gname = ''
                    with stream:
                        archive.addfile(info, stream)
    except BaseException:
        output.unlink(missing_ok=True)
        raise


MAX_MODEL_ARCHIVE = 32 << 20
MAX_MODEL_MEMORY = 64 << 20


class ModelStream:
    """An xz decode with explicit memory, compressed-input and decompressed-output budgets.

    The decoder memory limit is set before any dictionary is allocated, every compressed read is
    capped, and running out of any budget is a failure rather than an end of file. One stream is
    accepted; bytes after it must be the zero padding the format allows.
    """

    def __init__(self, path, compressed=MAX_MODEL_ARCHIVE, memory=MAX_MODEL_MEMORY, output=MAX_MODEL + (1 << 20)):
        self.file = path.open('rb')
        self.decoder = lzma.LZMADecompressor(format=lzma.FORMAT_XZ, memlimit=memory)
        self.compressed_left = compressed
        self.left = output

    def _input(self):
        if self.compressed_left <= 0:
            raise ValueError('model export exceeds its compressed budget')
        block = self.file.read(min(1 << 16, self.compressed_left))
        if not block:
            raise ValueError('truncated model export archive')
        self.compressed_left -= len(block)
        return block

    def read(self, size=-1):
        if self.left <= 0:
            raise ValueError('model export exceeds its decompressed budget')
        want = self.left if size is None or size < 0 else min(size, self.left)
        out = bytearray()
        while len(out) < want and not self.decoder.eof:
            data = self._input() if self.decoder.needs_input else b''
            chunk = self.decoder.decompress(data, max_length=want - len(out))
            out += chunk
            if not chunk and not self.decoder.eof and not self.decoder.needs_input:
                raise ValueError('model export archive did not make progress')
        self.left -= len(out)
        return bytes(out)

    def finish(self):
        """Require a clean decoder end and only zero padding after it.

        The decoder can already hold bytes that followed the stream in the same compressed
        chunk, so those are inspected first. Every probe after that consumes the same
        compressed budget as decoding, so unbounded zero padding cannot slip past it.
        """
        if not self.decoder.eof:
            raise ValueError('model export archive did not end cleanly')
        # Zero padding is legal stream padding whether the decoder buffered it in the same
        # chunk or it arrives from later file reads; only nonzero data or a second stream is
        # refused. These bytes were already charged to the compressed budget by _input.
        if any(self.decoder.unused_data):
            raise ValueError('unexpected data after the model export stream')
        while True:
            block = self.file.read(min(1 << 16, max(1, self.compressed_left)))
            if not block:
                return
            if len(block) > self.compressed_left:
                raise ValueError('model export exceeds its compressed budget')
            self.compressed_left -= len(block)
            if any(block):
                raise ValueError('unexpected data after the model export stream')

    def close(self):
        self.file.close()


def model_header(header):
    """Validate one ustar header. Extension and special member types are refused, never parsed."""
    if header[257:263] != b'ustar\0' or header[263:265] != b'00':
        raise ValueError('unsupported model export container')
    if any(header[345:500]):
        raise ValueError('unsupported model export member prefix')
    raw_kind = header[156:157]
    if raw_kind not in (b'0', b'\0'):
        raise ValueError('unsupported model export member type')
    stored = header[148:156].split(b'\0', 1)[0].strip()
    if not stored.isdigit():
        raise ValueError('invalid model export header checksum')
    unsigned = sum(header[:148]) + 32 * 8 + sum(header[156:])
    if unsigned != int(stored, 8):
        raise ValueError('model export header checksum mismatch')
    name = header[0:100].split(b'\0', 1)[0].decode('utf-8', 'strict')
    raw = header[124:136].split(b'\0', 1)[0].strip() or b'0'
    if not raw.isdigit():
        raise ValueError('unsupported model export member size')
    return name, int(raw, 8)


def model_extract(archive, output):
    """Verify and atomically publish one pinned model export; never install or infer."""
    _, model_lock = locks()
    expected = {f'musicnn-mtt/{name}' for name in ('model.onnx', 'model.json', 'LICENSE.md')}
    expected |= {'conformance.json', 'model.lock.json'}
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='sonic-model-', dir=output.parent) as temporary:
        stage = Path(temporary) / 'export'
        stage.mkdir()
        stream = ModelStream(archive)
        try:
            seen = set()
            total = 0
            trailer = 0
            while True:
                header = stream.read(512)
                if len(header) != 512:
                    raise ValueError('model export is missing its end-of-archive trailer')
                if header == bytes(512):
                    trailer += 1
                    if trailer == 2:
                        break
                    continue
                if trailer:
                    raise ValueError('unexpected data after the model export')
                name, size = model_header(header)
                safe_path(name)
                if name not in expected or name in seen:
                    raise ValueError('unexpected or duplicate model export member')
                seen.add(name)
                total += size
                if total > MAX_MODEL:
                    raise ValueError('unbounded model export archive')
                destination = stage / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                with destination.open('wb') as copied:
                    remaining = size
                    while remaining:
                        block = stream.read(min(remaining, 1 << 20))
                        if not block:
                            raise ValueError('truncated model export archive')
                        copied.write(block)
                        remaining -= len(block)
                padding = (-size) % 512
                if padding and len(stream.read(padding)) != padding:
                    raise ValueError('truncated model export archive')
            while True:
                tail = stream.read(1 << 20)
                if not tail:
                    break
                if any(tail):
                    raise ValueError('unexpected data after the model export')
            stream.finish()
        finally:
            stream.close()
        if seen != expected:
            raise ValueError('unexpected model export members')
        model_identity(stage / 'musicnn-mtt', model_lock)
        if json.loads((stage / 'model.lock.json').read_text()) != model_lock:
            raise ValueError('model export provenance differs from the reviewed pin')
        model_reference(model_lock, stage / 'conformance.json')
        model_publish(stage, output)


def model_publish(stage, output):
    """Atomically publish the verified directory, never replacing an existing one.

    Linux renameat2 with RENAME_NOREPLACE renames the verified same-parent stage in one step, so
    the destination either does not exist or is the complete export. Where that primitive is
    unavailable the export is refused rather than published with a weaker guarantee.
    """
    at_fdcwd = -100
    rename_noreplace = 1
    old, new = os.fsencode(str(stage)), os.fsencode(str(output))
    libc = ctypes.CDLL(None, use_errno=True)
    renameat2 = getattr(libc, 'renameat2', None)
    if renameat2 is not None:
        renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
        renameat2.restype = ctypes.c_int
        result = renameat2(at_fdcwd, old, at_fdcwd, new, rename_noreplace)
    else:
        numbers = {'x86_64': 316, 'aarch64': 276}
        number = numbers.get(platform.machine())
        if number is None:
            raise ValueError('no-replace publication unavailable on this architecture')
        syscall = libc.syscall
        syscall.restype = ctypes.c_long
        result = syscall(ctypes.c_long(number), ctypes.c_int(at_fdcwd), ctypes.c_char_p(old),
                         ctypes.c_int(at_fdcwd), ctypes.c_char_p(new), ctypes.c_uint(rename_noreplace))
    if result == 0:
        return
    error = ctypes.get_errno()
    if error == errno.EEXIST:
        raise ValueError('model export output already exists')
    raise ValueError('no-replace publication unavailable: ' + os.strerror(error))


def model_release_present(tag):
    """Whether the tag itself exists. A broken asset of an existing release is not absence."""
    request = urllib.request.Request(MODEL_API + tag, headers={
        'Accept': 'application/vnd.github+json', 'User-Agent': 'portico-sonic-toolchain'})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            payload = response.read((64 << 10) + 1)
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return False
        raise ValueError('could not confirm the model release tag') from None
    except OSError:
        raise ValueError('could not confirm the model release tag') from None
    if len(payload) > 64 << 10:
        raise ValueError('unbounded model release answer')
    answer = json.loads(payload)
    if not isinstance(answer, dict) or answer.get('tag_name') != tag:
        raise ValueError('model release tag answer differs')
    return True


def model_fetch(output, base=None):
    """Fetch the durable reviewed model export from its own pinned, non-latest release."""
    _, model_lock = locks()
    tag, name = model_release(model_lock), model_name(model_lock)
    base = (base or 'https://github.com/PorticoMediaServer/portico-server/releases/download/' + tag).rstrip('/') + '/'
    if not model_release_present(tag):
        raise ValueError('model export release is absent')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='sonic-model-fetch-', dir=output.parent) as temporary:
        try:
            with urllib.request.urlopen(base + 'checksums.sha256', timeout=60) as response:
                sums = response.read((64 << 10) + 1).decode('ascii')
        except urllib.error.HTTPError:
            raise ValueError('model export release exists without a checksum list') from None
        if len(sums) > 64 << 10:
            raise ValueError('unbounded model checksum list')
        matches = [line.split()[0] for line in sums.splitlines()
                   if len(line.split()) == 2 and line.split()[1].removeprefix('./') == name]
        if len(matches) != 1 or not re.fullmatch('[0-9a-f]{64}', matches[0]):
            raise ValueError('missing unique model export checksum')
        archive = Path(temporary) / name
        size = 0
        try:
            with urllib.request.urlopen(base + name, timeout=120) as response, archive.open('wb') as stream:
                while block := response.read(1 << 20):
                    size += len(block)
                    if size > MAX_MODEL:
                        raise ValueError('unbounded model export download')
                    stream.write(block)
        except urllib.error.HTTPError:
            raise ValueError('model export release exists without its pinned archive') from None
        if digest(archive) != matches[0]:
            raise ValueError('model export download checksum mismatch')
        model_extract(archive, output)


def model_verify(directory):
    _, model_lock = locks()
    return model_identity(directory, model_lock)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    for name in ('verify', 'verify-installed', 'install', 'fetch', 'extract', 'package'):
        command = commands.add_parser(name)
        command.add_argument('output', type=Path)
        command.add_argument('--target', choices=TARGETS, required=True)
        if name == 'extract':
            command.add_argument('--archive', type=Path, required=True)
        if name == 'install':
            command.add_argument('--bundle', type=Path, required=True)
        if name == 'package':
            for key in ('helper', 'sdk', 'model'):
                command.add_argument('--' + key, type=Path, required=True)
            command.add_argument('--source-revision', required=True)
    for name in ('model-package', 'model-fetch', 'model-verify'):
        command = commands.add_parser(name)
        command.add_argument('output', type=Path)
        if name == 'model-package':
            command.add_argument('--model', type=Path, required=True)
            command.add_argument('--conformance', type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == 'model-package':
            model_package(args.output, args.model, args.conformance)
        elif args.command == 'model-fetch':
            model_fetch(args.output)
        elif args.command == 'model-verify':
            model_verify(args.output)
        elif args.command == 'package':
            args.output.parent.mkdir(parents=True, exist_ok=True)
            package(args.helper, args.sdk, args.model, args.target, args.output, args.source_revision)
        elif args.command == 'extract':
            extract(args.archive, args.output, args.target)
        elif args.command == 'fetch':
            fetch(args.output, args.target)
        elif args.command == 'install':
            install(args.bundle, args.output, args.target)
        elif args.command == 'verify-installed':
            verify_installed(args.output, args.target)
        else:
            verify(args.output, args.target)
    except ValueError as error:
        parser.exit(1, 'Sonic bundle verification failed: ' + str(error) + '\n')
    except (OSError, KeyError, tarfile.TarError, subprocess.CalledProcessError):
        parser.exit(1, 'Sonic bundle verification failed; no bundle published.\n')
    if args.command.startswith('model-'):
        print('Sonic model export ' + args.command.removeprefix('model-') + ' passed.')
    else:
        print('Sonic bundle ' + args.command + ' passed (' + args.target + ').')


if __name__ == '__main__':
    main()
