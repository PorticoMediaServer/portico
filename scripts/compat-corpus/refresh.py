#!/usr/bin/env python3
"""Record current producer responses from disposable loopback-only music state.

Run under portico-heavy locally. Full suites and released-tool qualification run
on GitHub. No existing server, credentials or database is used.
"""
import argparse
import importlib.util
import json
import hashlib
import re
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def recording_build(binary, revision):
    """Bind the recording to the binary's Go VCS metadata, not the runner's cwd."""
    metadata = json.loads(subprocess.check_output(['go', 'version', '-m', '-json', str(binary)], text=True))
    settings = {item['Key']: item.get('Value', '') for item in metadata.get('Settings', [])}
    if not re.fullmatch('[a-f0-9]{40}', settings.get('vcs.revision', '')) or settings['vcs.revision'] != revision or settings.get('vcs.modified') != 'false':
        raise RuntimeError('record only a clean binary built from the claimed producer revision')
    with binary.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    return {'sourceRevision': settings['vcs.revision'], 'modified': False, 'binaryHash': digest}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    args = parser.parse_args()
    status = subprocess.check_output(['git','status','--porcelain'], cwd=ROOT, text=True).strip()
    if status:
        raise SystemExit('record from a clean reviewed checkout; put output and scratch outside it')
    revision = subprocess.check_output(['git','rev-parse','HEAD'], cwd=ROOT, text=True).strip()
    os.umask(0o077)
    args.scratch.mkdir(parents=True, exist_ok=True)
    if not shutil.which('ffmpeg') or not shutil.which('ffprobe'):
        raise SystemExit('ffmpeg and ffprobe are required')
    spec = importlib.util.spec_from_file_location('fixture_server', ROOT / 'scripts/e2e-server.py')
    fixture = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(fixture)
    with tempfile.TemporaryDirectory(prefix='compat-', dir=args.scratch) as temporary:
        scratch = Path(temporary)
        binary = scratch / 'server'
        subprocess.run(['go', 'build', '-buildvcs=true', '-p', '2', '-o', str(binary), './cmd/server'],
                       cwd=ROOT / 'server', check=True)
        build_info = scratch / 'build-info.json'
        build_info.write_text(json.dumps(recording_build(binary, revision)))
        music = scratch / 'media/Artist/Album'
        music.mkdir(parents=True)
        for track in (1, 2):
            subprocess.run(['ffmpeg', '-hide_banner', '-loglevel', 'error', '-f', 'lavfi',
                            '-i', 'sine=frequency=440:sample_rate=48000', '-t', '2',
                            '-c:a', 'flac', '-metadata', 'artist=Corpus Artist',
                            '-metadata', 'album_artist=Corpus Artist', '-metadata', 'album=Corpus Album',
                            '-metadata', f'title=Track {track}', '-metadata', f'track={track}',
                            str(music / f'{track}.flac')], check=True)
        exercise = fixture.Exercise(scratch, binary)
        try:
            exercise.start()
            exercise.setup()
            library = exercise.api.post('/v1/libraries', {
                'name': 'Corpus music', 'kind': 'music', 'path': str(scratch / 'media'),
                'metadataAgent': 'local'}, (201,))
            job = exercise.api.post(f'/v1/libraries/{library["id"]}/scans', expected=(202,))
            def complete():
                state = exercise.api.get(f'/v1/admin/ingestion/jobs/{job["id"]}')["status"]
                if state in ('failed', 'cancelled'):
                    raise RuntimeError('synthetic corpus scan failed')
                return state in ('complete', 'complete_with_warnings')
            fixture.wait(complete, 120)
            credentials = scratch / 'record-password.json'
            credentials.write_text(json.dumps({'password': exercise.password}))
            subprocess.run(['node', '--experimental-transform-types',
                            str(ROOT / 'scripts/compat-corpus/record.mjs'), '--base', exercise.base,
                            '--username', 'owner', '--password-file', str(credentials),
                            '--build-info', str(build_info),
                            '--out', str(args.out.resolve())], cwd=ROOT, check=True)
        finally:
            exercise.stop()


if __name__ == '__main__':
    main()
