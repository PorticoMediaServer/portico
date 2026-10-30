#!/usr/bin/env python3
"""Generate the synthetic demo population: short clips at the relative paths the
demo state expects (fixtures/demo-population/manifest.json), so the demo
libraries' metadata and artwork stay valid without the original media.

Usage: generate-demo-media.py <target-dir> [--force] [--video-seconds N] [--only movies|music|tv]
Creates <target>/movies, <target>/music, <target>/tv."""
import json, os, re, subprocess, sys, pathlib

root = pathlib.Path(__file__).resolve().parents[1]
manifest = json.load(open(root / 'fixtures' / 'demo-population' / 'manifest.json'))
target = pathlib.Path(sys.argv[1]).expanduser()
force = '--force' in sys.argv
args = sys.argv[1:]
VIDEO = int(args[args.index('--video-seconds') + 1]) if '--video-seconds' in args else 12
ONLY = args[args.index('--only') + 1] if '--only' in args else None
ffmpeg = os.environ.get('PORTICO_FFMPEG', '/opt/homebrew/bin/ffmpeg')
DUR = 12  # audio

def video(dst, title, subtitle, hue):
    """A colour card with a moving test pattern strip so seeking is visible. Titles are
    not drawn: the qualified ffmpeg builds ship without drawtext."""
    dst.parent.mkdir(parents=True, exist_ok=True)
    if dst.exists() and not force: return
    graph = (f"[0:v]drawbox=x=0:y=0:w=iw:h=ih:color=0x{hue}:t=fill[bg];"
             f"[1:v]scale=640:72[strip];[bg][strip]overlay=0:288[out]")
    subprocess.run([ffmpeg, '-hide_banner', '-loglevel', 'error', '-y', '-f', 'lavfi', '-i', f'color=c=black:s=640x360:r=24:d={VIDEO}',
                    '-f', 'lavfi', '-i', f'testsrc2=s=640x72:r=24:d={VIDEO}',
                    '-f', 'lavfi', '-i', f'sine=frequency=330:sample_rate=48000:d={VIDEO}',
                    '-filter_complex', graph, '-map', '[out]', '-map', '2:a', '-c:v', 'libx264', '-preset', 'veryfast', '-crf', '28', '-pix_fmt', 'yuv420p',
                    '-c:a', 'aac', '-b:a', '96k', '-shortest', '-movflags', '+faststart',
                    '-metadata', f'title={title}', '-metadata', f'comment={subtitle}', str(dst)], check=True)

def audio(dst, artist, album, track, title, hz):
    dst.parent.mkdir(parents=True, exist_ok=True)
    if dst.exists() and not force: return
    subprocess.run([ffmpeg, '-hide_banner', '-loglevel', 'error', '-y', '-f', 'lavfi', '-i', f'sine=frequency={hz}:sample_rate=44100:d={DUR}',
                    '-af', 'tremolo=f=2:d=0.4,afade=t=in:d=1,afade=t=out:st=11:d=1', '-c:a', 'flac',
                    '-metadata', f'artist={artist}', '-metadata', f'album_artist={artist}', '-metadata', f'album={album}', '-metadata', f'title={title}', '-metadata', f'track={track}', str(dst)], check=True)

palette = ['1b4c6c', '3a2f5c', '5c2f2f', '2f5c3a', '5c4a2f', '2f4f5c']
n = 0
for i, rel in enumerate(manifest['movies'] if ONLY in (None, 'movies') else []):
    m = re.match(r'(.*) \((\d{4})\)/', rel); title, year = (m.group(1), m.group(2)) if m else (rel, '')
    video(target / 'movies' / rel, title, year, palette[i % len(palette)]); n += 1
for i, rel in enumerate(manifest['tv'] if ONLY in (None, 'tv') else []):
    show = rel.split('/')[0]; ep = re.search(r'(S\d+E\d+)', rel); part = re.search(r'S\d+E\d+ - (.*)\.mp4$', rel)
    video(target / 'tv' / rel, show, f"{ep.group(1) if ep else ''} · {part.group(1) if part else ''}", palette[(i + 3) % len(palette)]); n += 1
for i, rel in enumerate(manifest['music'] if ONLY in (None, 'music') else []):
    artist, album, file = rel.split('/'); m = re.match(r'(\d+) - (.*)\.flac$', file); track, title = (m.group(1), m.group(2)) if m else ('1', file)
    audio(target / 'music' / rel, artist, album, track, title, 220 + 20 * (i % 12)); n += 1
print(f'generated {n} files under {target}')
