#!/usr/bin/env python3
"""Pack and audit only the selected native Mac Vulkan runtime closure."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import subprocess

LIBRARIES = ('libvulkan.1.dylib', 'libvulkan.dylib', 'libMoltenVK.dylib')
TOOLS = ('ffmpeg', 'ffprobe', 'portico-disc')
LOADS = {'LC_LOAD_DYLIB', 'LC_LOAD_WEAK_DYLIB', 'LC_REEXPORT_DYLIB',
         'LC_LAZY_LOAD_DYLIB', 'LC_LOAD_UPWARD_DYLIB'}
ICD = Path('share/vulkan/icd.d/MoltenVK_icd.json')


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def run(command):
    result = subprocess.run([str(x) for x in command], check=True, capture_output=True, text=True, timeout=30)
    if len(result.stdout) > 4 * 1024**2:
        raise ValueError('Mach-O metadata exceeds its bound')
    return result.stdout


def machine(target):
    architecture = {'macos-arm64': 'arm64', 'macos-x64': 'x86_64'}[target]
    if platform.system() != 'Darwin' or platform.machine() != architecture:
        raise ValueError('Matching native Mac runner required')
    return architecture


def metadata(path, architecture):
    if run(['/usr/bin/lipo', '-archs', path]).strip().split() != [architecture]:
        raise ValueError('Wrong or mixed Mach-O architecture: ' + str(path))
    return parse_commands(run(['/usr/bin/otool', '-l', path]))


def copy_runtime(source, destination, architecture):
    architectures = run(['/usr/bin/lipo', '-archs', source]).strip().split()
    before = digest(source)
    if architectures == [architecture]:
        shutil.copyfile(source, destination)
    elif (source.name == 'libMoltenVK.dylib' and len(architectures) == 2 and
          set(architectures) == {'arm64', 'x86_64'} and architecture in architectures):
        # The pinned generic macOS MoltenVK target produces both slices.
        # Keep only this engine's target; the final verifier still accepts
        # exactly one native architecture for every executable and library.
        run(['/usr/bin/lipo', source, '-thin', architecture, '-output', destination])
    else:
        raise ValueError('Wrong or mixed Mach-O architecture: ' + str(source))
    metadata(destination, architecture)
    if digest(source) != before:
        raise ValueError('Selected runtime source changed during copy/thinning')
    return {'architectures': architectures, 'selectedArchitecture': architecture,
            'selectedSliceSHA256': digest(destination)}


def parse_commands(text):
    result = {'loads': [], 'rpaths': [], 'ids': []}
    for block in re.split(r'(?m)^Load command \d+\n', text)[1:]:
        match = re.search(r'(?m)^\s*cmd (LC_\w+)\s*$', block)
        if not match:
            raise ValueError('Unrecognized Mach-O load command')
        kind = match[1]
        if kind == 'LC_LOAD_DYLINKER':
            if not re.search(r'(?m)^\s*name /usr/lib/dyld \(offset \d+\)\s*$', block):
                raise ValueError('Unexpected Mach-O dynamic linker')
        if kind == 'LC_DYLD_ENVIRONMENT':
            raise ValueError('Mach-O embedded loader environment is forbidden')
        if kind in LOADS | {'LC_ID_DYLIB', 'LC_RPATH'}:
            field = 'path' if kind == 'LC_RPATH' else 'name'
            entry = re.search(r'(?m)^\s*' + field + r' (.+) \(offset \d+\)\s*$', block)
            if not entry:
                raise ValueError('Missing Mach-O path: ' + kind)
            result['rpaths' if kind == 'LC_RPATH' else 'ids' if kind == 'LC_ID_DYLIB' else 'loads'].append(entry[1])
    return result


def system(path):
    return ('..' not in PurePosixPath(path).parts and
            path.startswith(('/usr/lib/', '/System/Library/Frameworks/', '/System/Library/PrivateFrameworks/')))


def contained(path, root):
    actual = path.resolve(strict=True)
    if not actual.is_relative_to(root.resolve(strict=True)) or not actual.is_file():
        raise ValueError('Runtime input escaped its owned prefix: ' + str(path))
    return actual


def relative_load(binary, dependency):
    return '@loader_path/' + os.path.relpath(dependency, binary.parent).replace(os.sep, '/')


def owned_path(value, binary, bundle):
    bundle = bundle.resolve(strict=True)
    binary = binary.parent.resolve(strict=True) / binary.name
    if not value.startswith('@loader_path/'):
        raise ValueError('Non-relative non-system Mach-O dependency: ' + value)
    path = binary.parent / value.removeprefix('@loader_path/')
    actual = contained(path, bundle)
    if path.is_symlink() or actual.parent != bundle / 'lib' or actual.name not in LIBRARIES:
        raise ValueError('Unselected Mach-O runtime dependency: ' + value)
    if value != relative_load(binary, actual):
        raise ValueError('Non-canonical runtime dependency path: ' + value)
    return actual


def verify(bundle, target):
    bundle = bundle.resolve(strict=True)
    architecture = machine(target)
    expected = [bundle / 'bin' / name for name in TOOLS] + [bundle / 'lib' / name for name in LIBRARIES]
    actual = sorted(p.name for p in (bundle / 'lib').iterdir())
    if actual != sorted(LIBRARIES):
        raise ValueError('Unexpected or missing Mac runtime library')
    records = {}
    for path in expected:
        if path.is_symlink():
            raise ValueError('Runtime files must be regular owned bytes')
        info = metadata(path, architecture)
        expected_ids = ['@loader_path/' + path.name] if path.parent.name == 'lib' else []
        if info['ids'] != expected_ids:
            raise ValueError('Unexpected Mach-O install identity: ' + str(path))
        expected_rpath = '@loader_path' if path.parent.name == 'lib' else '@loader_path/../lib'
        if info['rpaths'] != [expected_rpath]:
            raise ValueError('Unexpected Mach-O runtime search path: ' + str(path))
        for dependency in info['loads']:
            if not system(dependency):
                owned_path(dependency, path, bundle)
        records[path.relative_to(bundle).as_posix()] = {'sha256': digest(path), **info}
    icd = bundle / ICD
    if icd.is_symlink():
        raise ValueError('ICD must be a regular owned file')
    document = json.loads(icd.read_text())
    if document['ICD']['library_path'] != '../../../lib/libMoltenVK.dylib':
        raise ValueError('ICD library path is not the selected relative runtime')
    if contained(icd.parent / document['ICD']['library_path'], bundle) != bundle / 'lib/libMoltenVK.dylib':
        raise ValueError('ICD escaped the runtime bundle')
    return {'target': target, 'executablesAndLibraries': records, 'icdSHA256': digest(icd),
            'boundary': 'Static native Mach-O/ICD closure; no physical Vulkan device or app signing acceptance'}


def pack(prefix, bundle, target):
    prefix, bundle = prefix.resolve(strict=True), bundle.resolve(strict=True)
    architecture = machine(target)
    library_dir = bundle / 'lib'; library_dir.mkdir()
    inputs, aliases = {}, {}
    for name in LIBRARIES:
        path = prefix / 'lib' / name
        source = contained(path, prefix / 'lib')
        if name.startswith('libvulkan') and not re.fullmatch(r'libvulkan(?:\.\d+)*\.dylib', source.name):
            raise ValueError('Vulkan alias resolved to an unselected library')
        if name == 'libMoltenVK.dylib' and source.name != name:
            raise ValueError('MoltenVK alias resolved to an unselected library')
        inputs[name] = {'path': str(source), 'sha256': digest(source)}
        aliases[name] = name
        aliases[source.name] = 'libvulkan.1.dylib' if name.startswith('libvulkan') else name
        inputs[name].update(copy_runtime(source, library_dir / name, architecture))
        if digest(source) != inputs[name]['sha256']:
            raise ValueError('Selected runtime source changed while packing')
    if inputs['libvulkan.1.dylib']['sha256'] != inputs['libvulkan.dylib']['sha256']:
        raise ValueError('Vulkan loader aliases have different bytes')
    originals = {}
    binaries = [bundle / 'bin' / name for name in TOOLS] + [library_dir / name for name in LIBRARIES]
    for binary in binaries:
        if binary.is_symlink():
            raise ValueError('Cannot rewrite a linked runtime input')
        info = metadata(binary, architecture)
        originals[binary.relative_to(bundle).as_posix()] = {'sha256': digest(binary), **info}
        command = ['/usr/bin/install_name_tool']
        for value in info['loads']:
            if system(value):
                continue
            name = PurePosixPath(value).name
            if name not in aliases:
                raise ValueError('Unselected non-system Mach-O load: ' + value)
            if value.startswith('/'):
                source = contained(Path(value), prefix / 'lib')
                if digest(source) != inputs[aliases[name]]['sha256']:
                    raise ValueError('Mach-O load does not name the selected bytes')
            elif value.startswith('@rpath/'):
                if value != '@rpath/' + name:
                    raise ValueError('Nested or escaping original rpath dependency')
            elif value.startswith(('@loader_path/', '@executable_path/')):
                original_parent = prefix / ('bin' if binary.parent.name == 'bin' or value.startswith('@executable_path/') else 'lib')
                source = contained(original_parent / value.split('/', 1)[1], prefix)
                if digest(source) != inputs[aliases[name]]['sha256']:
                    raise ValueError('Relative Mach-O load does not name selected bytes')
            else:
                raise ValueError('Unrecognized non-system Mach-O load: ' + value)
            command += ['-change', value, relative_load(binary, library_dir / aliases[name])]
        for value in info['rpaths']:
            if value.startswith('/'):
                if not Path(value).resolve(strict=True).is_relative_to(prefix):
                    raise ValueError('External original Mach-O runtime search path: ' + value)
            elif value not in ('@loader_path', '@loader_path/../lib', '@executable_path/../lib'):
                raise ValueError('Unrecognized original Mach-O runtime search path: ' + value)
            command += ['-delete_rpath', value]
        command += ['-add_rpath', '@loader_path' if binary.parent == library_dir else '@loader_path/../lib']
        if binary.parent == library_dir:
            if len(info['ids']) != 1:
                raise ValueError('Exactly one Mach-O library identity required')
            command += ['-id', '@loader_path/' + binary.name]
        elif info['ids']:
            raise ValueError('Executable unexpectedly has a dylib identity')
        run(command + [binary])
    original_icd = contained(prefix / ICD, prefix)
    document = json.loads(original_icd.read_text())
    if PurePosixPath(document['ICD']['library_path']).name != 'libMoltenVK.dylib':
        raise ValueError('Unselected Vulkan ICD driver')
    document['ICD']['library_path'] = '../../../lib/libMoltenVK.dylib'
    destination = bundle / ICD; destination.parent.mkdir(parents=True)
    destination.write_text(json.dumps(document, indent=2) + '\n')
    record = {'runtimeInputs': inputs, 'originalLoadCommands': originals,
              'originalICDSHA256': digest(original_icd), 'rewritten': verify(bundle, target),
              'signingOrder': 'Rewrite before app signing/notarisation; this stage uses no signing identity or credentials.'}
    (bundle / 'macos-runtime-build.json').write_text(json.dumps(record, indent=2) + '\n')
    return record


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('pack', 'verify'))
    parser.add_argument('--bundle', type=Path, required=True)
    parser.add_argument('--target', choices=('macos-arm64', 'macos-x64'), required=True)
    parser.add_argument('--prefix', type=Path)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    if args.mode == 'pack' and not args.prefix:
        parser.error('pack requires the exact owned source-built prefix')
    result = pack(args.prefix, args.bundle, args.target) if args.mode == 'pack' else verify(args.bundle, args.target)
    if args.report:
        args.report.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result))
