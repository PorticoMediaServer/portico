#!/usr/bin/env python3
"""Generate tiny, real, distinct media files for the P2-65 real-scan fixture.

Every file is a real container with a valid 0.1 s video and/or audio stream,
probed with ffprobe after writing, with unique bytes (varying colour, tone and
embedded comment) and no hardlink/symlink dedupe. Default is plan-only. Execution above100 requires exactly300k, explicit
acknowledgement and a private8GiB kernel quota.
"""
import argparse
import hashlib
import json
import math
import os
import pathlib
import subprocess
import sys
import time

STOP_BYTES = 7 * 1024 ** 3
HARD_BYTES = 8 * 1024 ** 3
DEFAULT_MIX = {'movies': 0.30, 'episodes': 0.50, 'music': 0.20}


def fail(message, code=2):
    print(json.dumps({'state': 'REFUSED', 'reason': message}), file=sys.stderr)
    raise SystemExit(code)


def private_root(raw):
    path = pathlib.Path(raw).absolute()
    for component in [path, *path.parents]:
        if component.is_symlink():
            fail(f'symlinked media path refused: {component}')
    return path


def split_counts(total, mix):
    shares = sum(mix.values())
    if abs(shares - 1.0) > 0.001 or any(value < 0 for value in mix.values()):
        fail('mix shares must be non-negative and sum to 1')
    counts = {kind: int(math.floor(total * mix[kind])) for kind in mix}
    counts['movies'] += total - sum(counts.values())
    return counts


def cumulative(total, mix, phase, phase_size):
    end = min(total, phase * phase_size)
    start = min(total, (phase - 1) * phase_size)
    return {kind: split_counts(end, mix)[kind] - split_counts(start, mix)[kind] for kind in mix}


def media_path(root, kind, index, phase):
    phase_dir = f'phase-{phase:02d}'
    if kind == 'movies':
        year = 1980 + index % 45
        name = f'Movie {index:06d} ({year})'
        return root / 'movies' / phase_dir / name / f'{name}.mkv'
    if kind == 'episodes':
        show, episode = divmod(index, 20)
        season, number = divmod(episode, 10)
        show_name = f'Show {show:05d}'
        return root / 'tv' / phase_dir / show_name / f'Season {season + 1:02d}' / f'{show_name} - S{season + 1:02d}E{number + 1:02d}.mkv'
    album, track = divmod(index, 10)
    artist = album // 5
    return root / 'music' / phase_dir / f'Artist {artist:05d}' / f'Album {album:05d}' / f'{track + 1:02d} - Track {index:05d}.m4a'


def command(path, kind, index, window):
    colour = (index * 2654435761) & 0xFFFFFF
    tone = 180 + index % 12000
    comment = f'p265-{kind}-{index:09d}'
    if kind == 'music':
        return ['ffmpeg', '-hide_banner', '-loglevel', 'error',
                '-f', 'lavfi', '-i', f'sine=frequency={tone}:duration={window}',
                '-c:a', 'aac', '-b:a', '12k',
                '-metadata', f'title=Track {index:05d}',
                '-metadata', f'artist=Artist {index // 50:05d}',
                '-metadata', f'album=Album {index // 10:05d}',
                '-metadata', f'comment={comment}', '-y', str(path)]
    return ['ffmpeg', '-hide_banner', '-loglevel', 'error',
            '-f', 'lavfi', '-i', f'color=c=0x{colour:06x}:s=128x72:d={window}:r=10',
            '-f', 'lavfi', '-i', f'sine=frequency={tone}:duration={window}',
            '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-b:v', '10k',
            '-c:a', 'aac', '-b:a', '8k', '-shortest',
            '-metadata', f'comment={comment}', '-y', str(path)]


def probe(path):
    try:
        out = subprocess.run(['ffprobe', '-v', 'error', '-show_entries',
                              'format=duration,size:stream=codec_type,codec_name,duration',
                              '-of', 'json', str(path)], capture_output=True, text=True, timeout=30)
    except (OSError, subprocess.TimeoutExpired) as error:
        return None, str(error)
    if out.returncode != 0:
        return None, out.stderr.strip()[:400]
    try:
        facts = json.loads(out.stdout)
    except ValueError as error:
        return None, str(error)
    duration = float((facts.get('format') or {}).get('duration') or 0)
    streams = [stream.get('codec_type') for stream in facts.get('streams') or []]
    if duration < 0.1:
        return None, f'duration {duration} below 0.1 s'
    return {'duration': duration, 'streams': streams}, None


def tree_bytes(root):
    physical = logical = 0
    if not root.exists():
        return 0, 0
    for current, directories, files in os.walk(root, followlinks=False):
        directory = os.lstat(current)
        physical += directory.st_blocks * 512
        for name in files:
            path = os.path.join(current, name)
            info = os.lstat(path)
            if os.path.islink(path):
                continue
            physical += info.st_blocks * 512
            logical += info.st_size
    return physical, logical


def hash_file(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        for block in iter(lambda: handle.read(1 << 20), b''):
            digest.update(block)
    return digest.hexdigest()


def tree_state(root):
    state = {'files': 0, 'logicalBytes': 0, 'physicalBytes': 0, 'inodeKeys': set(),
             'hardlinks': 0, 'symlinks': 0, 'duplicates': 0}
    hashes = set()
    for current, directories, files in os.walk(root, followlinks=False):
        directory = os.lstat(current)
        state['physicalBytes'] += directory.st_blocks * 512
        for name in directories + files:
            path = os.path.join(current, name)
            if os.path.islink(path):
                state['symlinks'] += 1
        for name in files:
            path = os.path.join(current, name)
            info = os.lstat(path)
            if os.path.islink(path):
                continue
            if info.st_nlink > 1:
                state['hardlinks'] += 1
            state['files'] += 1
            state['logicalBytes'] += info.st_size
            state['physicalBytes'] += info.st_blocks * 512
            state['inodeKeys'].add((info.st_dev, info.st_ino))
            digest = hash_file(path)
            if digest in hashes:
                state['duplicates'] += 1
            hashes.add(digest)
    state['inodes'] = len(state['inodeKeys'])
    del state['inodeKeys']
    return state


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', required=True)
    parser.add_argument('--total', type=int, default=300000)
    parser.add_argument('--phase', type=int, default=1)
    parser.add_argument('--phase-size', type=int, default=50000)
    parser.add_argument('--mix', default='movies=0.30,episodes=0.50,music=0.20')
    parser.add_argument('--window-seconds', type=float, default=0.1)
    parser.add_argument('--generation', choices=['fresh', 'template'], help='fresh = encode every file; template = one real template then -c copy remux')
    parser.add_argument('--reconcile-every', type=int, default=50000)
    parser.add_argument('--acknowledge-300k', action='store_true')
    parser.add_argument('--facts')
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--tiny', action='store_true')
    args = parser.parse_args()
    if args.total < 1 or args.phase < 1 or args.phase_size < 1:
        fail('total, phase and phase-size must be positive')
    if args.tiny and args.total > 100:
        fail('--tiny is the 30/100 rehearsal only')
    if args.execute and args.total > 100:
        if not (args.acknowledge_300k and args.total == 300000):
            fail('execution above 100 requires --acknowledge-300k and --total 300000')
    if args.reconcile_every < 1:
        fail('reconcile-every must be positive')
    if args.window_seconds < 0.1:
        fail('window must be at least 0.1 s')
    mix = {}
    for part in args.mix.split(','):
        key, _, value = part.partition('=')
        if key not in DEFAULT_MIX:
            fail(f'unknown mix kind {key!r}')
        mix[key] = float(value)
    root = private_root(args.root)
    counts = cumulative(args.total, mix, args.phase, args.phase_size)
    phases = max(1, math.ceil(args.total / args.phase_size))
    generation = args.generation or 'template'
    plan = {'state': 'PLAN', 'root': str(root), 'total': args.total, 'phase': args.phase,
            'phases': phases, 'phaseSize': args.phase_size, 'counts': counts,
            'windowSeconds': args.window_seconds, 'execute': False, 'generation': generation,
            'projectedPhysicalBytes': args.total * 4096,
            'stopBytes': STOP_BYTES, 'hardBytes': HARD_BYTES,
            'hardlinkOrSymlinkDedupe': False}
    if not args.execute:
        print(json.dumps(plan))
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        fail('live media generation is prohibited in CI')
    if not args.facts:
        fail('--facts is required on execute')
    root.mkdir(parents=True, exist_ok=True)
    if args.total > 100:
        from quota import require_quota
        require_quota(root)
    templates = root.parent / '.p265-templates'
    if generation == 'template':
        templates.mkdir(parents=True, exist_ok=True, mode=0o700)
        for kind, name in (('movies', 'movie.mkv'), ('music', 'track.m4a')):
            template = templates / name
            if template.exists():
                continue
            attempt = subprocess.run(command(template, kind, 0, max(0.2, args.window_seconds)),
                                     capture_output=True, text=True)
            if attempt.returncode != 0:
                fail(f'template {kind} failed: {attempt.stderr.strip()[:300]}')
    started = time.monotonic()
    generated = {'movies': 0, 'episodes': 0, 'music': 0}
    failures = []
    existing_physical, existing_logical = tree_bytes(root)
    physical, logical = existing_physical, existing_logical
    reconcile = 0
    generated_since_reconcile = 0
    capped = False
    for kind, count in counts.items():
        offset = split_counts((args.phase - 1) * args.phase_size, mix)[kind]
        for position in range(count):
            index = offset + position
            path = media_path(root, kind, index, args.phase)
            path.parent.mkdir(parents=True, exist_ok=True)
            if physical + 32 * 1024 >= STOP_BYTES:
                failures.append({'kind': kind, 'index': index, 'error': 'planned write would cross the 7 GiB stop'})
                capped = True
                break
            if generation == 'template':
                source = templates / ('movie.mkv' if kind != 'music' else 'track.m4a')
                album, track = divmod(index, 10)
                copy = ['ffmpeg', '-hide_banner', '-loglevel', 'error', '-i', str(source), '-c', 'copy',
                        '-metadata', f'comment=p265-{kind}-{index:09d}',
                        '-metadata', f'title={path.stem}']
                if kind == 'music':
                    copy += ['-metadata', f'artist=Artist {album // 5:05d}',
                             '-metadata', f'album=Album {album:05d}',
                             '-metadata', f'track={track + 1:02d}',
                             '-metadata', f'disc=1']
                else:
                    copy += ['-metadata', f'date={1980 + index % 45}']
                copy += ['-y', str(path)]
                attempt = subprocess.run(copy, capture_output=True, text=True)
            else:
                attempt = subprocess.run(command(path, kind, index, args.window_seconds),
                                         capture_output=True, text=True)
            if attempt.returncode != 0:
                failures.append({'kind': kind, 'index': index, 'error': attempt.stderr.strip()[:400]})
                continue
            facts, error = probe(path)
            if error:
                failures.append({'kind': kind, 'index': index, 'error': error})
                continue
            if kind == 'music' and 'audio' not in facts['streams']:
                failures.append({'kind': kind, 'index': index, 'error': 'no audio stream'})
                continue
            if kind != 'music' and not {'video', 'audio'}.issubset(set(facts['streams'])):
                failures.append({'kind': kind, 'index': index, 'error': 'missing video/audio stream'})
                continue
            info = os.lstat(path)
            physical += info.st_blocks * 512
            logical += info.st_size
            generated[kind] += 1
            generated_since_reconcile += 1
            reconcile += 1
            if reconcile >= args.reconcile_every:
                measured, _ = tree_bytes(root)
                # Directory blocks grow as new title/album folders are added; reconcile them here.
                # File blocks were charged immediately, and the 8 GiB filesystem quota is the hard cap.
                physical = measured
                reconcile = 0
                generated_since_reconcile = 0
            if physical >= STOP_BYTES or physical >= HARD_BYTES:
                failures.append({'kind': kind, 'index': index, 'error': 'byte cap reached; generation stopped'})
                capped = True
                break
        if capped:
            break
    state = tree_state(root)
    receipt = {**plan, 'state': 'GENERATED', 'execute': True, 'generated': generated,
               'failures': failures[:20], 'failureCount': len(failures),
               'capped': capped, 'generation': generation, 'reconciledAt': args.reconcile_every,
               'files': state['files'],
               'logicalBytes': state['logicalBytes'], 'physicalBytes': state['physicalBytes'],
               'generatedPhysicalBytes': physical, 'inodes': state['inodes'],
               'hardlinks': state['hardlinks'], 'symlinks': state['symlinks'],
               'duplicates': state['duplicates'],
               'elapsedSeconds': round(time.monotonic() - started, 3),
               'generatedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())}
    os.makedirs(os.path.dirname(os.path.abspath(args.facts)), exist_ok=True)
    fd = os.open(args.facts, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as handle:
        json.dump(receipt, handle, indent=2)
        handle.write('\n')
    print(json.dumps(receipt))
    if failures:
        raise SystemExit(3)
    if state['duplicates'] or state['hardlinks'] or state['symlinks']:
        raise SystemExit(4)


if __name__ == '__main__':
    main()
