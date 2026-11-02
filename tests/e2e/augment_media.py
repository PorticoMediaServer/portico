"""Add small, freely generated acceptance fixtures to the server exercise media."""
from pathlib import Path
import json
import shutil
import subprocess
import sys
from zipfile import ZipFile, ZIP_DEFLATED, ZIP_STORED

ROOT = Path(__file__).resolve().parents[2]


def generate(media):
    base = media / 'base.mp4'
    artwork = ROOT / 'fixtures/library-layout/Audiobooks/Test Listener/cover.png'
    shutil.copyfile(artwork, media / 'Movies/Arrival (2016)/poster.png')
    for title in ['The exceptionally long synthetic title that tests whether cards truncate without changing their geometry', '星の港']:
        target = media / f'Movies/{title} (2024)/{title} (2024).mp4'
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(base, target)
        target.with_suffix('.nfo').write_text(f'<movie><title>{title}</title><year>2024</year></movie>', encoding='utf-8')
    show = media / 'Shows/Example Show/Season 0/Example Show S00E01.mp4'
    show.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(base, show)
    for season, episode in [(1, 2), (1, 3), (2, 3)]:
        target = media / f'Shows/Example Show/Season {season}/Example Show S{season:02}E{episode:02}.mp4'
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(base, target)
    pgs = media / 'synthetic.sup'
    subprocess.run([sys.executable, str(ROOT / 'third_party/apple-subtitles/test/make-pgs.py'), str(pgs)], check=True)
    target = media / 'Movies/Multiple Audio (2024).mkv'
    subprocess.run(['ffmpeg', '-hide_banner', '-loglevel', 'error', '-i', str(base), '-i', str(pgs),
                    '-map', '0:v', '-map', '0:a', '-map', '0:a', '-map', '1:s', '-c', 'copy',
                    '-metadata:s:a:0', 'language=eng', '-metadata:s:a:1', 'language=fra', str(target)], check=True)
    target = media / 'Movies/HDR Tagged (2024).mp4'
    subprocess.run(['ffmpeg', '-hide_banner', '-loglevel', 'error', '-i', str(base), '-c', 'copy',
                    '-color_primaries', 'bt2020', '-color_trc', 'smpte2084', '-colorspace', 'bt2020nc', str(target)], check=True)
    for title in ['Multiple Audio', 'HDR Tagged']:
        (media / f'Movies/{title} (2024).nfo').write_text(f'<movie><title>{title}</title><year>2024</year></movie>')
    target = media / 'Music/Second Artist/Second Album/01 Another.flac'
    target.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(['ffmpeg', '-hide_banner', '-loglevel', 'error', '-i', str(base), '-vn', '-c:a', 'flac',
                    '-metadata', 'artist=Second Artist', '-metadata', 'album_artist=Second Artist',
                    '-metadata', 'album=Second Album', '-metadata', 'title=Another', '-metadata', 'track=1', str(target)], check=True)
    reading = media / 'Reading/Synthetic Author/Synthetic Book.epub'
    reading.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(reading, 'w') as z:
        z.writestr('mimetype', 'application/epub+zip', compress_type=ZIP_STORED)
        z.writestr('META-INF/container.xml', '<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>', compress_type=ZIP_DEFLATED)
        z.writestr('OEBPS/content.opf', '<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:portico-e2e</dc:identifier><dc:title>Synthetic Book</dc:title><dc:creator>Synthetic Author</dc:creator><dc:language>en</dc:language><meta property="dcterms:modified">2026-01-01T00:00:00Z</meta></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="chapter"/></spine></package>', compress_type=ZIP_DEFLATED)
        z.writestr('OEBPS/nav.xhtml', '<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>', compress_type=ZIP_DEFLATED)
        z.writestr('OEBPS/chapter.xhtml', '<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><h1>Chapter</h1><p>Synthetic sample text.</p></body></html>', compress_type=ZIP_DEFLATED)
    comic = media / 'Comics/Synthetic Series/01 Synthetic Comic.cbz'
    comic.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(comic, 'w', compression=ZIP_DEFLATED) as z:
        z.write(artwork, '001.png')
        z.write(artwork, '002.png')
    def streams(path):
        return json.loads(subprocess.check_output(['ffprobe', '-v', 'error', '-show_streams', '-of', 'json', str(path)]))['streams']
    multi = streams(media / 'Movies/Multiple Audio (2024).mkv')
    if sum(s['codec_type'] == 'audio' for s in multi) != 2 or not any(s.get('codec_name') == 'hdmv_pgs_subtitle' for s in multi):
        raise RuntimeError('multi-audio/PGS fixture is incomplete')
    video = next(s for s in streams(media / 'Movies/HDR Tagged (2024).mp4') if s['codec_type'] == 'video')
    if video.get('color_transfer') != 'smpte2084' or video.get('color_primaries') != 'bt2020':
        raise RuntimeError('HDR fixture is missing its color tags')
