#!/usr/bin/env python3
"""Qualify only the approved Intel GMP/Nettle boundary, never an engine/install."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import runpy
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
CONTEXT = runpy.run_path(str(ROOT / 'scripts/qualification-context.py'))


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def native(context, env):
    if platform.system() != 'Darwin' or platform.machine() != 'x86_64':
        raise ValueError('native Intel Mac required for GMP qualification')
    try:
        translated = subprocess.check_output(['/usr/sbin/sysctl', '-n', 'sysctl.proc_translated'],
                                             stderr=subprocess.DEVNULL, text=True).strip()
    except subprocess.CalledProcessError:
        translated = '0'  # The key is absent on Intel hardware.
    if translated == '1':
        raise ValueError('Rosetta does not qualify the native Intel GMP boundary')
    if context == 'github-actions' and env.get('GITHUB_EVENT_NAME') != 'workflow_dispatch':
        raise ValueError('GMP compiler probe is manual only')
    return CONTEXT['guard'](env, context)


def program(text):
    functions = []
    for name in ('make_dir', 'remove_dir', 'download', 'execute', 'build', 'build_done'):
        matches = re.findall(r'(?ms)^' + name + r'\(\) \{\n.*?^\}', text)
        if len(matches) != 1:
            raise ValueError('pinned Homebridge helper changed: ' + name)
        functions.append(matches[0])
    # Forward the identical argv/status, but retain successful upstream test
    # output too. The original helper hides it in a shell variable on success.
    functions[3] = 'execute() { printf "$"; printf " %q" "$@"; printf "\\n"; "$@"; }'
    start = '  if build "gmp" "6.3.0"; then\n'
    end = '  if [[ ! ${TARGET_ARCH} == \'arm64\' ]]; then\n'
    if text.count(start) != 1 or text.count(end) != 1:
        raise ValueError('pinned GMP/Nettle section changed')
    section = text[text.index(start):text.index(end, text.index(start))]
    marker = '    execute make install\n'
    if section.count(marker) != 2 or section.count('if build "nettle" "3.10"') != 1:
        raise ValueError('pinned GMP/Nettle install boundary changed')
    if '--with-pic' not in section or 'read_only_relocs' in section or '--disable-assembly' in section:
        raise ValueError('reviewed PIC assembly policy is absent or overridden')
    # This upstream test runs after the unchanged compile and before install.
    section = section.replace(marker, '    execute make -j "${MJOBS}" check\n' + marker, 1)
    # The pinned download helper deliberately accepts unset optional $2/$3.
    return ('#!/bin/bash\nset -eo pipefail\n'
        'CWD="$PWD"; PACKAGES="$CWD/packages"; WORKSPACE="$CWD/workspace"\n'
        'TARGET_OS=darwin; TARGET_ARCH=x86_64; MJOBS=2; LATEST=false\n'
        'CFLAGS="-I${WORKSPACE}/include"; LDFLAGS="-L${WORKSPACE}/lib"\n'
        'export PATH="$WORKSPACE/bin:$PATH" PKG_CONFIG_PATH="$WORKSPACE/lib/pkgconfig:$WORKSPACE/share/pkgconfig"\n'
        'export PKG_CONFIG_LIBDIR="$PKG_CONFIG_PATH"\n'
        'mkdir -p "$PACKAGES" "$WORKSPACE"\n' + '\n'.join(functions) + '\n' + section)


def qualify(work, evidence, context):
    native(context, os.environ)
    if work.exists() or evidence.exists() or any(p.is_symlink() for p in (*work.parents, *evidence.parents)):
        raise ValueError('fresh owned work/evidence directories required')
    work.mkdir(parents=True)
    evidence.mkdir(parents=True)
    if shutil.disk_usage(work).free < 4 * 1024**3:
        raise ValueError('less than 4 GiB native scratch headroom')
    ctx = CONTEXT['receipt'](os.environ, context)
    ctx['engineSourceSHA'] = ctx.pop('installerSourceSHA')
    ctx.update(dependencyOnly=True, engineQualified=False, installerQualified=False,
               target='macos-x64', passed=False,
               unexercised=['preceding Homebridge tool builds', 'complete engine', 'x265 archive merge',
                            'installer', 'physical playback'])
    env = dict(os.environ, NUMJOBS='2', MAKEFLAGS='-j2', CMAKE_BUILD_PARALLEL_LEVEL='2')
    (work / 'tmp').mkdir()
    env['TMPDIR'] = str(work / 'tmp')
    def run(name, command, cwd=ROOT):
        with (evidence / (name + '.log')).open('w') as output:
            subprocess.run([str(x) for x in command], cwd=cwd, env=env, stdout=output,
                           stderr=subprocess.STDOUT, check=True, timeout=1200)
    try:
        changed = subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=all', '--',
                                          'scripts', 'third_party/ffmpeg'], cwd=ROOT, text=True)
        if changed:
            raise ValueError('commit source inputs before native qualification')
        run('tools', ['bash', '-c', 'uname -a; sw_vers; xcodebuild -version; xcrun --find clang; '
                     'clang --version; make --version; xcrun --find libtool; df -h'])
        run('authority', [sys.executable, ROOT / 'scripts/check-ffmpeg-build-inputs.py'])
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())['macosRecipe']
        recipe = work / 'recipe'
        run('clone', ['git', 'clone', '--no-checkout', lock['repository'], recipe])
        run('checkout', ['git', 'checkout', '--detach', lock['commit']], recipe)
        for patch_name in lock['patches']:
            patch = ROOT / patch_name
            if patch.name == 'homebridge-intel-gmp-pic.patch' and digest(patch) != lock['intelGmpPicPatch']['sha256']:
                raise ValueError('reviewed GMP PIC patch changed')
            run('patch-' + patch.stem, ['git', 'apply', '--check', patch], recipe)
            run('apply-' + patch.stem, ['git', 'apply', patch], recipe)
        script = work / 'probe.sh'
        script.write_text(program((recipe / 'build-ffmpeg').read_text()))
        run('syntax', ['bash', '-n', script])
        ctx.update(recipeCommit=lock['commit'], recipeSHA256=digest(recipe / 'build-ffmpeg'),
                   probeSHA256=digest(script), picPatchSHA256=lock['intelGmpPicPatch']['sha256'])
        run('gmp-check-nettle-build', ['bash', script], work)
        nettle = work / 'packages/nettle-3.10'
        tool = nettle / 'tools/pkcs1-conv'
        if not tool.is_file() or tool.is_symlink():
            raise ValueError('original Nettle pkcs1-conv was not linked')
        tool.unlink()  # Disposable probe output; force the same native relink.
        run('nettle-link', ['make', '-j2', 'pkcs1-conv'], nettle / 'tools')
        link = (evidence / 'nettle-link.log').read_text()
        if 'read_only_relocs' in link or 'pkcs1-conv' not in link:
            raise ValueError('unexpected or suppressed Nettle link')
        run('nettle-file', ['file', tool])
        if 'Mach-O 64-bit executable x86_64' not in (evidence / 'nettle-file.log').read_text():
            raise ValueError('Nettle tool is not a native Intel Mac executable')
        run('nettle-dynamic', ['otool', '-L', tool])
        if 'libgmp.' in (evidence / 'nettle-dynamic.log').read_text():
            raise ValueError('Nettle tool used a shared host GMP')
        gmp = work / 'packages/gmp-6.3.0'
        pic = re.findall(r'(?m)^pic_mode=(\w+)$', (gmp / 'libtool').read_text())
        if pic != ['yes']:
            raise ValueError('generated GMP libtool did not select PIC')
        (evidence / 'gmp-libtool-pic.log').write_text('pic_mode=yes\n')
        for name in ('config.log', 'config.status'):
            shutil.copyfile(gmp / name, evidence / ('gmp-' + name))
        ctx.update(passed=True, gmpMakeCheck=True, nettleOriginalLink=True,
                   staticGmpSHA256=digest(work / 'workspace/lib/libgmp.a'),
                   nettleToolSHA256=digest(tool),
                   sourceArchives={p.name: digest(p) for p in (work / 'packages').glob('*.tar.*')})
        if digest(script) != ctx['probeSHA256']:
            ctx['passed'] = False
            raise ValueError('GMP probe script changed while being executed')
    except Exception as error:
        ctx['failure'] = str(error)
        raise
    finally:
        temporary = evidence / 'gmp-probe.new'
        temporary.write_text(json.dumps(ctx, indent=2) + '\n')
        temporary.replace(evidence / 'gmp-probe.json')
    return ctx


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', choices=['local', 'github-actions'], required=True)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    qualify(args.work.absolute(), args.evidence.absolute(), args.context)
