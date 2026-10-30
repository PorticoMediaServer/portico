#!/usr/bin/env python3
"""Qualify encoded HDR fixtures with the exact canonical Linux static prefix."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--work', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    scratch = Path(os.environ['PORTICO_LANE_SCRATCH']).resolve(strict=True)
    work = args.work.resolve(strict=True)
    if not work.is_relative_to(scratch):
        raise ValueError('HDR qualification must stay in lane scratch')
    contract_file = root / 'third_party/ffmpeg/linux-hdr-gate.v1.json'
    contract = json.loads(contract_file.read_text())
    subprocess.run(['python3', str(root / 'scripts/check-ffmpeg-build-inputs.py'),
                    '--output', str(work / 'build-source-authority.json')], check=True)
    prefix = work / 'linux-qualification-prefix'
    seal = json.loads((work / 'qualification-prefix-files.json').read_text())
    if {str(p.relative_to(prefix)): digest(p) for p in sorted(prefix.rglob('*')) if p.is_file()} != seal:
        raise ValueError('Encoded-video qualification prefix changed')
    rounds = json.loads((work / 'repeat-build.json').read_text())
    pair = {name: digest(prefix / 'bin' / name) for name in ('ffmpeg', 'ffprobe', 'portico-disc')}
    if len(rounds) != 2 or any(r['binaries'] != pair for r in rounds):
        raise ValueError('Encoded-video gate requires the actual repeated executable pair')
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    libdovi = json.loads((prefix / 'libdovi-build.json').read_text())
    oracle = work / 'libdovi/oracle/dovi_tool'
    if digest(oracle) != libdovi['oracleSHA256']:
        raise ValueError('Official source-built test oracle changed')
    inputs = {'qualify-linux-ffmpeg-hdr.py': Path(__file__).resolve(),
              'contract.json': contract_file,
              'verify-hdr10plus-encode.py': root / 'scripts/verify-hdr10plus-encode.py'}
    for key, entry in contract['inputs'].items():
        path = root / entry['path']
        if digest(path) != entry['sha256']:
            raise ValueError('MAIN encoded fixture gate input changed: ' + key)
        inputs[{'roundtrip': 'dovi-video-roundtrip.c', 'qualifier': 'qualify-hdr-video.py',
                'fixtureManifest': 'fixtures/manifest.json', 'joinContract': 'join-contract.json',
                'joinTest': 'dovi-join-packets.c', 'joinQualifier': 'qualify-dovi-join.py',
                'joinManifest': 'join-fixtures/manifest.json'}[key]] = path
    fixtures = inputs['fixtures/manifest.json'].parent
    manifest = json.loads(inputs['fixtures/manifest.json'].read_text())
    for entry in manifest['files']:
        name = entry['path']
        if Path(name).name != name:
            raise ValueError('Encoded fixture path escaped its directory')
        path = fixtures / name
        if path.stat().st_size != entry['bytes'] or digest(path) != entry['sha256']:
            raise ValueError('Encoded fixture bytes changed: ' + name)
        inputs['fixtures/' + name] = path
    join_manifest = json.loads(inputs['join-fixtures/manifest.json'].read_text())
    join_directory = inputs['join-fixtures/manifest.json'].parent
    for entry in join_manifest['fixtures']:
        name = entry['path']
        if Path(name).name != name:
            raise ValueError('Join fixture path escaped its directory')
        path = join_directory / name
        if path.stat().st_size != entry['bytes'] or digest(path) != entry['sha256']:
            raise ValueError('Join fixture bytes changed: ' + name)
        inputs['join-fixtures/' + name] = path
    hashes = {name: digest(path) for name, path in inputs.items()}
    result_file = work / 'linux-output/dovi-video-build.json'
    if result_file.exists():
        result = json.loads(result_file.read_text())
        if (result.get('qualified') is not True or result.get('inputs') != hashes or
                result.get('binarySHA256') != pair or
                result.get('sourceArchiveSHA256') != rounds[0]['sourceArchiveSHA256'] or
                result.get('qualificationPrefixSealSHA256') != digest(work / 'qualification-prefix-files.json')):
            raise ValueError('Previously qualified encoded HDR inputs changed; preserve the previous result')
        print('Reused exact binary/source/fixture-bound encoded HDR and dual-PID proof')
        return
    key = hashlib.sha256(json.dumps(hashes, sort_keys=True).encode()).hexdigest()
    frozen = work / 'linux-build-inputs' / ('hdr-' + key)
    for name, source in inputs.items():
        destination = frozen / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        if not destination.exists():
            shutil.copyfile(source, destination)
        if digest(destination) != hashes[name]:
            raise ValueError('Frozen encoded fixture gate input changed: ' + name)
    stage = work / 'linux-hdr-gate'
    if stage.exists():
        raise ValueError('Previous HDR gate output exists; retain its result before a new run')
    stage.mkdir()
    (stage / 'tmp').mkdir()
    script = stage / 'run.sh'
    script.write_text('''#!/bin/bash
set -euo pipefail
portico_output_owner="$(stat -c '%u:%g' /gate)"
portico_restore_owner() {
    portico_stage_status=$?
    trap - EXIT
    chown -hRP "$portico_output_owner" /gate
    exit "$portico_stage_status"
}
trap portico_restore_owner EXIT
export TMPDIR=/gate/tmp LC_ALL=C.UTF-8 TZ=UTC
export PKG_CONFIG_PATH=/opt/ffbuild/lib/pkgconfig:/opt/ffbuild/share/pkgconfig
export PKG_CONFIG_LIBDIR="$PKG_CONFIG_PATH"
"$CC" -std=c11 -O2 -Wall -Wextra -Werror /inputs/dovi-video-roundtrip.c \\
 -o /gate/dovi-video-roundtrip $(pkg-config --cflags --libs --static libavformat libavcodec libavutil dovi)
python3 /inputs/qualify-hdr-video.py --fixtures /inputs/fixtures --scratch /gate/runtime \\
 --ffmpeg /opt/ffbuild/bin/ffmpeg --ffprobe /opt/ffbuild/bin/ffprobe \\
 --dovi-tool /oracle/dovi_tool --roundtrip-binary /gate/dovi-video-roundtrip
python3 /inputs/qualify-linux-ffmpeg-hdr.py --inside
"$CC" -std=c11 -O2 -Wall -Wextra -Werror /inputs/dovi-join-packets.c \\
 -o /gate/dovi-join-packets $(pkg-config --cflags --libs --static libavformat libavcodec libavutil dovi)
python3 /inputs/qualify-dovi-join.py --fixtures /inputs/join-fixtures --hdr-fixtures /inputs/fixtures \\
 --scratch /gate/join-runtime --ffmpeg /opt/ffbuild/bin/ffmpeg --ffprobe /opt/ffbuild/bin/ffprobe \\
 --test-binary /gate/dovi-join-packets
python3 /inputs/verify-hdr10plus-encode.py --ffmpeg /opt/ffbuild/bin/ffmpeg \
 --ffprobe /opt/ffbuild/bin/ffprobe --fixtures /inputs/fixtures \
 --scratch /gate/runtime/hdr10plus-encode --report /gate/runtime/hdr10plus-encode.json
''')
    subprocess.run(['docker', 'run', '--rm', '--name', 'portico-hardware-r29-hdr-gate',
        '--label', 'portico.lane=hardware-r29', '--cpus', '2', '--memory', '4g', '--memory-swap', '4g',
        '--network', 'none', '-v', str(stage) + ':/gate', '-v', str(prefix) + ':/opt/ffbuild:ro',
        '-v', str(frozen) + ':/inputs:ro', '-v', str(oracle.parent) + ':/oracle:ro',
        lock['linuxSourceBuild']['compilerImage'], 'bash', '/gate/run.sh'], check=True)
    video = json.loads((stage / 'runtime/report.json').read_text())
    hdr = json.loads((stage / 'hdr10plus-report.json').read_text())
    join = json.loads((stage / 'join-runtime/report.json').read_text())
    join_contract = json.loads(inputs['join-contract.json'].read_text())
    if (video.get('passed') is not True or video['binaries']['ffmpeg'] != pair['ffmpeg'] or
            video['binaries']['ffprobe'] != pair['ffprobe'] or len(video['layers']) != 2 or
            any(layer['packets'] != 24 or not layer['officialMode2RpuIdentical'] or
                not layer['baseDecodedFramesIdentical'] for layer in video['layers']) or not hdr['passed']):
        raise ValueError('Canonical encoded-video gate did not pass')
    if (join.get('passed') is not True or join['binarySha256'] != pair['ffmpeg'] or
            join['probeSha256'] != pair['ffprobe'] or len(join['fixtures']) != join_contract['fixtures'] or
            join['joinedCases'] != join_contract['gate']['positive'] or
            join['refusalCases'] != join_contract['gate']['refused']):
        raise ValueError('Canonical corrected dual-PID fixture gate did not pass')
    encode_report = stage / 'runtime/hdr10plus-encode.json'
    hdr_encode = json.loads(encode_report.read_text())
    if (hdr_encode.get('passed') is not True or hdr_encode.get('encoder') != 'libx265' or
            hdr_encode.get('frames') != 24 or hdr_encode.get('metadataFrames') != 24 or
            hdr_encode.get('perFrameMetadataIdentical') is not True or
            hdr_encode.get('encoderOption') != 'hdr10plus' or hdr_encode.get('optionDefaultFalse') is not True or
            hdr_encode.get('defaultOffFrames') != 24 or hdr_encode.get('defaultOffMetadataFrames') != 0 or
            hdr_encode.get('explicitOffFrames') != 24 or hdr_encode.get('explicitOffMetadataFrames') != 0 or
            hdr_encode.get('ffmpegSHA256') != pair['ffmpeg'] or hdr_encode.get('ffprobeSHA256') != pair['ffprobe']):
        raise ValueError('Canonical libx265 HDR10+ encoder gate did not pass')
    if any(digest(prefix / 'bin' / name) != value for name, value in pair.items()):
        raise ValueError('Canonical executable pair changed during encoded-video gate')
    result = {'schema': 1, 'qualified': True, 'target': 'linux-x64', 'binarySHA256': pair,
        'inputs': hashes, 'sourceArchiveSHA256': rounds[0]['sourceArchiveSHA256'],
        'qualificationPrefixSealSHA256': digest(work / 'qualification-prefix-files.json'),
        'fixtureManifestSHA256': contract['inputs']['fixtureManifest']['sha256'],
        'video': video, 'hdr10plus': hdr, 'hdr10plusEncode': hdr_encode, 'join': join,
        'joinFixtureManifestSHA256': contract['inputs']['joinManifest']['sha256'],
        'joinPatchSHA256': join_contract['patchSha256'], 'scope': contract['scope']}
    result_file.write_text(json.dumps(result, indent=2) + '\n')
    # Decoded/encoded test media and the test harness are not bundle payloads.
    # Only source, fixtures and measured reports/hashes enter provenance.
    shutil.rmtree(stage / 'runtime')
    shutil.rmtree(stage / 'hdr-runtime')
    shutil.rmtree(stage / 'join-runtime')
    (stage / 'dovi-video-roundtrip').unlink()
    (stage / 'dovi-join-packets').unlink()
    print('Canonical encoded profile conversion and HDR10+ metadata gates passed')


def inside():
    stage, fixtures = Path('/gate'), Path('/inputs/fixtures')
    runtime = stage / 'hdr-runtime'
    runtime.mkdir()
    ffmpeg, ffprobe = '/opt/ffbuild/bin/ffmpeg', '/opt/ffbuild/bin/ffprobe'

    def run(argv):
        result = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=120)
        if len(result.stdout) > 4 << 20 or len(result.stderr) > 4 << 20 or result.returncode:
            raise ValueError('HDR10+ executable recipe failed: ' + result.stderr.decode(errors='replace')[-4000:])
        return result.stdout

    def metadata(path):
        data = json.loads(run([ffprobe, '-v', 'error', '-select_streams', 'v:0', '-show_frames',
                               '-of', 'json', str(path)]))
        frames = data['frames']
        values = [[v for v in f.get('side_data_list', [])
                   if '2094-40' in v.get('side_data_type', '') or 'HDR10+' in v.get('side_data_type', '')]
                  for f in frames]
        if len(frames) != 24:
            raise ValueError(f'HDR10+ fixture frame count changed: {len(frames)} instead of 24')
        if any(len(v) != 1 for v in values):
            raise ValueError('HDR10+ per-frame metadata missing')
        return [v[0] for v in values]

    source = fixtures / 'hdr10plus.hevc'
    reference = metadata(source)
    copied, encoded = runtime / 'copied.mp4', runtime / 'encoded.hevc'
    # The elementary-stream fixture has no packet PTS. The MOV muxer's
    # inferred negative timestamps otherwise create an edit list that hides
    # two frames. This gate measures all 24 metadata records, not raw-stream
    # timestamp inference; production clock preservation has separate fixtures.
    run([ffmpeg, '-v', 'error', '-nostdin', '-threads', '1', '-i', str(source), '-map', '0:v:0',
         '-an', '-c:v', 'copy', '-use_editlist', '0', str(copied)])
    run([ffmpeg, '-v', 'error', '-nostdin', '-threads', '1', '-filter_threads', '1', '-i', str(source),
         '-map', '0:v:0', '-an', '-frames:v', '24', '-c:v', 'libx265', '-hdr10plus', '1', '-threads:v', '1',
         '-pix_fmt', 'yuv420p10le', '-x265-params',
         'pools=none:frame-threads=1:wpp=0:repeat-headers=1',
         '-f', 'hevc', str(encoded)])
    if metadata(copied) != reference or metadata(encoded) != reference:
        raise ValueError('HDR10+ per-frame metadata changed during copy or CPU encoding')
    report = {'passed': True, 'frames': 24, 'sourceSHA256': digest(source),
        'copiedSHA256': digest(copied), 'encodedSHA256': digest(encoded),
        'perFrameMetadataIdentical': True, 'encoder': 'libx265',
        'scope': 'Generated HDR10+ metadata carry only; no measured mastering or physical display proof'}
    (stage / 'hdr10plus-report.json').write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    if os.sys.argv[1:] == ['--inside']:
        inside()
    else:
        main()
