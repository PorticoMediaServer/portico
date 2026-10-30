"""Build freely authored subtitle/audio fixtures in the caller's scratch directory."""
from pathlib import Path
import subprocess
import sys

root = Path(sys.argv[1]).resolve()
root.mkdir(parents=True, exist_ok=True)
subprocess.run([sys.executable, str(Path(__file__).with_name('make-pgs.py')), str(root / 'test.sup')], check=True)


def ffmpeg(*args):
    subprocess.run(['ffmpeg', '-hide_banner', '-loglevel', 'error', '-y', *map(str, args)], check=True)


ffmpeg('-fix_sub_duration', '-i', root / 'test.sup', '-map', '0:s', '-c:s', 'dvdsub', '-f', 'dvd', root / 'test.sub')
ffmpeg('-copyts', '-fix_sub_duration', '-i', root / 'test.sup', '-map', '0:s', '-c:s', 'dvbsub', '-avoid_negative_ts', 'disabled', '-f', 'matroska', root / 'test.mks')
ffmpeg('-f', 'lavfi', '-i', 'sine=frequency=1000:duration=1', '-ac', '2', '-c:a', 'libopus', root / 'test.opus')

# A DVD mux has navigation packs before its first subtitle PES. IDX must point to
# the actual subtitle pack, never the navigation packet. A large prefix also proves
# the companion is seekable beyond FFmpeg's initial AVIO buffer.
sub = (root / 'test.sub').read_bytes()
pes = sub.find(b'\0\0\1\xbd')
pack = sub.rfind(b'\0\0\1\xba', 0, pes)
if pes < 0 or pack < 0:
    raise RuntimeError('authored DVD subtitle packet missing')
prefix = 128 * 1024
(root / 'test.sub').write_bytes(bytes(prefix) + sub)
palette = ', '.join(['000000', 'fe0000', '00ff00', '0000ff'] + ['ffffff'] * 12)
(root / 'test.idx').write_text(
    '# VobSub index file, v7 (do not modify this line!)\n'
    f'size: 320x180\npalette: {palette}\nid: en, index: 0\n'
    f'timestamp: 00:00:01:000, filepos: {prefix + pack:09x}\n'
)
