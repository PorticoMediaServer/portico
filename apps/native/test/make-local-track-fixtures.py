"""Small authored RN06 fixtures; output belongs to the caller's disposable scratch."""
from pathlib import Path
import subprocess
import sys

root = Path(sys.argv[1]).resolve()
root.mkdir(parents=True, exist_ok=True)
repo = Path(__file__).resolve().parents[3]
subprocess.run(['ffmpeg', '-hide_banner', '-loglevel', 'error', '-y',
                '-f', 'lavfi', '-i', 'sine=frequency=440:duration=2',
                '-f', 'lavfi', '-i', 'sine=frequency=880:duration=2',
                '-map', '0:a', '-map', '1:a', '-c:a', 'aac',
                '-metadata:s:a:0', 'language=eng', '-metadata:s:a:1', 'language=eng',
                '-disposition:a:0', 'default', '-disposition:a:1', '0', str(root/'local-audio.mp4')], check=True)
subprocess.run([sys.executable, str(repo/'third_party/apple-subtitles/test/make-pgs.py'), str(root/'test.sup')], check=True)
subprocess.run(['ffmpeg', '-hide_banner', '-loglevel', 'error', '-y', '-fix_sub_duration', '-i', str(root/'test.sup'),
                '-map', '0:s', '-c:s', 'dvdsub', '-f', 'dvd', str(root/'test.sub')], check=True)
sub = (root/'test.sub').read_bytes()
pes = sub.find(b'\0\0\1\xbd')
pack = sub.rfind(b'\0\0\1\xba', 0, pes)
if pes < 0 or pack < 0:
    raise RuntimeError('authored subtitle packet missing')
palette = ', '.join(['000000', 'fe0000', '00ff00', '0000ff'] + ['ffffff']*12)
(root/'test.idx').write_text('# VobSub index file, v7 (do not modify this line!)\n'
                            f'size: 320x180\npalette: {palette}\nid: en, index: 0\n'
                            f'timestamp: 00:00:01:000, filepos: {pack:09x}\n')
