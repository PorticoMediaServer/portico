#!/usr/bin/env python3
"""Native patch-deletion and one-object Objective-C proof; never builds dependencies."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def control(work):
    work.mkdir(parents=True, exist_ok=False)
    # Exercise the actual native patch implementation rather than assuming
    # GNU/Linux's delete-empty behavior carries over to Apple's BSD patch.
    patch = work / 'delete.patch'
    patch.write_text('--- old.c\n+++ /dev/null\n@@ -1 +0,0 @@\n-delete me\n')
    for label, args in [('original', []), ('corrected', ['-E'])]:
        directory = work / label; directory.mkdir()
        (directory / 'old.c').write_text('delete me\n')
        subprocess.run(['patch', '--batch', '--fuzz=0', *args, '-p0', '-i', patch], cwd=directory, check=True)
    original_kept_empty = (work / 'original/old.c').exists()
    assert not (work / 'corrected/old.c').exists()
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    archive = work / 'source.tar.gz'
    with urllib.request.urlopen(lock['ffmpeg']['source'], timeout=60) as response, archive.open('wb') as output:
        while chunk := response.read(1024**2): output.write(chunk)
    with archive.open('rb') as stream:
        assert hashlib.file_digest(stream, 'sha256').hexdigest() == lock['ffmpeg']['sha256']
    spec = importlib.util.spec_from_file_location('prepare', ROOT / 'scripts/prepare-ffmpeg-source.py')
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    source = work / 'source'; module.prepare(archive, source, lock)
    module.check_objective_c_sources(source)
    assert not (source / 'libavfilter/vf_transpose_vt.c').exists()
    assert 'const FFFilter ff_vf_transpose_vt' in (source / 'libavfilter/vf_transpose_vt.m').read_text()
    # No retained prefix exists for41304996046 (capture requires linked tools).
    # This filter needs only the real Apple SDK; build exactly its object, with
    # every third-party library disabled, never recompiling a dependency.
    configure = ['./configure', '--disable-everything', '--disable-programs', '--disable-doc',
                 '--disable-autodetect', '--disable-x86asm', '--enable-coreimage',
                 '--enable-videotoolbox', '--enable-filter=transpose_vt']
    result = subprocess.run(configure, cwd=source, text=True, capture_output=True)
    (work / 'configure.log').write_text(result.stdout + result.stderr)
    if result.returncode:
        print((source / 'ffbuild/config.log').read_text(), flush=True)
        result.check_returncode()
    result = subprocess.run(['make', '-j2', 'libavfilter/vf_transpose_vt.o'], cwd=source, text=True, capture_output=True)
    (work / 'object.log').write_text(result.stdout + result.stderr); result.check_returncode()
    assert 'OBJCC' in result.stdout, result.stdout
    symbols = subprocess.check_output(['/usr/bin/nm', '-g', source / 'libavfilter/vf_transpose_vt.o'], text=True)
    assert re.search(r'(?m)^[0-9a-fA-F]+\s+[DSRT]\s+_ff_vf_transpose_vt$', symbols), symbols
    (work / 'control.json').write_text(json.dumps(dict(controlOnly=True, engineBuild=False, qualified=False,
        release=False, dependencyCompileInvocations=0, originalPatchKeptEmptyFile=original_kept_empty,
        correctedPatchRemovedFile=True, objectiveCRule=True, symbol=symbols), indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__); parser.add_argument('--work', type=Path, required=True)
    control(parser.parse_args().work)
