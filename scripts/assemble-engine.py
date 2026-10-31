#!/usr/bin/env python3
"""Assemble source-built Windows engine inputs; native gates remain mandatory."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
import zipfile
import importlib.util

spec = importlib.util.spec_from_file_location("engine_inputs", Path(__file__).with_name("engine_inputs.py"))
inputs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inputs)
digest, recipe_inventory, source_pairs = inputs.digest, inputs.recipe_inventory, inputs.source_pairs

spec = importlib.util.spec_from_file_location("engine_intake", Path(__file__).with_name("fetch-engine.py"))
intake = importlib.util.module_from_spec(spec)
spec.loader.exec_module(intake)

ROOT = Path(__file__).resolve().parents[1]
MAX_RAW = 3 * 1024**3
MAX_FILES = 200000


def write_json(path, value):
    temporary = path.with_suffix(path.suffix + '.new')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


def source_licenses(archive, destination):
    notices, seen, count, expanded = {}, set(), 0, 0
    with tarfile.open(archive, 'r|*') as source:
        for member in source:
            count += 1
            expanded += member.size
            path = PurePosixPath(member.name)
            if path.is_absolute() or '..' in path.parts or count > MAX_FILES or expanded > 30 * 1024**3:
                raise ValueError('unsafe/unbounded dependency source inventory')
            if not member.isfile() or '.git' in path.parts:
                continue
            name = path.name.lower()
            if not (name.startswith(('license', 'licence', 'copying', 'copyright', 'notice', 'patent', 'unlicense')) or
                    name in ('ftl.txt', 'gplv2.txt', 'gplv3.txt', 'lgplv21.txt', 'ofl.txt') or
                    any(p.lower() in ('licenses', 'licences', 'license', 'licence', 'licensing', 'legal') for p in path.parts[:-1])):
                # NVIDIA header-only sources carry their MIT/BSD notices in
                # opening comments, as in the existing Linux collector.
                if 'ffnvcodec' not in path.parts or path.suffix != '.h':
                    continue
                stream = source.extractfile(member)
                start = stream.read(min(member.size, 128 * 1024))
                match = re.match(rb'/\*.*?\*/', start, re.S)
                if not match or b'copyright' not in match[0].lower():
                    continue
                if not any(word in match[0].lower() for word in (b'permission', b'redistribution', b'license')):
                    continue
                data = match[0] + b'\n'
                relative = str(path.parent / ('LICENSE-' + path.name + '.txt'))
            else:
                if member.size > 2 * 1024**2:
                    raise ValueError('oversized dependency licence text')
                data = source.extractfile(member).read()
                relative = path.as_posix()
            if relative in seen:
                raise ValueError('duplicate source licence member')
            seen.add(relative)
            output = destination / relative
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_bytes(data)
            notices[relative] = hashlib.sha256(data).hexdigest()
    if not notices:
        raise ValueError('no actual licence texts in dependency source: ' + archive.name)
    return notices


def extract_raw(archive, output):
    with zipfile.ZipFile(archive) as source:
        members = source.infolist()
        if len(members) > MAX_FILES or sum(m.file_size for m in members) > MAX_RAW:
            raise ValueError('raw engine archive exceeds bounds')
    intake.extract(archive, output)


def pe(path, target):
    with path.open('rb') as stream:
        if stream.read(2) != b'MZ':
            raise ValueError('not a PE executable')
        stream.seek(60)
        offset = struct.unpack('<I', stream.read(4))[0]
        if offset > path.stat().st_size - 6:
            raise ValueError('invalid PE offset')
        stream.seek(offset)
        machine = 0x8664 if target == 'windows-x64' else 0xaa64
        if stream.read(4) != b'PE\0\0' or struct.unpack('<H', stream.read(2))[0] != machine:
            raise ValueError('wrong Windows executable target')


def configuration(path):
    configs = sorted(set(value.decode() for value in re.findall(rb'[\x20-\x7e]{40,}', path.read_bytes())
                         if b'--enable-gpl' in value and b'--enable-version3' in value))
    if not configs:
        raise ValueError('compiled GPLv3 configure string absent')
    for value in configs:
        if any(flag.startswith(('--enable-libfdk', '--enable-nonfree')) for flag in value.split()):
            raise ValueError('prohibited fdk/nonfree configuration')
    return configs


def build_proof(report, target, raw, recipe, source_sha, authority_sha):
    if (report.get('rawCompilation') is not True or report.get('qualified') is not False or
            report.get('target') != target or report.get('engineSourceSHA') != source_sha or
            report.get('sourceAuthoritySHA256') != authority_sha or
            report.get('rawArchives', {}).get(raw.name) != digest(raw) or
            report.get('dockerfileSHA256') != digest(recipe / 'Dockerfile') or
            report.get('recipeTreeSHA256') != inputs.json_digest(inputs.recipe_tree(recipe)) or
            report.get('configureScripts') != inputs.configure_scripts(ROOT) or
            report.get('recipeFiles') != recipe_inventory(recipe)):
        raise ValueError('raw engine/source/recipe provenance does not match this assembly')


def source_build_receipt(report, source):
    # These hashes came from the consumed build graph and frozen inputs in the
    # raw build report. Assembly checks them; it never regenerates a graph.
    return {key: report[key] for key in ('target', 'engineSourceSHA', 'dockerfileSHA256',
            'recipeTreeSHA256', 'configureScripts')} | {'schemaVersion': 1, 'release': False,
            'sourceArchiveSHA256': digest(source)}


def assemble(target, raw, recipe, output, report_file):
    if output.exists() or any(path.is_symlink() for path in (output, *output.parents)):
        raise ValueError('engine output must be fresh without symlink parents')
    output.parent.mkdir(parents=True, exist_ok=True)
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    source_sha = inputs.checked_source(ROOT)
    build_report = json.loads(report_file.read_text())
    build_proof(build_report, target, raw, recipe, source_sha, lock['sourceAuthority']['sha256'])
    inputs.compiler_inputs(recipe, build_report)
    subprocess.run([sys.executable, ROOT / 'scripts/check-ffmpeg-build-inputs.py'], check=True)
    with tempfile.TemporaryDirectory(prefix='engine-assembly-', dir=output.parent) as temp:
        work = Path(temp)
        extract_raw(raw, work / 'raw')
        for name in ('ffmpeg.exe', 'ffprobe.exe', 'portico-disc.exe'):
            found = list((work / 'raw').rglob(name))
            if len(found) != 1:
                raise ValueError('expected one source-built ' + name)
            pe(found[0], target)
        compiled_configuration = configuration(next((work / 'raw').rglob('ffmpeg.exe')))
        licenses = work / 'raw/LICENSES'
        licenses.mkdir(exist_ok=True)
        font_contract = lock['testFixtures']['subtitleFont']
        font = ROOT / 'packages/reader-core/assets/Literata[opsz,wght].ttf'
        font_license = font.with_name('OFL.txt')
        if digest(font) != font_contract['sha256'] or digest(font_license) != font_contract['licenseSHA256']:
            raise ValueError('checked subtitle fixture font/licence changed')
        (licenses / 'fixtures').mkdir(exist_ok=True)
        shutil.copyfile(font_license, licenses / 'fixtures/Literata-OFL.txt')
        records = []
        for script, filename in source_pairs(recipe).items():
            path = recipe / filename
            records.append({'script': script, 'sourceArchive': filename, 'sourceSHA256': digest(path),
                'licenses': source_licenses(path, licenses / Path(script).with_suffix(''))})
        compiler_sources = recipe / 'portico-compiler-inputs'
        shutil.copytree(compiler_sources / 'LICENSES', licenses / 'compiler-installed')
        for path in sorted(compiler_sources.rglob('*')):
            if path.is_file() and path.name.endswith(('.tar.xz', '.tar.gz', '.tar.bz2')):
                records.append({'compilerSourceArchive': path.relative_to(recipe).as_posix(),
                    'sourceSHA256': digest(path), 'licenses': source_licenses(path, licenses / 'compiler' / path.name)})
        write_json(licenses / 'dependency-license-inventory.json', records)
        provenance = work / 'provenance'
        (provenance / 'scripts').mkdir(parents=True)
        shutil.copytree(ROOT / 'third_party/ffmpeg', provenance / 'contract',
                        ignore=shutil.ignore_patterns('bundle', 'bundle-*'))
        subprocess.run([sys.executable, ROOT / 'scripts/check-ffmpeg-build-inputs.py', '--output',
                        provenance / 'contract/build-source-authority.json'], check=True)
        shutil.copytree(ROOT / 'third_party/portico-disc', provenance / 'portico-disc')
        shutil.copyfile(ROOT / 'LICENSE', provenance / 'portico-disc/LICENSE')
        shutil.copytree(ROOT / 'fixtures/discs', provenance / 'fixtures/discs')
        (provenance / 'fixtures/subtitles').mkdir(parents=True)
        shutil.copyfile(font, provenance / 'fixtures/subtitles/Literata.ttf')
        shutil.copyfile(font_license, provenance / 'fixtures/subtitles/OFL.txt')
        for path in (ROOT / 'scripts').glob('*'):
            if path.is_file() and ('ffmpeg' in path.name or 'engine' in path.name or 'disc' in path.name or
                                   path.name in ('build-linux-libdovi.py', 'configure-windows-libdovi.py') or
                                   path.name in inputs.CONFIGURE_SCRIPTS):
                shutil.copyfile(path, provenance / 'scripts' / path.name)
        source = work / 'corresponding-source.tar.xz'
        # The real recipe includes every downloaded dependency snapshot. Exclude
        # only compiler outputs, Git metadata and OCI layers, as in build-ffmpeg.
        subprocess.run(['tar', '-C', recipe.parent, '--sort=name', '--mtime=@' + str(lock['sourceDateEpoch']),
            '--owner=0', '--group=0', '--numeric-owner', '--hard-dereference',
            '--exclude=' + recipe.name + '/.git', '--exclude=' + recipe.name + '/workspace',
            '--exclude=' + recipe.name + '/ffbuild', '--exclude=' + recipe.name + '/artifacts', '--exclude=' + recipe.name + '/.cache/images',
            '-I', 'xz -T1 -0', '-cf', source, recipe.name, '-C', work, 'provenance'], check=True)
        receipt = source_build_receipt(build_report, source)
        intake.verify_source(source, target, receipt)
        write_json(work / 'raw/source-build-receipt.json', receipt)
        subprocess.run([sys.executable, ROOT / 'scripts/normalize-ffmpeg-bundle.py', work / 'raw', target,
                        work / 'bundle', source], check=True)
        subprocess.run([sys.executable, ROOT / 'scripts/verify-ffmpeg-manifest.py', work / 'bundle'], check=True)
        if inputs.checked_source(ROOT) != source_sha:
            raise ValueError('engine source changed during assembly')
        build_proof(json.loads(report_file.read_text()), target, raw, recipe, source_sha, lock['sourceAuthority']['sha256'])
        write_json(work / 'construction-receipt.json', {'schemaVersion': 1, 'target': target,
            'engineSourceSHA': source_sha, 'rawArchiveSHA256': digest(raw), 'sourceArchiveSHA256': digest(source),
            'compiledConfiguration': compiled_configuration, 'release': False, 'nativeEngineChecks': False,
            'unexercised': ['native ABI/features/recipes/disc runtime', 'repeat-build acceptance', 'installer'],
            'sourceAndLicenseInventory': records})
        shutil.rmtree(work / 'raw')
        shutil.rmtree(provenance)
        source.unlink()
        work.rename(output)
    return output / 'bundle'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('target', choices=['windows-x64', 'windows-arm64'])
    for name in ('raw', 'recipe', 'output', 'build-report'):
        parser.add_argument('--' + name, type=Path, required=True)
    args = parser.parse_args()
    print(assemble(args.target, args.raw, args.recipe.resolve(), args.output.absolute(), args.build_report))
