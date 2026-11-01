"""Shared source-custody inventory for engine construction and assembly."""
import hashlib
from pathlib import Path
import re
import subprocess
import json
import runpy
import os
import stat

CONFIGURE_SCRIPTS = ('build-engine.py', 'engine_inputs.py', 'engine-recipe-adapter.py',
    'configure-ffmpeg-recipe.py', 'configure-ffmpeg-bzip2.py', 'ffmpeg-configure-checks.py', 'configure-windows-libdovi.py', 'configure-windows-rav1e.py',
    'patch-btbn-winarm64-system-error.sh', 'prepare-ffmpeg-source.py', 'archive-ffmpeg-source.py',
    'ffmpeg-build-tools.py', 'requirements-ffmpeg-meson.txt')


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def recipe_inventory(recipe):
    result = {}
    for path in sorted(recipe.rglob('*')):
        relative = path.relative_to(recipe)
        if {'.git', 'artifacts', 'ffbuild', 'workspace'} & set(relative.parts):
            continue
        if relative.parts[:2] == ('.cache', 'images'):
            continue
        if path.is_symlink() and (path.is_dir() or not path.resolve().is_relative_to(recipe.resolve())):
            raise ValueError('escaping/directory recipe symlink: ' + relative.as_posix())
        if path.is_file():
            result[relative.as_posix()] = digest(path)
        elif path.is_symlink():
            raise ValueError('broken recipe symlink: ' + relative.as_posix())
    return result


def json_digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def recipe_tree(recipe):
    result = {}
    for relative, sha in recipe_inventory(recipe).items():
        path = recipe / relative
        if path.is_symlink():
            result[relative] = {'kind': 'symlink', 'target': str(path.readlink()), 'mode': path.lstat().st_mode & 0o777}
        else:
            result[relative] = {'kind': 'file', 'sha256': sha, 'mode': path.stat().st_mode & 0o777}
    return result


def configure_scripts(root):
    return {'provenance/scripts/' + name: digest(root / 'scripts' / name) for name in CONFIGURE_SCRIPTS}


def consumed_dockerfile(recipe, expected_sha):
    path = recipe / 'Dockerfile'
    if not re.fullmatch('[a-f0-9]{64}', expected_sha) or path.is_symlink():
        raise ValueError('Dockerfile changed after build graph generation')
    data = path.read_bytes()
    if hashlib.sha256(data).hexdigest() != expected_sha:
        raise ValueError('Dockerfile changed after build graph generation')
    # Outside recipe/: freezing must not mutate the sealed recipe-tree input.
    # Docker receives only these verified bytes, never the mutable generator path.
    directory = recipe.parent / '.engine-inputs'
    if directory.is_symlink():
        raise ValueError('frozen Dockerfile directory must not be a symlink')
    directory.mkdir(mode=0o700, exist_ok=True)
    frozen = directory / ('Dockerfile.' + expected_sha)
    try:
        fd = os.open(frozen, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o444)
    except FileExistsError:
        pass
    else:
        try:
            with os.fdopen(fd, 'wb') as stream:
                stream.write(data)
                os.fchmod(stream.fileno(), 0o444)
        except BaseException:
            frozen.unlink()
            raise
    mode = frozen.lstat().st_mode
    if not stat.S_ISREG(mode) or stat.S_IMODE(mode) != 0o444 or digest(frozen) != expected_sha:
        raise ValueError('frozen Dockerfile changed or is not a read-only regular file')
    return frozen


def verify_compiler_sources(root):
    return runpy.run_path(str(Path(__file__).with_name('capture-engine-compiler.py')))['verify'](root)


def compiler_layout(layout, image_id):
    """Bind BuildKit's exported OCI context to the source-inventoried image."""
    def blob(entry):
        value = entry.get('digest', '')
        if not re.fullmatch('sha256:[a-f0-9]{64}', value):
            raise ValueError('invalid compiler OCI digest')
        path = layout / 'blobs/sha256' / value.removeprefix('sha256:')
        if (path.is_symlink() or not path.is_file() or not 0 < path.stat().st_size <= 2 * 1024**2 or
                path.stat().st_size != entry.get('size') or digest(path) != value.removeprefix('sha256:')):
            raise ValueError('compiler OCI metadata changed')
        return json.loads(path.read_bytes())
    index = json.loads((layout / 'index.json').read_bytes())
    entries = index.get('manifests', [])
    if index.get('schemaVersion') != 2 or len(entries) != 1:
        raise ValueError('compiler OCI context must contain one unambiguous image')
    manifest = blob(entries[0])
    if manifest.get('schemaVersion') != 2 or 'config' not in manifest:
        raise ValueError('compiler OCI context is not an image manifest')
    blob(manifest['config'])
    # Classic Docker identifies a loaded image by config; containerd's image
    # store uses the image manifest. Both must describe this exact OCI context.
    if image_id not in (entries[0]['digest'], manifest['config']['digest']):
        raise ValueError('compiler OCI context differs from the source-inventoried image')
    return {'manifestDigest': entries[0]['digest'], 'configDigest': manifest['config']['digest']}


def compiler_inputs(recipe, report):
    root = recipe / 'portico-compiler-inputs'
    binding = json.loads((recipe / 'portico-compiler-image.json').read_text())
    expected_sha = digest(root / 'compiler-inputs.json')
    if (binding.get('compilerImageId') != report.get('compilerImageId') or
            binding.get('sourceInventorySHA256') != expected_sha or
            report.get('compilerSourceInventorySHA256') != expected_sha):
        raise ValueError('compiler source custody differs from the consumed image')
    record = verify_compiler_sources(root)
    expected = {'windows-x64': 'x86_64-pc-windows-gnu', 'windows-arm64': 'aarch64-pc-windows-gnullvm'}
    if record.get('target') != expected.get(report.get('target')):
        raise ValueError('compiler runtime target differs from the engine target')
    return record


def source_pairs(recipe, require_files=True):
    pairs = {}
    for line in (recipe / 'Dockerfile').read_text().splitlines():
        script = re.search(r'--mount=src=(scripts\.d/[^,]+),dst=/stage.sh', line)
        archive = re.search(r'--mount=src=(\.cache/downloads/[^,]+),dst=/cache.tar.xz', line)
        if script and archive:
            name, filename = script.group(1), archive.group(1)
            if name in pairs and pairs[name] != filename:
                raise ValueError('dependency source changed between stages')
            path = recipe / filename
            if not path.resolve().is_relative_to(recipe.resolve()) or (require_files and not path.is_file()):
                raise ValueError('missing/escaping corresponding source: ' + filename)
            pairs[name] = filename
    if not pairs:
        raise ValueError('generated dependency source inventory is empty')
    return pairs


def checked_source(root):
    inputs = ['scripts', 'third_party/ffmpeg', 'third_party/portico-disc', 'LICENSE']
    changed = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=all', '--', *inputs],
                                      cwd=root, text=True)
    if changed:
        raise ValueError('commit engine source inputs before compiling: ' + changed.strip())
    return subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
