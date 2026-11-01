#!/usr/bin/env python3
"""Repeat the Linux source build with one sealed dependency/compiler input."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def first_working_build(output, input_record, source_sha):
    record = output / 'first-build.json'
    if not record.exists():
        return None
    first = json.loads(record.read_text())
    if json.loads((output / 'linux-build-inputs.json').read_text()) != input_record:
        raise ValueError('First working build inputs changed; preserve its output')
    binaries = {name: digest(output / 'bin' / name)
                for name in ('ffmpeg', 'ffprobe', 'portico-disc')}
    if first['round'] != 1 or first['sourceArchiveSHA256'] != source_sha or first['binaries'] != binaries:
        raise ValueError('First working build changed')
    return first


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--work', required=True, type=Path)
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--verify-completed-only', action='store_true')
    parser.add_argument('--first-build-only', action='store_true',
                        help='publish the first working tools before repeat/HDR qualification')
    parser.add_argument('--recover-first-round', type=Path,
                        help='captured exact Docker identity for a compiler whose host wrapper was interrupted')
    parser.add_argument('--recover-exit-status', type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    work = args.work.resolve()
    deps = work / 'linux-deps'
    prefix = deps / 'prefix'
    files = json.loads((deps / 'prefix-files.json').read_text())
    actual = {str(p.relative_to(prefix)): digest(p) for p in sorted(prefix.rglob('*')) if p.is_file()}
    if actual != files:
        raise ValueError('Dependency prefix changed after qualification')
    # ENV values were emitted by the exact recipe generator, never evaluated as
    # shell text on the host. Pass them as environment entries to the compiler.
    dockerfile = (work / 'recipe/Dockerfile').read_text().replace('\\\n', ' ')
    options = {}
    for line in dockerfile.splitlines():
        if line.startswith('ENV '):
            for entry in shlex.split(line[4:]):
                if '=' in entry:
                    key, value = entry.split('=', 1)
                    if key.startswith('FF_'):
                        options[key] = value
    if 'FF_CONFIGURE' not in options:
        raise ValueError('Generated dependency configure flags missing')
    common = ['--enable-gpl', '--enable-version3', '--disable-nonfree', '--disable-libfdk-aac',
              '--disable-autodetect', '--disable-debug', '--disable-doc', '--disable-ffplay', '--enable-libdav1d',
              '--enable-libsvtav1', '--enable-libx264', '--enable-libx265', '--enable-libzimg',
              '--enable-libdovi', '--enable-libass', '--enable-fontconfig', '--enable-libfreetype', '--enable-libfribidi',
              '--enable-libharfbuzz', '--enable-libsoxr', '--enable-libbluray', '--enable-libdvdnav',
              '--enable-libdvdread', '--enable-libplacebo', '--enable-libopus', '--enable-libvpx',
              '--enable-libzvbi', '--enable-vaapi', '--enable-libdrm', '--enable-libvpl',
              '--enable-opencl', '--enable-vulkan', '--enable-ffnvcodec', '--enable-cuda-llvm',
              '--enable-nvenc', '--enable-nvdec', '--enable-amf']
    common += ['--enable-cuda', '--enable-cuvid', '--enable-openssl', '--enable-v4l2-m2m',
               '--enable-bzlib', '--enable-libopus', '--enable-libtheora', '--enable-zlib', '--enable-iconv', '--enable-lzma', '--enable-libxml2',
               '--enable-libmp3lame', '--enable-libvorbis', '--enable-libvpx', '--enable-libwebp',
               '--enable-libopenmpt', '--enable-chromaprint', '--enable-libsrt']
    options['FF_CONFIGURE'] += ' ' + ' '.join(common)
    source_sha = digest(args.source)
    object_cache = os.environ.get('PORTICO_FFMPEG_OBJECT_CACHE')
    cache_fingerprint = None
    if object_cache:
        object_cache = Path(object_cache).resolve(strict=True)
        spec = importlib.util.spec_from_file_location('protocol_object_cache',
            root / 'scripts/reuse-ffmpeg-protocol-objects.py')
        cache = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cache)
        cache_fingerprint = cache.fingerprint(object_cache, lock)
    recovery = None
    prior_inputs = None
    recovered_provenance = None
    completed_record = work / 'linux-output/linux-build-inputs.json'
    if completed_record.exists():
        recovered_provenance = json.loads(completed_record.read_text()).get('recoveredFirstRound')
        if recovered_provenance:
            prior_inputs = recovered_provenance['originalInputs']
    if args.recover_first_round:
        if not args.recover_exit_status or args.recover_exit_status.read_text().strip() != '0':
            raise ValueError('Recovery requires the actual completed Docker wait exit status 0')
        recovery = json.loads(args.recover_first_round.read_text())
        prior_inputs = recovery['originalInputs']
        if (recovery['image'] != lock['linuxSourceBuild']['compilerImage'] or
                recovery['command'] != ['bash', '/ffbuild/build.sh'] or
                recovery['memory'] != 4 * 2**30 or recovery['nanoCPUs'] != 2 * 10**9 or
                recovery['networkMode'] != 'none'):
            raise ValueError('Recovered compiler identity or resource/network boundary changed')
        # A live compiler must never race deletion or a second writer. The
        # captured full ID can be absent after --rm, but cannot still be active.
        status = subprocess.run(['docker', 'inspect', '--format', '{{.State.Running}}', recovery['containerId']],
                                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
        if status.returncode == 0 and status.stdout.strip() == 'true':
            raise ValueError('Recovered compiler is still running')
    # Snapshot the small job inputs before either build. Switching the lane's
    # branch to publish an independent checked component cannot change a
    # running container's helper, compiler capture or source lock.
    inputs = {'build-linux-ffmpeg.py': Path(__file__).resolve(),
              'build-ffmpeg.sh': root / 'scripts/build-ffmpeg.sh',
              'configure-ffmpeg-recipe.py': root / 'scripts/configure-ffmpeg-recipe.py',
              'build-portico-disc.py': root / 'scripts/build-portico-disc.py',
              'capture-compiler.py': root / 'scripts/capture-ffmpeg-compiler.py',
              'sources.lock.json': root / 'third_party/ffmpeg/sources.lock.json',
              'build-source-authority.json': work / 'build-source-authority.json',
              'check-ffmpeg-build-inputs.py': root / 'scripts/check-ffmpeg-build-inputs.py',
              'portico-disc.c': work / 'recipe/portico-disc/portico-disc.c',
              'mpegts_dovi.h': work / 'recipe/portico-disc/mpegts_dovi.h',
              'LICENSE': work / 'recipe/portico-disc/LICENSE',
              'Dockerfile': work / 'recipe/Dockerfile',
              'dovi-profile-packets.c': root / 'third_party/ffmpeg/tests/dovi-profile-packets.c',
              'portico-dovi-profile.v1.json': root / 'third_party/ffmpeg/portico-dovi-profile.v1.json'}
    fixture_root = root / 'fixtures/video/dovi-metadata'
    if object_cache:
        inputs['reuse-ffmpeg-protocol-objects.py'] = root / 'scripts/reuse-ffmpeg-protocol-objects.py'
    contract = json.loads(inputs['portico-dovi-profile.v1.json'].read_text())
    if digest(inputs['dovi-profile-packets.c']) != contract['hostProof']['packetTestSourceSha256']:
        raise ValueError('Qualified packet test source changed')
    if digest(fixture_root / 'manifest.json') != contract['hostProof']['fixtureManifestSha256']:
        raise ValueError('Qualified Dolby Vision packet fixtures changed')
    fixtures = json.loads((fixture_root / 'manifest.json').read_text())
    inputs['fixtures/manifest.json'] = fixture_root / 'manifest.json'
    for fixture in fixtures['fixtures']:
        name = fixture['path']
        if Path(name).name != name or digest(fixture_root / name) != fixture['sha256']:
            raise ValueError('Dolby Vision packet fixture checksum/path mismatch: ' + name)
        inputs['fixtures/' + name] = fixture_root / name
    if prior_inputs:
        old_key = hashlib.sha256(json.dumps(prior_inputs['files'], sort_keys=True).encode()).hexdigest()
        original_runner = work / 'linux-build-inputs' / old_key / 'build-linux-ffmpeg.py'
        if digest(original_runner) != prior_inputs['files']['build-linux-ffmpeg.py']:
            raise ValueError('Original frozen compiler runner changed')
        inputs['recovery/original-build-linux-ffmpeg.py'] = original_runner
    hashes = {name: digest(path) for name, path in inputs.items()}
    key = hashlib.sha256(json.dumps(hashes, sort_keys=True).encode()).hexdigest()
    frozen = work / 'linux-build-inputs' / key
    frozen.mkdir(parents=True, exist_ok=True)
    for name, source in inputs.items():
        destination = frozen / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        if destination.exists():
            if digest(destination) != hashes[name]:
                raise ValueError('Frozen build input changed: ' + name)
        else:
            shutil.copyfile(source, destination)
        if digest(destination) != hashes[name]:
            raise ValueError('Build input changed during capture: ' + name)
    input_record = {'files': hashes,
        'sourceArchiveSHA256': source_sha, 'compilerImage': lock['linuxSourceBuild']['compilerImage'],
        'dependencyPrefixFilesSHA256': digest(deps / 'prefix-files.json'),
        'nativeBuildTools': {'glslc': digest(deps / 'extra/glslc')}}
    if object_cache:
        input_record['protocolObjectCacheSHA256'] = cache_fingerprint
    if recovery:
        unchanged = {k: v for k, v in hashes.items()
                     if k not in ('build-linux-ffmpeg.py', 'recovery/original-build-linux-ffmpeg.py')}
        previous = {k: v for k, v in prior_inputs['files'].items() if k != 'build-linux-ffmpeg.py'}
        if unchanged != previous or any(input_record[k] != prior_inputs[k] for k in
                ('sourceArchiveSHA256', 'compilerImage', 'dependencyPrefixFilesSHA256', 'nativeBuildTools')):
            raise ValueError('Interrupted build source, compiler, dependency, recipe or fixture inputs changed')
        if recovery['env'] != {**options, 'TMPDIR': '/ffbuild/tmp',
                'PORTICO_DISC_SOURCE_COMMIT': os.environ['PORTICO_DISC_SOURCE_COMMIT'],
                'SOURCE_DATE_EPOCH': str(lock['sourceDateEpoch'])}:
            raise ValueError('Recovered compiler environment differs from the exact recipe')
        recovered_provenance = {'containerId': recovery['containerId'],
            'originalInputs': prior_inputs, 'shellSHA256': recovery['shellSHA256'],
            'dockerWaitExitStatus': 0, 'image': recovery['image'], 'command': recovery['command'],
            'env': recovery['env'], 'mounts': recovery['mounts'],
            'memory': recovery['memory'], 'nanoCPUs': recovery['nanoCPUs'], 'networkMode': recovery['networkMode']}
    if recovered_provenance:
        input_record['recoveredFirstRound'] = recovered_provenance
    completed = work / 'linux-output'
    if (completed / 'repeat-build.json').exists():
        if json.loads((completed / 'linux-build-inputs.json').read_text()) != input_record:
            raise ValueError('Completed source build inputs changed; preserve the previous output')
        rounds = json.loads((completed / 'repeat-build.json').read_text())
        binaries = {name: digest(completed / 'bin' / name) for name in ('ffmpeg', 'ffprobe', 'portico-disc')}
        if (len(rounds) != 2 or [r['round'] for r in rounds] != [1, 2] or
                any(r['sourceArchiveSHA256'] != source_sha or r['binaries'] != binaries for r in rounds)):
            raise ValueError('Completed repeated executable proof changed')
        print('Reused two identical source-built executable rounds with unchanged inputs')
        if not args.verify_completed_only and not args.first_build_only:
            subprocess.run(['python3', str(root / 'scripts/qualify-linux-ffmpeg-hdr.py'), '--work', str(work)], check=True)
        return
    if args.verify_completed_only:
        raise ValueError('Packaging requires a completed two-round source build')
    first = first_working_build(work / 'linux-output', input_record, source_sha)
    if first is not None and args.first_build_only:
        print('Reused the first working source build; repeat/HDR qualification remains')
        return
    (work / 'linux-build-inputs.json').write_text(json.dumps(input_record, indent=2) + '\n')
    results = []
    for index in (1, 2):
        if index == 1 and first is not None:
            results.append(first)
            continue
        build = work / 'linux-compile'
        recover_round = recovery is not None and index == 1
        if not recover_round:
            if build.exists():
                shutil.rmtree(build)
            build.mkdir()
            # Stage the sealed dependency files as host-side hard links.
            # FFmpeg installs distinct files into this private prefix.
            shutil.copytree(prefix, build / 'prefix', copy_function=os.link, symlinks=True)
        command = build / 'build.sh'
        shell = '''#!/bin/bash
set -euo pipefail
portico_output_owner="$(stat -c '%u:%g' /ffbuild)"
portico_restore_owner() {
    portico_stage_status=$?
    trap - EXIT
    chown -hRP "$portico_output_owner" /ffbuild
    exit "$portico_stage_status"
}
trap portico_restore_owner EXIT
export LC_ALL=C.UTF-8 TZ=UTC ZERO_AR_DATE=1
mkdir -p /ffbuild/source
tar -xf /source.tar.xz -C /ffbuild/source --strip-components=1
cd /ffbuild/source
export CFLAGS="$CFLAGS -ffile-prefix-map=/ffbuild/source=/usr/src/ffmpeg"
export CXXFLAGS="$CXXFLAGS -ffile-prefix-map=/ffbuild/source=/usr/src/ffmpeg"
./configure --prefix=/opt/ffbuild --pkg-config-flags=--static $FFBUILD_TARGET_FLAGS $FF_CONFIGURE \\
 --extra-cflags="$FF_CFLAGS" --extra-cxxflags="$FF_CXXFLAGS" --extra-libs="$FF_LIBS" \\
 --extra-ldflags="$FF_LDFLAGS" --extra-ldexeflags="$FF_LDEXEFLAGS" \\
 --cc="$CC" --cxx="$CXX" --ar="$AR" --ranlib="$RANLIB" --nm="$NM" \\
 --extra-version=Portico-Jellyfin || { cat ffbuild/config.log; exit 1; }
make -j''' + str(int(os.environ.get('PORTICO_BUILD_JOBS', '2'))) + '''
make install
python3 /capture-compiler.py --lock /sources.lock.json --output /opt/ffbuild
python3 /build-portico-disc.py --source /disc/portico-disc.c --source-commit "$PORTICO_DISC_SOURCE_COMMIT" \\
 --prefix /opt/ffbuild --output /opt/ffbuild/bin/portico-disc \\
 --metadata /opt/ffbuild/portico-disc-build.json --target linux-x64
cp /disc/LICENSE /opt/ffbuild/portico-disc-LICENSE
cp COPYING.GPLv3 /opt/ffbuild/COPYING.GPLv3
export PKG_CONFIG_PATH=/opt/ffbuild/lib/pkgconfig:/opt/ffbuild/share/pkgconfig
export PKG_CONFIG_LIBDIR="$PKG_CONFIG_PATH"
"$CC" -std=c11 -O2 -Wall -Wextra -Werror /disc/dovi-profile-packets.c \
 -o /ffbuild/dovi-profile-packets $(pkg-config --cflags --libs --static libavcodec libavutil dovi)
/ffbuild/dovi-profile-packets /disc/fixtures > /ffbuild/dovi-profile-packets.json
/opt/ffbuild/bin/ffmpeg -hide_banner -h bsf=dovi_profile > /ffbuild/dovi-profile-help.txt 2>&1
cat /ffbuild/dovi-profile-packets.json
'''
        if object_cache:
            shell = shell.replace('\nmake -j',
                '\npython3 /disc/reuse-ffmpeg-protocol-objects.py --cache /protocol-cache '
                '--source /ffbuild/source --lock /sources.lock.json --expected ' + cache_fingerprint + '\nmake -j', 1)
        if recover_round:
            if command.read_text() != shell or digest(command) != recovery['shellSHA256']:
                raise ValueError('Recovered compilation shell differs from the fresh build recipe')
        else:
            command.write_text(shell)
        argv = ['docker', 'run', '--rm', '--name', 'portico-hardware-r29-ffmpeg',
                '--cpus', '2', '--memory', '4g', '--memory-swap', '4g', '--network', 'none',
                '--label', 'portico.lane=hardware-r29',
                '-v', str(build) + ':/ffbuild', '-v', str(build / 'prefix') + ':/opt/ffbuild',
                '-v', str(args.source.resolve()) + ':/source.tar.xz:ro',
                '-v', str(frozen) + ':/disc:ro',
                '-v', str(frozen / 'build-portico-disc.py') + ':/build-portico-disc.py:ro',
                '-v', str(frozen / 'capture-compiler.py') + ':/capture-compiler.py:ro',
                '-v', str(frozen / 'sources.lock.json') + ':/sources.lock.json:ro',
                '-e', 'SOURCE_DATE_EPOCH=' + str(lock['sourceDateEpoch']),
                '-e', 'TMPDIR=/ffbuild/tmp',
                '-e', 'PORTICO_DISC_SOURCE_COMMIT=' + os.environ['PORTICO_DISC_SOURCE_COMMIT']]
        (build / 'tmp').mkdir(exist_ok=recover_round)
        if object_cache:
            argv += ['-v', str(object_cache) + ':/protocol-cache:ro']
        if (deps / 'extra/glslc').exists():
            argv += ['-v', str(deps / 'extra/glslc') + ':/usr/bin/glslc:ro']
        for key, value in options.items():
            argv += ['-e', key + '=' + value]
        argv += [lock['linuxSourceBuild']['compilerImage'], 'bash', '/ffbuild/build.sh']
        if recover_round:
            mounts = {m['destination']: (m['source'], m['rw']) for m in recovery['mounts']}
            original_frozen = original_runner.parent
            expected_mounts = {'/ffbuild': (str(build), True), '/opt/ffbuild': (str(build / 'prefix'), True),
                '/source.tar.xz': (str(args.source.resolve()), False), '/disc': (str(original_frozen), False),
                '/build-portico-disc.py': (str(original_frozen / 'build-portico-disc.py'), False),
                '/capture-compiler.py': (str(original_frozen / 'capture-compiler.py'), False),
                '/sources.lock.json': (str(original_frozen / 'sources.lock.json'), False),
                '/usr/bin/glslc': (str(deps / 'extra/glslc'), False)}
            if mounts != expected_mounts:
                raise ValueError('Recovered compiler mounts differ from the sealed recipe')
            print('Adopted completed first compiler round with exact original inputs and Docker exit 0', flush=True)
        else:
            subprocess.run(argv, check=True)
        if {str(p.relative_to(prefix)): digest(p) for p in sorted(prefix.rglob('*')) if p.is_file()} != files:
            raise ValueError('FFmpeg installation changed a sealed dependency input')
        binaries = {name: digest(build / 'prefix/bin' / name) for name in ('ffmpeg', 'ffprobe', 'portico-disc')}
        packet = json.loads((build / 'dovi-profile-packets.json').read_text())
        expected_packet = {'passed': True, 'convertedPackets': 96, 'wrongRpuProfilesRejected': 3,
                           'invalidConfigurationsRejected': 4, 'corruptRpuRejected': 1,
                           'noRpuRejected': True, 'modeIntrospection': True}
        if any(packet.get(key) != value for key, value in expected_packet.items()):
            raise ValueError('Canonical same-prefix Dolby Vision packet gate failed')
        help_text = (build / 'dovi-profile-help.txt').read_text()
        if not re.search(r'(?m)^\s+-mode\s+<int>.*\(from 2 to 2\).*\(default 2\)', help_text):
            raise ValueError('Canonical FFmpeg dovi_profile mode is not exactly 2')
        results.append({'round': index, 'sourceArchiveSHA256': source_sha, 'binaries': binaries,
                        'packetTests': packet, 'profileHelp': help_text,
                        'buildShellSHA256': digest(command), 'recoveredAfterHostInterruption': recover_round})
        (work / 'repeat-build.json').write_text(json.dumps(results, indent=2) + '\n')
        if index == 1:
            output = work / 'linux-output'
            if output.exists():
                raise ValueError('Bundle output already exists; preserve it explicitly before rebuilding')
            output.mkdir()
            (output / 'bin').mkdir()
            for tool in ('ffmpeg', 'ffprobe', 'portico-disc'):
                shutil.copyfile(build / 'prefix/bin' / tool, output / 'bin' / tool)
                shutil.copymode(build / 'prefix/bin' / tool, output / 'bin' / tool)
            for name in ('portico-disc-build.json', 'portico-disc-LICENSE', 'COPYING.GPLv3',
                         'compiler-linked-inputs.json', 'libdovi-build.json'):
                shutil.copyfile(build / 'prefix' / name, output / name)
            for directory in ('LICENSES', 'compiler-source'):
                shutil.copytree(build / 'prefix' / directory, output / directory)
            shutil.copytree(deps / 'licenses', output / 'LICENSES/dependencies')
            shutil.copyfile(deps / 'dependency-inputs.json', output / 'dependency-inputs.json')
            shutil.copyfile(deps / 'license-inventory.json', output / 'license-inventory.json')
            shutil.copyfile(deps / 'compiler.txt', output / 'compiler.txt')
            shutil.copyfile(work / 'linux-build-inputs.json', output / 'linux-build-inputs.json')
            (output / 'first-build.json').write_text(json.dumps(results[0], indent=2) + '\n')
            if args.first_build_only:
                print('First source-built ffmpeg, ffprobe and portico-disc are ready: ' + str(output / 'bin'))
                print('Repeat, HDR fixture and full bundle qualification have not run')
                return
        elif binaries != results[0]['binaries']:
            raise ValueError('Source-built executable bytes differ between repeat builds')
    (work / 'linux-output/dovi-profile-build.json').write_text(json.dumps({
        'schema': 1, 'qualified': True, 'sourceArchiveSHA256': source_sha,
        'patchSHA256': contract['ffmpeg']['patchSha256'],
        'packetTestSourceSHA256': hashes['dovi-profile-packets.c'],
        'fixtureManifestSHA256': hashes['fixtures/manifest.json'],
        'ffmpegSHA256': results[0]['binaries']['ffmpeg'],
        'packetTests': results[0]['packetTests'], 'profileHelp': results[0]['profileHelp'],
        'scope': 'Profile7 to8.1 packet metadata. No separatePID join or FEL reconstruction.'}, indent=2) + '\n')
    shutil.copyfile(work / 'repeat-build.json', work / 'linux-output/repeat-build.json')
    shutil.copyfile(work / 'linux-build-inputs.json', work / 'linux-output/linux-build-inputs.json')
    # Keep the exact second-round archives/headers only until the additional
    # encoded-video gates finish. This is owned qualification scratch, never a
    # distributed native library SDK or a replacement for target builds.
    qualification_prefix = work / 'linux-qualification-prefix'
    if qualification_prefix.exists():
        raise ValueError('An earlier qualification prefix exists; preserve its provenance before rebuilding')
    (work / 'linux-compile/prefix').rename(qualification_prefix)
    (work / 'qualification-prefix-files.json').write_text(json.dumps({
        str(p.relative_to(qualification_prefix)): digest(p)
        for p in sorted(qualification_prefix.rglob('*')) if p.is_file()}, indent=2) + '\n')
    shutil.rmtree(work / 'linux-compile')
    subprocess.run(['python3', str(root / 'scripts/qualify-linux-ffmpeg-hdr.py'), '--work', str(work)], check=True)
    print('Two source builds produced identical ffmpeg, ffprobe and portico-disc executable hashes')


if __name__ == '__main__':
    main()
