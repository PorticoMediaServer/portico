#!/usr/bin/env python3
"""Qualify the additive playlist-length option against real checked Blu-ray media."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import threading

LIMIT = 1 << 20
IDS = ('bluray-two-clip-bdmv', 'bluray-udf250-iso')
OPTION = re.compile(r'(?m)^\s+-min_playlist_length\s+<int>.*\(from 0 to INT_MAX\).*\(default 180\)')


def run_tool(arguments):
    environment = {'PATH': os.defpath, 'LC_ALL': 'C'}
    if os.name == 'nt':
        environment['SystemRoot'] = os.environ['SystemRoot']
    with subprocess.Popen(arguments, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, env=environment,
                          start_new_session=os.name != 'nt') as process:
        output, errors, lock = [bytearray(), bytearray()], [], threading.Lock()
        total, overflow = 0, False

        def kill():
            try:
                if os.name == 'nt':
                    process.kill()
                else:
                    os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass

        def read(stream, index):
            nonlocal total, overflow
            try:
                while block := stream.read(65536):
                    with lock:
                        total += len(block)
                        if total > LIMIT:
                            overflow = True
                            kill()
                            return
                        output[index].extend(block)
            except OSError as error:
                errors.append(error)

        readers = [threading.Thread(target=read, args=(stream, i), daemon=True)
                   for i, stream in enumerate((process.stdout, process.stderr))]
        for reader in readers:
            reader.start()
        try:
            code = process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            kill()
            process.wait()
            raise ValueError('Blu-ray protocol check exceeded 20 seconds')
        finally:
            for reader in readers:
                reader.join(timeout=1)
            kill()
        if overflow or errors or any(reader.is_alive() for reader in readers):
            raise ValueError('Blu-ray protocol check exceeded bounded output')
    return code, *(bytes(value).decode('utf-8', errors='replace') for value in output)


def qualify(ffmpeg, checked_paths):
    code, stdout, stderr = run_tool([str(ffmpeg), '-hide_banner', '-h', 'protocol=bluray'])
    if code or not OPTION.search(stdout + '\n' + stderr):
        raise ValueError('Actual Blu-ray protocol lacks min_playlist_length with default180/min0/maxINT_MAX')
    rows = []
    for identity in IDS:
        path = checked_paths[identity]
        # This is a build-time protocol integration gate, not the application's
        # mediaexec authority/seek test. Both use FFmpeg's own libbluray reader.
        prefix = [str(ffmpeg), '-hide_banner', '-nostdin', '-v', 'info',
                  '-protocol_whitelist', 'file,bluray,pipe',
                  '-format_whitelist', 'mpegts,ffmetadata', '-playlist', '1', '-angle', '0']
        decode = ['-f', 'mpegts', '-i', 'bluray:' + str(path), '-map', '0:v:0',
                  '-frames:v', '1', '-threads', '1', '-an', '-sn', '-f', 'framehash', '-']
        code, _, detail = run_tool(prefix + decode)
        if code == 0 or '0 usable playlists' not in detail:
            raise ValueError('Default180 changed or failed for an unrelated reason: ' + identity)
        code, output, detail = run_tool(prefix + ['-min_playlist_length', '60'] + decode)
        frames = [line for line in output.splitlines() if line.strip() and not line.startswith('#')]
        if code or len(frames) != 1 or not re.fullmatch(r'[0-9a-f]{64}', frames[0].split(',')[-1].strip()):
            raise ValueError('Selected short Blu-ray title did not decode with min60: ' + identity + ': ' + detail[-4000:])
        code, _, detail = run_tool(prefix + ['-min_playlist_length', '-1'] + decode)
        if code == 0 or 'out of range' not in detail:
            raise ValueError('Negative playlist minimum was not refused: ' + identity)
        rows.append({'id': identity, 'decodedFrames': 1,
                     'firstFrameSHA256': frames[0].split(',')[-1].strip(),
                     'default180RefusedShortTitle': True, 'minimum60Decoded': True,
                     'negativeMinimumRefused': True})
    if rows[0]['firstFrameSHA256'] != rows[1]['firstFrameSHA256']:
        raise ValueError('The same checked title differs between BDMV and UDF readers')
    return {'minimumOption': True, 'defaultSeconds': 180, 'selectedSeconds': 60,
            'fixtures': rows, 'scope': 'Actual FFmpeg libbluray selected-title decode only; no application seek, scanner, encryption or full VS4 claim.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ffmpeg', type=Path, required=True)
    parser.add_argument('--fixtures', type=Path, required=True)
    parser.add_argument('--fixture-sha256', required=True)
    args = parser.parse_args()
    spec = importlib.util.spec_from_file_location('disc_fixture_verifier', Path(__file__).with_name('verify-disc-bundle.py'))
    verifier = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(verifier)
    if verifier.digest(args.fixtures) != args.fixture_sha256:
        raise ValueError('Checked disc fixture manifest changed')
    fixtures = json.loads(args.fixtures.read_text())['fixtures']
    paths = {entry['id']: verifier.fixture_path(args.fixtures.parent, entry)
             for entry in fixtures if entry['id'] in IDS}
    result = qualify(args.ffmpeg, paths)
    for entry in fixtures:
        if entry['id'] in IDS:
            verifier.fixture_path(args.fixtures.parent, entry)
    print(json.dumps(result, sort_keys=True))


if __name__ == '__main__':
    main()
