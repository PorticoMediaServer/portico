#!/usr/bin/env python3
"""Release-checkpoint recipe only. No network, installation or PATH discovery.

Inputs: clean pinned upstream checkout, exact musl source archive, qualified
builder/tool manifest, private output parent. Must not run per implementation item.
"""
import ctypes
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import subprocess
import sys
import tarfile

HERE = Path(__file__).resolve().parent
PIN = json.loads((HERE / 'inputs.json').read_text())
RECIPE_FILES = ('worker.c', 'CMakeLists.txt', 'build-policy.h', 'build.py',
                'inputs.json', 'DEPENDENCY.md', 'REBUILD.md', 'GPL-3.0.txt', 'LGPL-3.0.txt')

def overlaps(left, right):
    return left == right or left.is_relative_to(right) or right.is_relative_to(left)

def publish_delivery(parent, stage, final):
    # Linux RENAME_NOREPLACE is mandatory: ordinary rename can replace an empty
    # destination directory. No copy/replace fallback may expose a partial tree.
    libc = ctypes.CDLL(None, use_errno=True)
    rename = getattr(libc, 'renameat2', None)
    require(rename is not None, 'atomic no-replace delivery publication unavailable')
    rename.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
    rename.restype = ctypes.c_int
    for root, directories, files in os.walk(stage, topdown=False, followlinks=False):
        require(all(not (Path(root) / name).is_symlink() for name in directories + files), 'delivery symlink refused')
        for name in files:
            with (Path(root) / name).open('rb') as stream:
                os.fsync(stream.fileno())
        descriptor = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
    descriptor = os.open(parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        result = rename(descriptor, os.fsencode(stage.name), descriptor, os.fsencode(final.name), 1)
        if result != 0:
            failure = ctypes.get_errno()
            raise OSError(failure, os.strerror(failure))
        os.fsync(descriptor)
    finally:
        os.close(descriptor)

def digest(path, limit=1 << 30):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        remaining = limit
        while remaining:
            block = stream.read(min(65536, remaining))
            if not block:
                return result.hexdigest()
            result.update(block); remaining -= len(block)
        require(not stream.read(1), 'hash input exceeds bound')
    return result.hexdigest()


def require(condition, message):
    if not condition:
        raise SystemExit(message)

def qualified_helper(sandbox, output):
    # A binary path plus a reference to elsewhere is not a redistribution input.
    root = Path(sandbox['qualifiedDeliveryDirectory'])
    require(root.is_absolute() and root.resolve() == root and root.is_dir(), 'qualified helper delivery directory required')
    require(not overlaps(output, root), 'output overlaps qualified helper delivery')
    manifest_path = root / 'manifest.json'
    require(manifest_path.is_file() and not manifest_path.is_symlink() and manifest_path.stat().st_size <= 1 << 20, 'bounded helper manifest required')
    require(digest(manifest_path, 1 << 20) == sandbox['qualifiedManifestSHA256'], 'qualified helper manifest hash mismatch')
    raw = manifest_path.read_bytes(); manifest = json.loads(raw)
    require(set(manifest) == {'schema', 'identity', 'linkage', 'executable', 'files'}, 'closed helper distribution schema required')
    require(manifest['schema'] == 1 and isinstance(manifest['identity'], str) and 0 < len(manifest['identity']) <= 256, 'actual qualified helper identity required')
    require(manifest['linkage'] in ('static', 'dynamic'), 'helper linkage declaration required')
    files = manifest['files']
    require(isinstance(files, list) and 0 < len(files) <= 4096, 'bounded complete helper file list required')
    roles = {'executable', 'license', 'notice', 'source', 'build-provenance', 'qualification', 'runtime-dependency'}
    seen = set(); total = 0
    for entry in files:
        require(set(entry) == {'path', 'size', 'sha256', 'role'}, 'closed helper file declaration required')
        name = entry['path']
        require(isinstance(name, str) and 0 < len(name) <= 1024 and re.fullmatch(r'[A-Za-z0-9._/-]+', name) and not name.startswith('/') and all(part not in ('', '.', '..') for part in name.split('/')), 'unsafe helper distribution path')
        require(name not in seen and entry['role'] in roles, 'duplicate/unknown helper artifact')
        seen.add(name)
        require(type(entry['size']) is int and 0 < entry['size'] <= 512 << 20, 'helper artifact size refused')
        require(isinstance(entry['sha256'], str) and re.fullmatch(r'[0-9a-f]{64}', entry['sha256']), 'actual helper artifact hash required')
        total += entry['size']; require(total <= 512 << 20, 'helper distribution size refused')
        candidate = root / name
        require(candidate.resolve() == candidate and candidate.is_file() and candidate.stat().st_size == entry['size'], 'regular exact helper artifact required')
        require(digest(candidate, entry['size']) == entry['sha256'], 'helper artifact hash mismatch')
    for role in ('executable', 'build-provenance', 'qualification'):
        require(sum(entry['role'] == role for entry in files) == 1, 'one exact helper ' + role + ' required')
    require(any(entry['role'] == 'license' for entry in files) and any(entry['role'] == 'source' for entry in files), 'matching helper license and source delivery required')
    if manifest['linkage'] == 'dynamic':
        require(any(entry['role'] == 'runtime-dependency' for entry in files), 'qualified helper dynamic closure absent')
    executable = next(entry for entry in files if entry['role'] == 'executable')
    require(manifest['executable'] == {'path': executable['path'], 'sha256': executable['sha256']} and executable['sha256'] == sandbox['sha256'], 'helper executable does not match qualified closure')
    require((root / executable['path']).stat().st_mode & 0o111, 'qualified helper is not executable')
    provenance_file = next(entry for entry in files if entry['role'] == 'build-provenance')
    require(provenance_file['size'] <= 65536, 'helper build provenance exceeds bound')
    provenance = json.loads((root / provenance_file['path']).read_bytes())
    require(provenance.get('executableSHA256') == executable['sha256'] and provenance.get('sourceSHA256') == {entry['path']: entry['sha256'] for entry in files if entry['role'] == 'source'}, 'helper source/build provenance does not match executable')
    require(isinstance(provenance.get('builderIdentity'), str) and re.fullmatch(r'sha256:[0-9a-f]{64}', provenance['builderIdentity']), 'actual qualified helper builder identity required')
    return root, raw, manifest

def copy_helper_file(source, destination, entry):
    # Copy exactly the qualified inode's declared extent, never a growing stream.
    before = source.stat(); total = 0; hashed = hashlib.sha256()
    descriptor = os.open(source, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, 'rb') as original, destination.open('xb') as target:
        require(os.path.samestat(before, os.fstat(original.fileno())), 'helper input replaced')
        while total < entry['size']:
            block = original.read(min(65536, entry['size'] - total))
            require(block, 'short helper artifact')
            target.write(block); hashed.update(block); total += len(block)
        require(not original.read(1) and hashed.hexdigest() == entry['sha256'], 'helper artifact changed during copy')
    destination.chmod(0o755 if before.st_mode & 0o111 else 0o644)


def main():
    require(len(sys.argv) == 5, 'build.py LIBMOBI_CHECKOUT MUSL_ARCHIVE TOOLCHAIN_MANIFEST OUTPUT')
    source, archive, manifest_path, output = [Path(value).resolve() for value in sys.argv[1:]]
    require(platform.system() == 'Linux' and platform.machine() == 'x86_64', 'unsupported builder target')
    require(not output.exists(), 'output must be new; no overwrite or install')
    require(not overlaps(output, HERE) and not overlaps(output, source), 'output must not overlap recipe or upstream source trees')
    for name in RECIPE_FILES:
        path = HERE / name
        require(path.is_file() and not path.is_symlink() and path.stat().st_size <= 1 << 20, 'reviewed regular recipe input required: ' + name)
    tools_manifest = json.loads(manifest_path.read_text())
    helper_root, helper_manifest_raw, helper_manifest = qualified_helper(tools_manifest['sandbox'], output)
    require(re.fullmatch(r'sha256:[0-9a-f]{64}', tools_manifest['builderImage']) is not None, 'qualified immutable builder image digest required')
    tools = {}
    for name, version in PIN['toolchain'].items():
        entry = tools_manifest['tools'][name]
        path = Path(entry['path'])
        require(path.is_absolute() and path.is_file() and not path.is_symlink(), 'exact regular tool file required')
        require(digest(path) == entry['sha256'], 'tool hash mismatch: ' + name)
        require(entry['version'] == version, 'tool version mismatch: ' + name)
        tools[name] = str(path)
    # Git is an input-inspection tool in the qualified builder, not a downloader.
    git = Path(tools_manifest['git']['path'])
    require(git.is_absolute() and digest(git) == tools_manifest['git']['sha256'], 'git input hash mismatch')
    require(subprocess.check_output([str(git), '-C', str(source), 'rev-parse', 'HEAD'], text=True).strip() == PIN['libmobi']['commit'], 'wrong libmobi commit')
    subprocess.run([str(git), '-C', str(source), 'diff', '--exit-code', 'HEAD', '--'], check=True, stdout=subprocess.DEVNULL)
    require(not subprocess.check_output([str(git), '-C', str(source), 'ls-files', '--others', '--exclude-standard']), 'untracked upstream input refused')
    for name, expected in PIN['libmobi']['files'].items():
        require(digest(source / name) == expected, 'upstream file hash mismatch: ' + name)
    require(digest(archive) == PIN['musl']['sha256'], 'musl archive hash mismatch')
    output.mkdir(mode=0o700)
    work = output / 'work'; work.mkdir(mode=0o700)
    env = {'PATH': '/usr/bin:/bin', 'LC_ALL': 'C', 'TZ': 'UTC', 'SOURCE_DATE_EPOCH': '1711929600', 'ZERO_AR_DATE': '1'}
    # All shell/configure utilities come from the recorded immutable Release
    # builder, never fetched by this recipe. The compiler/linker/archiver are exact.
    def run(argv, cwd=None, extra=None):
        subprocess.run(argv, cwd=cwd, env=env | (extra or {}), check=True)
    with tarfile.open(archive) as package:
        require(all(member.name.startswith('musl-1.2.6/') for member in package), 'unexpected musl archive root')
        package.extractall(work, filter='data')
    musl_source = work / 'musl-1.2.6'; musl_build = work / 'musl-build'; musl_build.mkdir()
    musl_prefix = work / 'musl-prefix'
    compiler = shlex.join([tools['clang'], '--target=x86_64-linux-musl', '-fuse-ld=' + tools['ld.lld']])
    run([str(musl_source / 'configure'), '--target=x86_64-linux-musl', '--disable-shared', '--prefix=' + str(musl_prefix)], cwd=musl_build,
        extra={'CC': compiler, 'AR': tools['llvm-ar'], 'RANLIB': tools['llvm-ranlib']})
    run([tools['make'], '-j1'], cwd=musl_build)
    run([tools['make'], 'install'], cwd=musl_build) # private prefix only
    build = work / 'worker-build'
    run([tools['cmake'], '-S', str(HERE), '-B', str(build), '-G', 'Unix Makefiles',
         '-DCMAKE_MAKE_PROGRAM=' + tools['make'], '-DCMAKE_C_COMPILER=' + tools['clang'],
         '-DCMAKE_C_COMPILER_TARGET=x86_64-linux-musl', '-DCMAKE_TRY_COMPILE_TARGET_TYPE=STATIC_LIBRARY',
         '-DCMAKE_AR=' + tools['llvm-ar'], '-DCMAKE_RANLIB=' + tools['llvm-ranlib'],
         '-DLIBMOBI_SOURCE=' + str(source), '-DMUSL_PREFIX=' + str(musl_prefix), '-DPORTICO_LLD=' + tools['ld.lld']])
    run([tools['cmake'], '--build', str(build), '--target', 'portico-mobi-worker', '--parallel', '1'])
    worker = build / 'portico-mobi-worker'
    elf = subprocess.check_output([tools['llvm-readelf'], '--program-headers', '--dynamic-table', str(worker)], text=True)
    require('INTERP' not in elf and '(NEEDED)' not in elf and 'Shared library:' not in elf, 'worker is not a static closure')
    delivery = output / '.delivery-incomplete'; delivery.mkdir(mode=0o700)
    shutil.copy2(worker, delivery / 'worker')
    # Sandbox is an existing Release-qualified Linux helper, not built/downloaded here.
    sandbox = tools_manifest['sandbox']; sandbox_path = Path(sandbox['path'])
    require(sandbox_path.is_absolute() and not sandbox_path.is_symlink() and digest(sandbox_path) == sandbox['sha256'], 'sandbox hash mismatch')
    helper_delivery = delivery / 'helper-distribution'; helper_delivery.mkdir()
    for entry in helper_manifest['files']:
        destination = helper_delivery / entry['path']; destination.parent.mkdir(parents=True, exist_ok=True)
        copy_helper_file(helper_root / entry['path'], destination, entry)
    (delivery / 'helper-distribution-manifest.json').write_bytes(helper_manifest_raw)
    helper_executable = next(entry for entry in helper_manifest['files'] if entry['role'] == 'executable')
    copy_helper_file(helper_delivery / helper_executable['path'], delivery / 'bwrap', helper_executable)
    bundle = {'schema': 1, 'target': PIN['target'], 'converter': 'libmobi:' + PIN['libmobi']['commit'],
              'options': 'portico-epub-v1;unencrypted-only;no-replica;default-kf8',
              'workerSHA256': digest(delivery / 'worker'), 'sandboxSHA256': digest(delivery / 'bwrap')}
    # Matching source and all application objects allow independent rebuild/relink.
    sources = delivery / 'sources'; sources.mkdir()
    with (sources / 'libmobi.tar').open('wb') as stream:
        subprocess.run([str(git), '-C', str(source), 'archive', PIN['libmobi']['commit']], stdout=stream, check=True)
    shutil.copy2(archive, sources / 'musl-1.2.6.tar.gz')
    recipe = sources / 'portico-mobi'; recipe.mkdir()
    for name in RECIPE_FILES:
        shutil.copy2(HERE / name, recipe / name)
    objects = delivery / 'relink-objects'; objects.mkdir()
    for index, obj in enumerate(sorted(build.rglob('*.o'))):
        shutil.copy2(obj, objects / (str(index) + '-' + obj.name))
    for name in ('libc.a', 'crt1.o', 'crti.o', 'crtn.o'):
        shutil.copy2(musl_prefix / 'lib' / name, objects / name)
    shutil.copy2(manifest_path, delivery / 'builder-inputs.json')
    shutil.copy2(musl_source / 'COPYRIGHT', delivery / 'MUSL-COPYRIGHT')
    shutil.copy2(source / 'COPYING', delivery / 'LIBMOBI-COPYING')
    shutil.copy2(source / 'src/miniz.c', delivery / 'MINIZ-NOTICE-AND-SOURCE.c')
    (delivery / 'static-elf.txt').write_text(elf)
    # All required sources, notices, relink objects and provenance now exist.
    # Only now create the runtime descriptor, durably sync, and publish once.
    (delivery / 'manifest.json').write_text(json.dumps(bundle, indent=2) + '\n')
    (delivery / 'manifest.sha256').write_text(digest(delivery / 'manifest.json') + '\n')
    publish_delivery(output, delivery, output / 'delivery')
    # No version/help/parser process is run by qualification in this recipe.
    # Release independently qualifies the sandbox/worker at its checkpoint.

if __name__ == '__main__':
    main()
