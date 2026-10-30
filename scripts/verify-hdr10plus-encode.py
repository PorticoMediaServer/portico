#!/usr/bin/env python3
"""Qualify libx265 HDR10+ metadata at the source frame size/rate only."""
import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import subprocess


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def hdr_metadata(frame):
    return next((side for side in frame.get('side_data_list', [])
                 if '2094-40' in side.get('side_data_type', '')), None)


def compare_frames(before, after):
    if len(before) != 24 or not all(hdr_metadata(frame) for frame in before):
        raise ValueError('Input fixture must contain 24 actual HDR10+ frame records')
    if len(after) != 24 or [hdr_metadata(frame) for frame in before] != [hdr_metadata(frame) for frame in after]:
        raise ValueError('Encoded libx265 frames did not retain exact per-frame HDR10+ metadata')


def check_disabled(frames):
    if len(frames) != 24 or any(hdr_metadata(frame) for frame in frames):
        raise ValueError('Disabled libx265 HDR10+ carry must keep the previous 24-frame behavior')


def qualify(ffmpeg, ffprobe, fixtures, scratch):
    manifest = fixtures / 'manifest.json'
    inventory = json.loads(manifest.read_text())
    fixture = fixtures / 'hdr10plus.hevc'
    expected = next(row['sha256'] for row in inventory['files'] if row['path'] == fixture.name)
    if fixture.is_symlink() or not fixture.resolve().is_relative_to(fixtures.resolve()) or digest(fixture) != expected:
        raise ValueError('Checked HDR10+ fixture changed')
    scratch.mkdir(parents=True, exist_ok=False)
    environment = {'PATH': os.defpath, 'LC_ALL': 'C', 'CI': '1'}
    if os.name == 'nt':
        environment['SystemRoot'] = os.environ['SystemRoot']
    with (scratch / 'process.log').open('wb') as log:
        def run(args, probe=False, capture=False):
            result = subprocess.run([str(value) for value in args], stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE if probe or capture else subprocess.DEVNULL, stderr=log,
                env=environment, timeout=45, check=True)
            if probe:
                if len(result.stdout) > 2 << 20:
                    raise ValueError('Frame metadata output exceeded its bound')
                return json.loads(result.stdout)['frames']
            if capture:
                if len(result.stdout) > 2 << 20:
                    raise ValueError('Encoder help output exceeded its bound')
                return result.stdout.decode('utf-8')
        def frames(path):
            return run([ffprobe, '-v', 'error', '-select_streams', 'v:0', '-show_frames', '-of', 'json', path], True)
        before = frames(fixture)
        help_text = run([ffmpeg, '-hide_banner', '-h', 'encoder=libx265'], capture=True)
        if not re.search(r'(?m)^\s+-hdr10plus\s+<boolean>.*\(default false\)', help_text):
            raise ValueError('HDR10+ carry must expose an explicit default-false boolean')
        def encode(output, option):
            run([ffmpeg, '-hide_banner', '-nostdin', '-loglevel', 'error', '-y', '-threads', '1',
             '-i', fixture, '-map', '0:v:0', '-c:v', 'libx265', '-threads', '1',
             *option,
             '-x265-params', 'pools=none:frame-threads=1:wpp=0:hdr10=1:log-level=error',
             '-pix_fmt', 'yuv420p10le', '-color_primaries', 'bt2020', '-color_trc', 'smpte2084',
             '-colorspace', 'bt2020nc', '-frames:v', '24', '-an', '-sn', '-dn', '-f', 'hevc', output])
        output = scratch / 'encoded.hevc'
        encode(output, ['-hdr10plus', '1'])
        compare_frames(before, frames(output))
        for name, option in [('default', []), ('explicit-off', ['-hdr10plus', '0'])]:
            disabled = scratch / (name + '.hevc')
            encode(disabled, option)
            check_disabled(frames(disabled))
    if digest(fixture) != expected:
        raise ValueError('Input fixture changed during qualification')
    return {'passed': True, 'encoder': 'libx265', 'frames': 24, 'metadataFrames': 24,
            'perFrameMetadataIdentical': True, 'fixtureSHA256': expected,
            'encoderOption': 'hdr10plus', 'optionDefaultFalse': True,
            'defaultOffFrames': 24, 'defaultOffMetadataFrames': 0,
            'explicitOffFrames': 24, 'explicitOffMetadataFrames': 0,
            'ffmpegSHA256': digest(ffmpeg), 'ffprobeSHA256': digest(ffprobe),
            'scope': 'Same-size/frame-rate CPU encoder metadata only; no scaling, tone-map, application, hardware or other-encoder claim.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ffmpeg', type=Path, required=True)
    parser.add_argument('--ffprobe', type=Path, required=True)
    parser.add_argument('--fixtures', type=Path, required=True)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    result = qualify(args.ffmpeg, args.ffprobe, args.fixtures, args.scratch)
    if args.report:
        args.report.write_text(json.dumps(result, indent=2) + '\n')
    print('Qualified libx265 HDR10+ metadata for all 24 source-sized frames')


if __name__ == '__main__':
    main()
