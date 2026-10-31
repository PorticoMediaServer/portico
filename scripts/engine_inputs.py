"""Shared source-custody inventory for engine construction and assembly."""
import hashlib
from pathlib import Path
import re
import subprocess
import json

CONFIGURE_SCRIPTS = ('build-engine.py', 'engine_inputs.py', 'engine-recipe-adapter.py',
    'configure-ffmpeg-recipe.py', 'configure-windows-libdovi.py',
    'patch-btbn-winarm64-system-error.sh', 'prepare-ffmpeg-source.py', 'archive-ffmpeg-source.py')


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
    if path.is_symlink() or digest(path) != expected_sha:
        raise ValueError('Dockerfile changed after build graph generation')
    return path


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
