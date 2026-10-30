"""Deterministic original video library: films, series, anime and one disc.

Libraries written below ``root`` (each entry is a scan root the state helper
should configure with the stated kind):

    Movies   kind=movie   (films, versions, editions, extras, one DVD disc)
    TV       kind=tv      (13 series: seasons, specials, multi-episode, markers)
    Anime    kind=anime   (2 series, absolute numbering)

Every picture, tone, subtitle and NFO is generated locally from FFmpeg's
lavfi sources and Pillow drawings. No commercial or supplied media is read;
the only existing bytes copied are the committed, procedural disc and HDR
fixtures noted in each manifest entry. The generation root is supplied by the
caller (the campaign uses /home/runner/portico-test-library-v3); no path here
is host-specific.

Manifest entries describe one media *file* (or one container folder) each:
``kind``, ``title`` (catalogue item title), ``path`` (relative to root) and
``features`` (what the item exercises). Several movie entries share a title
because they are versions/editions of one catalogue film; group by
``(kind, title)`` for expected item counts. ``expected_counts()`` does that.

Format spread (all short clips; duration is not the point):

* resolutions 480p, 720p, 1080p, 4K, plus the 320x180 HDR bases;
* video H.264, HEVC, AV1, VP9 and MPEG-2 (the disc title is MPEG-2 VOB);
* containers MP4, MKV, TS and WebM;
* audio AAC, AC-3, E-AC-3, DTS, Opus, FLAC, MP3 and ALAC;
* low bitrates for light software transcodes, one high-bitrate 96 s file for
  large-segment/remux checks (~16 Mbit/s, 24 s GOP, one segment > 36 MiB).

Importer facts this layout relies on (read from server/internal/ at c1c9ce7d5):

* ``naming.Read`` - "Title (Year)/Title (Year).mkv", ``-s01e02``,
  ``-s01e02-e03``, ``Season 01``/``Specials``, ``Extras``/``Featurettes``/
  ``Trailers``/``Behind The Scenes``/``Deleted Scenes``/``Interviews``
  folders and ``-trailer``-style suffixes, ``{edition-...}`` labels,
  `` - 4K`` version labels, anime bare ``Show - 001.mkv`` absolute numbers.
* ``localmetadata.VideoNFO`` - ``movie.nfo``/``tvshow.nfo``/``<stem>.nfo``;
  movie/tvshow/episodedetails roots, ``set`` with nested ``name``, ``mpaa``,
  ``rating``/``votes``, ``genre``, ``actor``, ``season``/``episode``/
  ``absolute_number``.
* ``subtitles.Inventory`` - ``<stem>.en.srt``, ``.en.forced.srt``,
  ``.en.sdh.srt``, ``.ja.ass`` beside the file or in ``Subs``/``Subtitles``.
* ``segmentdetect.ChapterKind`` - chapter titles "Recap", "Opening Titles",
  "Credits", "Next Episode Preview" become recap/intro/credits/outro markers.
* Artwork: movie ``poster.jpg``/``fanart.jpg``/``logo.png`` beside the file;
  show ``poster.jpg``/``fanart.jpg``/``logo.png`` in the show folder,
  ``seasonNN.jpg``/``season-specials-poster.jpg``, and episode
  ``<stem>-thumb.jpg``.

Every named media file has an independent inode, including copies of a shared
procedural template. Hardlinks would make inventory treat different episode
names as aliases of one physical asset. Storage therefore includes every copy;
the high-bitrate 96 s file alone is about 190 MiB.

True disc layout versus an ordinary chaptered file is explicit in features:
the disc entry says ``disc_layout:dvd`` + ``packaging:folder`` +
``container:VIDEO_TS``, while the chaptered film says ``chaptered_file:embedded``
+ ``not_a_disc``.

Dolby Vision retains the committed procedural, timestamped profile-7 MEL/FEL
MP4s (fixtures/video/dovi-probe-r40), including their explicit container DOVI
configuration. The generator verifies those records before retaining the files.
HDR10 and HLG use the adjacent generated base-pq.hevc and base-hlg.hevc streams.
"""
from pathlib import Path
import hashlib
import json
import shutil
import subprocess
import xml.etree.ElementTree as ET

from common import run, art, nfo, copy_media

LIBRARIES = [
    {'name': 'Portico Films', 'kind': 'movie', 'path': 'Movies'},
    {'name': 'Portico Television', 'kind': 'tv', 'path': 'TV'},
    {'name': 'Portico Anime', 'kind': 'anime', 'path': 'Anime'},
]

TEMPLATE_DIR = '.video-templates'
HIGH_BITRATE_SECONDS = 96

GENRES = ['Drama', 'Documentary', 'Mystery', 'Comedy', 'Science Fiction',
          'Thriller', 'History', 'Adventure', 'Animation', 'Romance']
STUDIOS = ['Northlight Pictures', 'Harbour & Vale', 'Lantern House',
           'Coldwater Studio', 'Meridian Film Works', 'Orchard Lane Films']
COUNTRIES = ['United Kingdom', 'Canada', 'France', 'Japan', 'Norway',
             'Australia', 'Ireland']
CERTIFICATIONS = ['G', 'PG', 'PG', 'PG-13', 'PG-13', 'R', 'R', 'NR',
                  'TV-14', 'TV-MA']
NAMES = [
    'Ada Merrin', 'Bram Ostrander', 'Celia Vane', 'Dorian Pike',
    'Edith Calloway', 'Fenn Marlow', 'Greta Solano', 'Hugo Renn',
    'Iris Tallis', 'Jonas Weir', 'Kira Bellamy', 'Lorne Ashby',
    'Mira Sandoval', 'Nils Ferrand', 'Odile Marchand', 'Perrin Holt',
    'Quinn Alder', 'Rosa Ibarra', 'Silas Crane', 'Tamsin Voss',
    'Ulla Berg', 'Viktor Salo', 'Wren Ashcombe', 'Yara Nasser',
    'Zane Okoro', 'Anouk Devlin', 'Bastian Roux', 'Clio Marchetti',
]
ADJ = ['Quiet', 'Broken', 'Golden', 'Distant', 'Borrowed', 'Hollow',
       'Patient', 'Restless', 'Copper', 'Winter', 'Silver', 'Hidden',
       'Open', 'Narrow', 'Slow', 'Bright', 'Faded', 'Second', 'Northern',
       'Salt', 'Amber', 'Violet', 'Iron', 'Paper']
NOUN = ['Signal', 'Harbour', 'Orchard', 'Lantern', 'Window', 'Letter',
        'Garden', 'Bridge', 'Room', 'River', 'Machine', 'Station', 'Field',
        'Door', 'Mirror', 'Song', 'Tide', 'Archive', 'Passage', 'Frost',
        'Ledger', 'Compass', 'Furnace', 'Choir']

# Features written for each library template: container, video codec,
# resolution, audio codec. "light" marks a source suitable for a light
# software transcode (720p or lower, low bitrate).
VARIANT_INFO = {
    'v480': ('mp4', 'h264', '854x480', 'aac', True),
    'v720': ('mp4', 'h264', '1280x720', 'aac', True),
    'v1080': ('mp4', 'h264', '1920x800', 'aac', False),
    'v2160': ('mp4', 'h264', '3840x1600', 'aac', False),
    'hevc1080': ('mkv', 'hevc', '1920x800', 'aac', False),
    'av1_720': ('mkv', 'av1', '1280x720', 'opus', True),
    'vp9_720': ('webm', 'vp9', '1280x720', 'opus', True),
    'mpeg2_480': ('ts', 'mpeg2', '720x480', 'ac3', True),
    'a_ac3': ('mkv', 'h264', '1280x720', 'ac3', True),
    'a_eac3': ('mkv', 'h264', '1280x720', 'eac3', True),
    'a_dts': ('mkv', 'h264', '1280x720', 'dts', True),
    'a_opus': ('mkv', 'h264', '1280x720', 'opus', True),
    'a_flac': ('mkv', 'h264', '1280x720', 'flac', True),
    'a_mp3': ('mp4', 'h264', '1280x720', 'mp3', True),
    'a_alac': ('mp4', 'h264', '1280x720', 'alac', True),
}

# One film per additional audio codec, plus the video codec/container showcase.
VARIANT_FILMS = {
    'A Theory of Everything Else': 'a_ac3',
    'Frozen Assets': 'a_eac3',
    'Borrowed Weather': 'a_dts',
    'The Silent Partner': 'a_opus',
    'Tin Roof Serenade': 'a_flac',
    'Mercury Falling': 'a_mp3',
    'The Accompanist': 'a_alac',
    'Gravity Well': 'hevc1080',
    'Static Bloom': 'av1_720',
    'Half-Light Hotel': 'vp9_720',
    'The Tin Star': 'mpeg2_480',
}

SPECIAL_FILMS = {
    'The Glass Meridian': {
        'year': 2014, 'cert': 'PG-13', 'rating': 8.4, 'runtime': 128,
        'collection': 'The Meridian Cycle',
        'versions': [('4K UHD', 'v2160'), ("Director's Cut", 'v1080'),
                     ('Theatrical', 'v1080')],
        'extras': [('', '-trailer', 'trailer', 'Official Trailer'),
                   ('Featurettes', '', 'featurette',
                    'Building the Meridian'),
                   ('Deleted Scenes', '', 'deleted_scene',
                    'The Observatory Scene')],
    },
    'Salt & Ember': {
        'year': 2019, 'cert': 'R', 'rating': 7.9, 'runtime': 116,
        'versions': [('4K', 'v2160'), ('1080p', 'v1080')],
        'audio': 'multi', 'subtitles': 'multi',
    },
    'Quiet Machinery': {
        'year': 2007, 'cert': 'PG-13', 'rating': 7.1, 'runtime': 104,
        'versions': [('Theatrical', 'v1080'),
                     ('Extended Edition', 'v1080')],
        'subtitles': 'subs_folder',
    },
    'A Field of Static': {
        'year': 2022, 'cert': 'TV-MA', 'rating': 8.0, 'runtime': 98,
        'versions': [('HDR10', 'hdr10'), ('Dolby Vision MEL', 'dv'), ('Dolby Vision FEL', 'dv_fel'),
                     ('SDR', 'v1080')],
    },
    'Amber Frequency': {
        'year': 2020, 'cert': 'PG', 'rating': 7.4, 'runtime': 91,
        'versions': [('HLG', 'hlg')],
    },
    'The Cartwheel Galaxy': {
        'year': 2023, 'cert': 'PG', 'rating': 8.2, 'runtime': 132,
        'versions': [('4K UHD', 'v2160')],
    },
    'The Chalk Line': {
        'year': 2013, 'cert': 'R', 'rating': 7.6, 'runtime': 119,
        'chapters': 8,
    },
    'Museum of Small Hours': {
        'year': 2011, 'cert': 'PG-13', 'rating': 8.7, 'runtime': 75,
    },
    'The Longest Winter': {
        'year': 1994, 'cert': 'PG', 'rating': 9.1, 'runtime': 90,
    },
    'The Deep Field': {
        'year': 2023, 'cert': 'PG-13', 'rating': 8.8, 'runtime': 2,
        'variant': 'high',
    },
    'The Ninth Harbour': {
        'year': 2004, 'cert': 'PG', 'rating': 7.8, 'runtime': 112,
        'collection': 'Harbour Tales', 'subtitles': 'ass_folder',
        'extras': [('', '-featurette', 'featurette',
                    'Charting the Harbour')],
    },
    'The Copper Orchard': {
        'year': 2009, 'cert': 'PG', 'rating': 7.2, 'runtime': 101,
        'collection': 'Orchard Stories',
    },
    'The Winter Orchard': {
        'year': 2015, 'cert': 'PG-13', 'rating': 7.5, 'runtime': 108,
        'collection': 'Orchard Stories',
    },
    'Orchard of Mirrors': {
        'year': 2018, 'cert': 'R', 'rating': 7.0, 'runtime': 121,
        'collection': 'Orchard Stories',
    },
    'The Wildflower Committee': {
        'year': 2016, 'cert': 'G', 'rating': 6.9, 'runtime': 88,
        'collection': 'Orchard Stories',
    },
    'Night Bus to Ostrava': {
        'year': 2012, 'cert': 'R', 'rating': 7.7, 'runtime': 114,
        'collection': 'Night Journeys',
    },
    'Last Train to Elsinore': {
        'year': 2017, 'cert': 'R', 'rating': 8.1, 'runtime': 126,
        'collection': 'Night Journeys',
    },
    'Midnight at the Observatory': {
        'year': 2006, 'cert': 'PG-13', 'rating': 7.3, 'runtime': 99,
        'collection': 'Night Journeys',
    },
    'Northbound Freight': {
        'year': 2001, 'cert': 'PG', 'rating': 7.4, 'runtime': 106,
        'collection': 'Night Journeys',
    },
    'Lantern Season': {
        'year': 2010, 'cert': 'PG', 'rating': 8.3, 'runtime': 117,
        'extras': [('', '-trailer', 'trailer', 'Lantern Season Trailer'),
                   ('Interviews', '', 'interview',
                    'Conversation with the Composer')],
    },
    'The Cartographer\'s Wife': {
        'year': 2008, 'cert': 'PG-13', 'rating': 7.9, 'runtime': 124,
        'extras': [('Behind The Scenes', '', 'behind_the_scenes',
                    'Drawing the Coast'),
                   ('', '-other', 'other', 'Title Card Gallery')],
    },
    'One More Sunrise': {
        'year': 2022, 'cert': 'PG', 'rating': 7.1, 'runtime': 96,
        'extras': [('', '-trailer', 'trailer', 'Sunrise Teaser')],
    },
}

PLAIN_FILMS = [
    'Paper Constellations', 'Halfway to Anywhere', 'Signal Fire Season',
    'Borrowed Weather', 'The Last Analog Summer', 'Ironwood',
    'The Blue Hour Agreement', 'Small Gods in the Kitchen',
    'The Silent Partner', 'Tin Roof Serenade',
    'A Theory of Everything Else', 'Frozen Assets', 'The Long Way Around',
    'Brass and Bone', 'The Understudy', 'November Light',
    'The Glassblower\'s Daughter', 'Static Bloom', 'Half-Light Hotel',
    'Eight Days in Marrakesh', 'Mercury Falling', 'The Accompanist',
    'House of Two Doors', 'Low Tide, High Water', 'The Memory Dealer',
    'A Room Without Windows', 'The Salt Road', 'Gravity Well',
    'The Hummingbird Protocol', 'The Velvet Antenna', 'The Second Sun',
    'Cold Comfort Hotel', 'The Lighthouse Keeper\'s Son', 'The Undertow',
    'Songs for a Rainy Century', 'The Ember Coast',
    'A Fine Balance of Ruin', 'The Analog Heart', 'Driftwood Cathedral',
    'Seven Kinds of Silence', 'The Green Room', 'A Small Revolution',
    'The Tin Star', 'Chrome and Cider', 'The Far Side of Morning',
]

UNICODE_FILMS = [
    ('海辺の灯台', 2011, '海辺の灯台', 'The Seaside Lighthouse', 'PG'),
    ('همس المدينة', 2016, 'همس المدينة', 'Whisper of the City', 'R'),
    ('Ένα Καλοκαίρι στη Σαντορίνη', 2012,
     'Ένα Καλοκαίρι στη Σαντορίνη', 'A Summer in Santorini', 'PG-13'),
    ('Северный ветер', 2017, 'Северный ветер', 'The North Wind', 'R'),
    ('Lumière d\'Octobre', 2009, 'Lumière d\'Octobre',
     'October Light', 'PG'),
    ('東京の雨', 2019, '東京の雨', 'Rain Over Tokyo', 'PG-13'),
    ('L\'Ultimo Inverno a Trieste', 1998,
     'L\'Ultimo Inverno a Trieste', 'The Last Winter in Trieste', 'NR'),
    ('الفيلم الطويل عن المدينة التي لا تنام', 2021,
     'الفيلم الطويل عن المدينة التي لا تنام',
     'The Long Film About the City That Never Sleeps', 'PG-13'),
]

LONG_TITLE = ('The Extraordinarily Long Chronicle of a Cartographer Who '
              'Refused to Draw the Sea')

NO_ART_FILMS = {
    'The Vanishing Projectionist', 'A Very Quiet Documentary',
    'A Film Without Metadata', 'Untitled Harbour Study',
}

NO_NFO_FILMS = {'A Film Without Metadata', 'Untitled Harbour Study'}

COLLECTION_OVERVIEW = {
    'The Meridian Cycle': 'Four original films about maps, memory and the '
                          'people who redraw both.',
    'Harbour Tales': 'Stories from one invented port town across four '
                     'decades.',
    'Orchard Stories': 'A quartet of quiet rural dramas from the same '
                       'fictional valley.',
    'Night Journeys': 'Four night-time journeys by rail, road and telescope.',
}

SHOWS = [
    {'title': 'Harrow Station', 'year': 2018, 'seasons': {1: 6, 2: 6},
     'cert': 'TV-MA', 'extras': [
         ('Featurettes', '', 'featurette', 'Making Harrow Station'),
         ('Behind The Scenes', '', 'behind_the_scenes',
          'Building the Station'),
         ('Trailers', '', 'trailer', 'Season 1 Trailer'),
         ('Deleted Scenes', '', 'deleted_scene',
          'The Missing Platform')],
     'stills': True, 'subtitles': {(1, 1): 'embedded',
                                   (1, 2): 'sidecar_set',
                                   (2, 5): 'sidecar_forced'},
     'premise': 'a night-shift stationmaster keeps a timetable nobody else '
                'can see'},
    {'title': 'The Orchard House', 'year': 2015,
     'seasons': {1: 8, 2: 8, 3: 8}, 'cert': 'TV-PG',
     'subtitles': {(1, 3): 'sidecar_forced'},
     'premise': 'three generations run an orchard that outlives them all'},
    {'title': 'Signal Ridge', 'year': 2020, 'seasons': {1: 6, 2: 24},
     'cert': 'TV-14', 'chapters': True,
     'premise': 'a mountain relay crew hears a signal from the wrong decade'},
    {'title': 'Night Shift at the Pier', 'year': 2021,
     'seasons': {1: 5, 2: 5}, 'missing': [(2, 3)], 'cert': 'TV-14',
     'subtitles': {(1, 1): 'sidecar_srt', (1, 2): 'embedded',
                   (2, 1): 'sidecar_set'},
     'premise': 'two harbour police officers share one impossible caseload'},
    {'title': 'The Cartographers', 'year': 2017, 'seasons': {1: 6},
     'specials': 3, 'multi': [(1, 2, 3)], 'cert': 'TV-14',
     'subtitles': {(1, 4): 'sidecar_forced'},
     'premise': 'mapmakers chart a coastline that keeps moving'},
    {'title': 'Orbit Classroom', 'year': 2019, 'seasons': {1: 8},
     'cert': 'TV-Y7',
     'subtitles': {(1, 1): 'ass_sidecar'},
     'premise': 'students aboard a teaching station learn in real orbit'},
    {'title': 'The Long Winter', 'year': 2022, 'seasons': {1: 6, 2: 6},
     'cert': 'TV-MA', 'premise': 'a snowbound town rations light, not food'},
    {'title': 'Marrow and Bone', 'year': 2015, 'seasons': {1: 8},
     'cert': 'TV-MA',
     'subtitles': {(1, 1): 'embedded_multi'},
     'premise': 'a forensic archivist reads bones the way others read books'},
    {'title': 'Static Garden', 'year': 2023, 'seasons': {1: 6},
     'cert': 'TV-14', 'premise': 'a greenhouse grows plants that remember '
                                'radio'},
    {'title': 'The Quiet Fleet', 'year': 2014, 'seasons': {1: 7, 2: 7},
     'cert': 'TV-PG', 'premise': 'a fishing fleet works an ocean without '
                                'fish'},
    {'title': 'Brass Lantern', 'year': 2018, 'seasons': {1: 10},
     'cert': 'TV-14', 'premise': 'a brass foundry town keeps one lamp '
                                'burning'},
    {'title': 'The Understudy\'s Room', 'year': 2013,
     'seasons': {1: 6, 2: 6}, 'cert': 'TV-PG',
     'premise': 'a theatre understudy inherits a dressing room and its '
                'ghosts'},
    {'title': 'November Rain', 'year': 2020, 'seasons': {1: 8},
     'cert': 'TV-MA', 'premise': 'a weather station logs rain that has not '
                                'fallen yet'},
]

ANIME = [
    {'title': 'Lantern Keepers', 'year': 2020, 'episodes': 24,
     'cert': 'TV-14',
     'premise': 'inherited lanterns hold the memories of a floating city'},
    {'title': 'Starfall Academy', 'year': 2022, 'episodes': 14,
     'cert': 'TV-PG',
     'premise': 'students catch falling stars before they reach the ground'},
]

DISC_SOURCE = Path(__file__).resolve().parents[2] / 'fixtures' / 'discs' / \
    'dvd-two-title-video-ts'
HDR_SOURCE = Path(__file__).resolve().parents[2] / 'fixtures' / 'video' / \
    'hdr-generated'


def _h(*parts):
    return int(hashlib.sha256('|'.join(str(p) for p in parts)
                              .encode('utf-8')).hexdigest(), 16)


def _pick(seq, *key):
    return seq[_h(*key) % len(seq)]


def _write(path, text):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding='utf-8')


def _run_ff(ffmpeg, args):
    run([ffmpeg, *args])


def _bsfs(ffmpeg):
    out = subprocess.run([str(ffmpeg), '-hide_banner', '-bsfs'],
                         capture_output=True, text=True).stdout
    return set(out.split())


def _ffprobe(ffmpeg):
    probe = Path(ffmpeg).with_name('ffprobe')
    return str(probe) if probe.exists() else 'ffprobe'


def _duration_seconds(ffmpeg, path):
    out = subprocess.run(
        [_ffprobe(ffmpeg), '-v', 'error', '-show_entries',
         'format=duration', '-of', 'json', str(path)],
        check=True, capture_output=True, text=True).stdout
    try:
        return float(json.loads(out)['format']['duration'])
    except (KeyError, ValueError, TypeError):
        return 0.0


def _has_dv_config(path):
    raw = Path(path).read_bytes()
    return b'dvcC' in raw or b'dvvC' in raw


def _art_set(folder, title, seed, poster=True, backdrop=True, logo=True):
    folder = Path(folder)
    folder.mkdir(parents=True, exist_ok=True)
    if poster:
        art(folder / 'poster.jpg', title, (600, 900), seed)
    if backdrop:
        art(folder / 'fanart.jpg', title, (1920, 1080), seed + 1)
    if logo:
        words = ' '.join(str(title).split()[:3])
        art(folder / 'logo.png', words, (800, 310), seed + 2, transparent=True)


# ---------------------------------------------------------------- templates

def _encode_h264(ffmpeg, path, width, height, duration, vbitrate,
                 abitrate=96, gop=48):
    if Path(path).exists():
        return Path(path)
    path = Path(path)
    _run_ff(ffmpeg, [
        '-y', '-f', 'lavfi',
        '-i', f'testsrc2=size={width}x{height}:rate=24',
        '-f', 'lavfi',
        '-i', 'sine=frequency=440:sample_rate=48000',
        '-t', str(duration),
        '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p',
        '-threads', '2',
        '-b:v', f'{vbitrate}k', '-maxrate', f'{vbitrate}k',
        '-minrate', f'{vbitrate}k', '-bufsize', f'{vbitrate * 2}k',
        '-x264-params',
        f'nal-hrd=cbr:force-cfr=1:keyint={gop}:min-keyint={gop}',
        '-g', str(gop),
        '-c:a', 'aac', '-b:a', f'{abitrate}k', '-shortest', str(path)])
    return path


def _encode_video_only(ffmpeg, path, width, height, duration, vbitrate,
                       gop=48):
    if Path(path).exists():
        return Path(path)
    path = Path(path)
    _run_ff(ffmpeg, [
        '-y', '-f', 'lavfi',
        '-i', f'testsrc2=size={width}x{height}:rate=24',
        '-t', str(duration),
        '-c:v', 'libx264', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p',
        '-threads', '2',
        '-b:v', f'{vbitrate}k', '-maxrate', f'{vbitrate}k',
        '-minrate', f'{vbitrate}k', '-bufsize', f'{vbitrate * 2}k',
        '-x264-params',
        f'nal-hrd=cbr:force-cfr=1:keyint={gop}:min-keyint={gop}',
        '-g', str(gop), '-an', str(path)])
    return path


def _encode_audio(ffmpeg, path, seconds, freq, channels=2, codec='aac',
                  bitrate='128k'):
    if Path(path).exists():
        return Path(path)
    path = Path(path)
    args = ['-y', '-f', 'lavfi',
            '-i', f'sine=frequency={freq}:sample_rate=48000',
            '-t', str(seconds), '-ac', str(channels)]
    if channels == 6:
        args += ['-af', 'aformat=channel_layouts=5.1']
    args += ['-c:a', codec, '-b:a', bitrate]
    if codec == 'dca':
        args += ['-strict', '-2']
    args += [str(path)]
    _run_ff(ffmpeg, args)
    return path


def _subtitles(tpl):
    srt = ('1\n00:00:00,400 --> 00:00:02,600\nThis is an original synthetic '
           'subtitle line.\n\n'
           '2\n00:00:02,800 --> 00:00:06,000\nSecond line, mapped to the '
           'generated picture and tone.\n\n'
           '3\n00:00:06,200 --> 00:00:09,400\nThird line for the middle of '
           'the clip.\n\n'
           '4\n00:00:09,600 --> 00:00:12,800\nภาษาไทย ตัวอย่างบรรทัดคำบรรยาย'
           '\n\n'
           '5\n00:00:13,000 --> 00:00:15,600\nFinal line before the end.\n')
    forced = ('1\n00:00:03,000 --> 00:00:05,000\n[Foreign dialogue] '
              'この台詞は架空のものです。\n\n'
              '2\n00:00:09,000 --> 00:00:11,000\n[Foreign dialogue] '
              'همس في الممر\n')
    sdh = ('1\n00:00:01,000 --> 00:00:03,000\n[door closes]\n\n'
           '2\n00:00:04,000 --> 00:00:07,000\nNARRATOR: An invented line of '
           'dialogue.\n\n'
           '3\n00:00:10,000 --> 00:00:12,000\n[a low tone hums]\n')
    paths = {'srt': tpl / 'sub-en.srt', 'forced': tpl / 'sub-forced.srt',
             'sdh': tpl / 'sub-sdh.srt', 'ass': tpl / 'sub-ja.ass'}
    _write(paths['srt'], srt)
    _write(paths['forced'], forced)
    _write(paths['sdh'], sdh)
    _write(paths['ass'], _sub_ass())
    return paths


def _mux(ffmpeg, out, video, audio=(), subs=(), chapters=None):
    """Copy streams into the container, adding tracks and chapters."""
    out = Path(out)
    if out.exists():
        return out
    out.parent.mkdir(parents=True, exist_ok=True)
    cmd = ['-y', '-i', str(video)]
    for _, _, path in audio:
        cmd += ['-i', str(path)]
    for _, _, path, _ in subs:
        cmd += ['-i', str(path)]
    chapter_index = None
    if chapters:
        chapter_index = 1 + len(audio) + len(subs)
        cmd += ['-i', str(chapters)]
    cmd += ['-map', '0:v:0']
    for index in range(len(audio)):
        cmd += ['-map', f'{index + 1}:a:0']
    offset = 1 + len(audio)
    for index in range(len(subs)):
        cmd += ['-map', f'{offset + index}:s:0']
    cmd += ['-c', 'copy']
    for index, (lang, title, _) in enumerate(audio):
        cmd += [f'-metadata:s:a:{index}', f'language={lang}',
                f'-metadata:s:a:{index}', f'title={title}',
                f'-disposition:a:{index}', 'default' if index == 0 else '0']
    for index, (lang, title, _, forced) in enumerate(subs):
        cmd += [f'-metadata:s:s:{index}', f'language={lang}',
                f'-metadata:s:s:{index}', f'title={title}',
                f'-disposition:s:{index}',
                'forced' if forced else ('default' if index == 0 else '0')]
    if chapters:
        cmd += ['-map_chapters', str(chapter_index)]
    cmd += [str(out)]
    _run_ff(ffmpeg, cmd)
    return out


def _chapters(path, rows, duration):
    """rows: [(start_seconds, end_seconds, title)] inside ``duration``."""
    last = max(row[1] for row in rows)
    if last >= duration:
        raise ValueError('chapter END %.3f outside %.3fs source'
                         % (last, duration))
    lines = [';FFMETADATA1']
    for start, end, title in rows:
        lines += ['[CHAPTER]', 'TIMEBASE=1/1000',
                  f'START={int(start * 1000)}', f'END={int(end * 1000)}',
                  f'title={title}']
    _write(path, '\n'.join(lines) + '\n')
    return Path(path)


def _templates(root, ffmpeg, full=True):
    tpl = Path(root) / TEMPLATE_DIR
    tpl.mkdir(parents=True, exist_ok=True)
    files = {}
    files['v480'] = _encode_h264(ffmpeg, tpl / 'movie-480p.mp4',
                                 854, 480, 8, 600, 96)
    files['v720'] = _encode_h264(ffmpeg, tpl / 'movie-720p.mp4',
                                 1280, 720, 10, 1500, 128)
    files['v1080'] = _encode_h264(ffmpeg, tpl / 'movie-1080p.mp4',
                                  1920, 800, 10, 4000, 160)
    files['v2160'] = _encode_h264(ffmpeg, tpl / 'movie-2160p.mp4',
                                  3840, 1600, 6, 12000, 192)
    files['extra'] = _encode_h264(ffmpeg, tpl / 'extra-480p.mp4',
                                  854, 480, 5, 500, 96)
    files['ep'] = _encode_h264(ffmpeg, tpl / 'episode-720p.mp4',
                               1280, 720, 16, 1200, 128)
    files['ep_video'] = _encode_video_only(ffmpeg, tpl / 'episode-video.mkv',
                                           1280, 720, 16, 1200)
    files['audio_eng'] = _encode_audio(ffmpeg, tpl / 'audio-en.m4a', 20, 440)
    files['audio_jpn'] = _encode_audio(ffmpeg, tpl / 'audio-ja.m4a', 20, 330)
    files['audio_commentary'] = _encode_audio(
        ffmpeg, tpl / 'audio-commentary.m4a', 20, 220)
    files['audio_51'] = _encode_audio(ffmpeg, tpl / 'audio-en-51.ac3', 20,
                                      440, channels=6, codec='ac3',
                                      bitrate='384k')
    files.update(_subtitles(tpl))

    # Additional video codecs and containers.
    files['hevc1080'] = _encode_other(
        ffmpeg, tpl / 'movie-hevc.mkv', 'hevc', 1920, 800, 6)
    files['av1_720'] = _encode_other(
        ffmpeg, tpl / 'movie-av1.mkv', 'av1', 1280, 720, 6)
    files['vp9_720'] = _encode_other(
        ffmpeg, tpl / 'movie-vp9.webm', 'vp9', 1280, 720, 6)
    files['mpeg2_480'] = _encode_other(
        ffmpeg, tpl / 'movie-mpeg2.ts', 'mpeg2', 720, 480, 6)

    # Additional audio codecs in a 720p MKV/MP4 shell.
    for key, (container, codec, ext, bitrate) in {
            'a_ac3': ('mkv', 'ac3', 'ac3', '192k'),
            'a_eac3': ('mkv', 'eac3', 'eac3', '224k'),
            'a_dts': ('mkv', 'dca', 'dts', '768k'),
            'a_opus': ('mkv', 'libopus', 'opus', '96k'),
            'a_flac': ('mkv', 'flac', 'flac', '128k'),
            'a_mp3': ('mp4', 'libmp3lame', 'mp3', '160k'),
            'a_alac': ('mp4', 'alac', 'm4a', '128k'),
    }.items():
        audio = _encode_audio(ffmpeg, tpl / f'audio-{key}.{ext}', 20, 440,
                              codec=codec, bitrate=bitrate)
        files[key] = _audio_shell(ffmpeg, tpl / f'movie-{key}.{container}',
                                  files['ep_video'], audio)

    # Proven procedural HDR bases, muxed (stream copy) from committed fixtures.
    files['hdr10'] = _mux_hdr(ffmpeg, tpl / 'hdr10.mp4',
                              HDR_SOURCE / 'base-pq.hevc', 'hvc1')
    files['hlg'] = _mux_hdr(ffmpeg, tpl / 'hlg.mp4',
                            HDR_SOURCE / 'base-hlg.hevc', 'hvc1')
    files['dv'] = _mux_dv(ffmpeg, tpl / 'dolby-vision.mp4')
    files['dv_fel'] = _mux_dv(ffmpeg, tpl / 'dolby-vision-fel.mp4', 'FEL')

    # Seed for the single high-bitrate play-out: 24 s at 16 Mbit/s means one
    # 24 s keyframe interval is ~48 MiB, above the 36 MiB remux target.
    files['high'] = _encode_video_only(
        ffmpeg, tpl / 'high-bitrate-seed.mp4', 1920, 800, 24, 16000,
        gop=576) if full else None
    return files


def _encode_other(ffmpeg, path, codec, width, height, duration):
    if Path(path).exists():
        return Path(path)
    path = Path(path)
    base = ['-y', '-f', 'lavfi',
            '-i', f'testsrc2=size={width}x{height}:rate=24',
            '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000',
            '-t', str(duration)]
    if codec == 'hevc':
        base += ['-c:v', 'libx265', '-preset', 'ultrafast',
                 '-x265-params',
                 'log-level=error:pools=2:frame-threads=2',
                 '-threads', '2', '-pix_fmt', 'yuv420p',
                 '-b:v', '1500k', '-c:a', 'aac', '-b:a', '128k']
    elif codec == 'av1':
        base += ['-c:v', 'libsvtav1', '-preset', '12', '-crf', '40',
                 '-svtav1-params', 'lp=2', '-threads', '2',
                 '-pix_fmt', 'yuv420p', '-c:a', 'libopus', '-b:a', '96k']
    elif codec == 'vp9':
        base += ['-c:v', 'libvpx-vp9', '-deadline', 'realtime',
                 '-cpu-used', '8', '-crf', '40', '-b:v', '0',
                 '-threads', '2', '-row-mt', '1',
                 '-pix_fmt', 'yuv420p', '-c:a', 'libopus', '-b:a', '96k']
    elif codec == 'mpeg2':
        base += ['-c:v', 'mpeg2video', '-b:v', '900k', '-threads', '2',
                 '-pix_fmt', 'yuv420p', '-c:a', 'ac3', '-b:a', '192k']
    base += ['-shortest', str(path)]
    _run_ff(ffmpeg, base)
    return path


def _audio_shell(ffmpeg, path, video, audio):
    if Path(path).exists():
        return Path(path)
    path = Path(path)
    _run_ff(ffmpeg, ['-y', '-i', str(video), '-i', str(audio),
                     '-map', '0:v:0', '-map', '1:a:0', '-c', 'copy',
                     '-metadata:s:a:0', 'language=eng',
                     '-metadata:s:a:0', 'title=English',
                     '-shortest', str(path)])
    return path


def _mux_hdr(ffmpeg, out, source, tag):
    if Path(out).exists():
        return Path(out)
    _run_ff(ffmpeg, ['-y', '-framerate', '24', '-f', 'hevc', '-i',
                     str(source), '-c', 'copy', '-tag:v', tag, str(out)])
    return Path(out)


def _mux_dv(ffmpeg, out, layer='MEL'):
    """Retain a committed procedural DV container, including its real config.

    Raw Annex-B RPU bytes and an ordinary stream copy do not create a DOVI
    configuration record. Retaining these small original containers preserves
    their explicit profile-7 configuration and authored packet timestamps.
    """
    source = HDR_SOURCE.parent / 'dovi-probe-r40' / f'source-{layer}.mp4'
    if not source.is_file():
        return None
    facts = json.loads(subprocess.run(
        [_ffprobe(ffmpeg), '-v', 'error', '-show_streams', '-of', 'json', str(source)],
        check=True, capture_output=True, text=True).stdout)
    records = [sd for stream in facts.get('streams', [])
               for sd in stream.get('side_data_list', [])
               if sd.get('side_data_type') == 'DOVI configuration record']
    if not any(sd.get('dv_profile') == 7 and sd.get('rpu_present_flag') == 1
               and sd.get('el_present_flag') == 1 for sd in records):
        raise RuntimeError('Retained Dolby Vision configuration was not found')
    out = Path(out)
    if out.exists():
        out.unlink()
    copy_media(source, out)
    return out


def _high_bitrate_file(ffmpeg, out, seed):
    if Path(out).exists():
        return Path(out)
    loop = HIGH_BITRATE_SECONDS // 24
    _run_ff(ffmpeg, ['-y', '-stream_loop', str(loop - 1), '-i', str(seed),
                     '-t', str(HIGH_BITRATE_SECONDS), '-c', 'copy',
                     str(out)])
    return Path(out)


# -------------------------------------------------------------------- films

def _film_specs():
    specs = [{'title': title} for title in PLAIN_FILMS]
    for title, year, original, translated, cert in UNICODE_FILMS:
        specs.append({'title': title, 'year': year, 'original': original,
                      'translated': translated, 'cert': cert})
    specs.append({'title': LONG_TITLE, 'year': 1908, 'cert': 'NR'})
    for extra in ['The Vanishing Projectionist', 'A Very Quiet Documentary',
                  'One More Sunrise', 'A Film Without Metadata',
                  'Untitled Harbour Study']:
        specs.append({'title': extra})
    for title, overrides in SPECIAL_FILMS.items():
        base = next((s for s in specs if s['title'] == title), {'title': title})
        base.update(overrides)
        if base not in specs:
            specs.append(base)
    return specs


def _film_details(spec):
    title = spec['title']
    year = spec.get('year') or (1978 + _h(title, 'year') % 47)
    digest = _h(title, year)
    cert = spec.get('cert') or CERTIFICATIONS[digest % len(CERTIFICATIONS)]
    rating = spec.get('rating') or round(4.8 + (digest >> 8) % 47 / 10, 1)
    votes = 180 + (digest >> 16) % 980000
    runtime = spec.get('runtime') or (82 + (digest >> 24) % 90)
    genres = [GENRES[(digest >> n) % len(GENRES)] for n in (0, 7, 14)]
    genres = list(dict.fromkeys(genres))[:2]
    studio = STUDIOS[(digest >> 5) % len(STUDIOS)]
    country = COUNTRIES[(digest >> 9) % len(COUNTRIES)]
    versions = spec.get('versions')
    if versions is None:
        variant = spec.get('variant') or VARIANT_FILMS.get(title) or \
            _tier_for_year(year)
        versions = [(None, variant)]
    return {'title': title, 'year': year, 'cert': cert, 'rating': rating,
            'votes': votes, 'runtime': runtime, 'genres': genres,
            'studio': studio, 'country': country,
            'collection': spec.get('collection'), 'versions': versions,
            'spec': spec, 'seed': _h('art', title, year) % 100000}


def _variant_features(variant):
    container, codec, resolution, audio, light = VARIANT_INFO[variant]
    features = [f'container:{container}', f'video:{codec}',
                f'resolution:{resolution}', f'audio:{audio}']
    if light:
        features.append('transcode_source:light')
    return features


def _set_container(features, container):
    for index, feature in enumerate(features):
        if feature.startswith('container:'):
            features[index] = f'container:{container}'
            return features
    features.append(f'container:{container}')
    return features


def _film_nfo(folder, detail):
    title = detail['title']
    spec = detail['spec']
    plot = ('%s. An original Portico synthetic film: every name, place and '
            'picture here is invented, and the moving image is a generated '
            'test pattern.' % _pick(
                ['A quiet story about work, weather and the people who stay.',
                 'A small drama that follows one decision across a season.',
                 'A study of maps, memory and the distance between them.',
                 'A wry comedy set where the road meets the water.',
                 'A patient mystery told through letters and recorded sound.'],
                title, 'plot'))
    fields = {
        'title': title,
        'originaltitle': spec.get('original', title),
        'sorttitle': ' '.join(title.split()[:4]),
        'plot': plot,
        'tagline': _pick(['Every map hides one road.',
                          'Some winters never end.',
                          'The signal was never for us.',
                          'Nothing here is quite what it seems.',
                          'A story told in weather.'], title, 'tag'),
        'year': detail['year'],
        'premiered': '%d-%02d-%02d' % (detail['year'],
                                       1 + _h(title) % 12,
                                       1 + _h(title, 'd') % 27),
        'mpaa': detail['cert'],
        'rating': detail['rating'],
        'votes': detail['votes'],
        'runtime': detail['runtime'],
        'genre': detail['genres'],
        'studio': detail['studio'],
        'country': detail['country'],
        'director': _pick(NAMES, title, 'director'),
        'credits': [_pick(NAMES, title, 'writer', i) for i in range(2)],
        'tag': ['synthetic', _pick(GENRES, title, 'tag')],
    }
    for index in range(4):
        fields.setdefault('actor', []).append({
            'name': _pick(NAMES, title, 'actor', index),
            'role': _pick(NAMES, title, 'role', index),
            'order': index})
    if detail['collection']:
        fields['set'] = {
            'name': detail['collection'],
            'overview': COLLECTION_OVERVIEW[detail['collection']]}
    if title not in NO_NFO_FILMS:
        nfo(folder / 'movie.nfo', 'movie', fields)


def _chapter_rows(duration, count):
    """Even named chapters for a chaptered film, all inside ``duration``."""
    rows = []
    usable = max(0.5, duration - 0.1)
    step = usable / count
    names = ['Opening', 'The House', 'The Road', 'The Letter', 'The Storm',
             'The Return', 'The Harbour', 'Closing']
    for index in range(count):
        rows.append((index * step, (index + 1) * step,
                     names[index % len(names)]))
    return rows


def _marker_rows(duration):
    """Recap/intro/credits/outro chapter labels inside the actual duration."""
    scale = min(1.0, max(0.2, (duration - 0.2) / 16.0))
    rows = [(0, 2.5, 'Recap'), (2.5, 5.0, 'Opening Titles'),
            (5.0, 9.0, 'Chapter 1'), (9.0, 13.0, 'Credits'),
            (13.0, 16.0, 'Next Episode Preview')]
    rows = [(start * scale, end * scale, title)
            for start, end, title in rows]
    rows[-1] = (rows[-1][0], min(rows[-1][1], duration - 0.1), rows[-1][2])
    return rows


def _srt_sidecar(path, text):
    _write(path, text)


EXTENSIONS = {'mp4': '.mp4', 'mkv': '.mkv', 'webm': '.webm', 'ts': '.ts'}


def _film_filename(stem, title, label, extension):
    if not label:
        return f'{stem}{extension}'
    if title == 'Salt & Ember':
        return f'{stem} - {label}{extension}'
    return f'{stem} {{edition-{label}}}{extension}'


def _films(root, tpl, ffmpeg, full):
    entries = []
    movies = Path(root) / 'Movies'
    for spec in _film_specs():
        detail = _film_details(spec)
        title, year = detail['title'], detail['year']
        folder = movies / f'{title} ({year})'
        folder.mkdir(parents=True, exist_ok=True)
        _film_nfo(folder, detail)
        if title not in NO_ART_FILMS:
            _art_set(folder, title, detail['seed'])

        spec = detail['spec']
        for label, variant in detail['versions']:
            if variant in ('dv', 'dv_fel') and tpl.get(variant) is None:
                print('video: skipped Dolby Vision version: qualified '
                      'dovi_profile BSF not available in %s' % ffmpeg,
                      flush=True)
                continue
            stem = f'{title} ({year})'
            features = ['film']
            if label:
                features.append(f'version:{label}')
            if variant in ('hdr10', 'hlg', 'dv', 'dv_fel', 'high'):
                container = 'mp4'
            else:
                container = VARIANT_INFO[variant][0]
            if spec.get('chapters') or (spec.get('audio') == 'multi'
                                        and label == '1080p'):
                container = 'mkv'
            filename = _film_filename(stem, title, label,
                                      EXTENSIONS[container])
            target = folder / filename
            if variant == 'high':
                if full:
                    _high_bitrate_file(ffmpeg, target, tpl['high'])
                    features += [
                        f'duration:{HIGH_BITRATE_SECONDS}s',
                        'bitrate:high', 'bitrate:16000k', 'gop:24s',
                        'large_segment_target:48MiB', 'remux_source']
                else:
                    copy_media(tpl['v720'], target)
                    features.append('high_bitrate_skipped')
                features += ['container:mp4', 'video:h264',
                             'resolution:1920x800', 'audio:aac',
                             'transcode_source:remux-only']
            else:
                copy_media(tpl[variant], target)
                if variant in ('hdr10', 'hlg', 'dv', 'dv_fel'):
                    features += ['container:mp4', 'video:hevc',
                                 'resolution:320x180', 'audio:none']
                else:
                    features += _variant_features(variant)
            if spec.get('chapters'):
                duration = _duration_seconds(ffmpeg, target)
                if duration <= 1:
                    raise RuntimeError('chapter source duration unknown: %s'
                                       % target)
                chapter_path = folder / '.chapters.txt'
                _chapters(chapter_path,
                          _chapter_rows(duration, spec['chapters']),
                          duration)
                muxed = folder / (filename + '.chaptered.mkv')
                _mux(ffmpeg, muxed, target, chapters=chapter_path)
                chapter_path.unlink()
                muxed.replace(target)
                features += [f'chapters:{spec["chapters"]}',
                             f'source_duration:{duration:.2f}s',
                             'chaptered_file:embedded', 'not_a_disc']
            if variant == 'hdr10':
                features += ['dynamic_range:hdr10', 'hdr10:pq',
                             'source:fixtures/video/hdr-generated/'
                             'base-pq.hevc']
            if variant == 'hlg':
                features += ['dynamic_range:hlg',
                             'source:fixtures/video/hdr-generated/'
                             'base-hlg.hevc']
            if variant in ('dv', 'dv_fel'):
                features += ['dynamic_range:dolby_vision', 'dv_profile:7',
                             'dv_layer:' + ('FEL' if variant == 'dv_fel' else 'MEL'),
                             'source:fixtures/video/dovi-probe-r40/source-' + ('FEL' if variant == 'dv_fel' else 'MEL') + '.mp4']
                if not _has_dv_config(target):
                    features.append('dolby_vision_signalling:unverified')
                else:
                    features.append('dolby_vision_signalling:container')
            if title == 'The Glass Meridian' and label == '4K UHD':
                features += ['dynamic_range:sdr']
            if spec.get('audio') == 'multi' and label == '1080p':
                target = _embedded_audio(ffmpeg, target, tpl)
                features += ['audio:eng-5.1', 'audio:jpn',
                             'audio:commentary', 'subtitle:embedded:srt',
                             'subtitle:embedded:ass-styled',
                             'subtitle:embedded:forced']
            if spec.get('subtitles') == 'multi' and label == '1080p':
                _multi_sidecars(folder, target.stem)
                features += ['subtitle:sidecar:srt',
                             'subtitle:sidecar:forced',
                             'subtitle:sidecar:sdh',
                             'subtitle:sidecar:ass-styled']
            if spec.get('subtitles') == 'subs_folder':
                subs = folder / 'Subs'
                stem_name = target.stem
                _srt_sidecar(subs / f'{stem_name}.en.srt', _sub_en())
                _srt_sidecar(subs / f'{stem_name}.en.forced.srt',
                             _sub_forced())
                _write(subs / f'{stem_name}.ja.ass', _sub_ass())
                features += ['subtitle:sidecar-subs-folder:srt',
                             'subtitle:sidecar:forced',
                             'subtitle:sidecar:ass-styled']
            if spec.get('subtitles') == 'ass_folder':
                subs = folder / 'Subs'
                _write(subs / f'{target.stem}.en.ass', _sub_ass())
                features += ['subtitle:sidecar:ass-styled',
                             'subtitle:only-external']
            if container == 'mkv':
                _set_container(features, 'mkv')
            entries.append({'kind': 'movie', 'title': title,
                            'path': target.relative_to(root).as_posix(),
                            'features': features})

        for folder_name, suffix, extra_kind, extra_title in spec.get(
                'extras', []):
            _extra(folder, folder_name, suffix, extra_kind, extra_title,
                   tpl, root, entries, owner=title,
                   owner_stem=f'{title} ({year})')
    return entries


def _tier_for_year(year):
    if year < 1990:
        return 'v480'
    if year < 2005:
        return 'v720'
    return 'v1080'


def _embedded_audio(ffmpeg, target, tpl):
    out = target.with_name(target.stem + '.tracks.mkv')
    _mux(ffmpeg, out, target,
         audio=[('eng', 'English 5.1', tpl['audio_51']),
                ('jpn', 'Japanese', tpl['audio_jpn']),
                ('eng', 'Commentary with the director',
                 tpl['audio_commentary'])],
         subs=[('eng', 'English', tpl['srt'], False),
               ('jpn', 'Japanese (styled)', tpl['ass'], False),
               ('eng', 'English (forced)', tpl['forced'], True)])
    out.replace(target)
    return target


def _multi_sidecars(folder, stem):
    _srt_sidecar(folder / f'{stem}.en.srt', _sub_en())
    _srt_sidecar(folder / f'{stem}.en.forced.srt', _sub_forced())
    _srt_sidecar(folder / f'{stem}.en.sdh.srt', _sub_sdh())
    _write(folder / f'{stem}.ja.ass', _sub_ass())


def _sub_en():
    return ('1\n00:00:00,500 --> 00:00:03,000\nAn original English sidecar '
            'line.\n\n'
            '2\n00:00:03,500 --> 00:00:07,000\nMapped to generated picture '
            'and tone.\n\n'
            '3\n00:00:08,000 --> 00:00:12,000\nThird line for playback '
            'checks.\n\n'
            '4\n00:00:12,500 --> 00:00:15,500\nFinal line, no external '
            'content.\n')


def _sub_forced():
    return ('1\n00:00:03,000 --> 00:00:05,000\n[Speaking Japanese] '
            'この台詞は架空です。\n\n'
            '2\n00:00:09,000 --> 00:00:11,500\n[Speaking Arabic] '
            'همس في الممر\n')


def _sub_sdh():
    return ('1\n00:00:01,000 --> 00:00:03,000\n[door closes]\n\n'
            '2\n00:00:04,000 --> 00:00:07,000\nNARRATOR: An invented line of '
            'dialogue.\n\n'
            '3\n00:00:10,000 --> 00:00:12,000\n[a low tone hums]\n')


def _sub_ass():
    return ('[Script Info]\nScriptType: v4.00+\nPlayResX: 1280\n'
            'PlayResY: 720\nWrapStyle: 0\nScaledBorderAndShadow: yes\n\n'
            '[V4+ Styles]\n'
            'Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour,'
            ' OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, '
            'ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, '
            'Alignment, MarginL, MarginR, MarginV, Encoding\n'
            'Style: Default,DejaVu Sans,34,&H00FFFFFF,&H000000FF,'
            '&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,2,0,2,30,30,30,1\n'
            'Style: Sign,DejaVu Sans,42,&H0000FFFF,&H000000FF,&H00000000,'
            '&H80000000,-1,0,0,0,100,100,0,0,1,3,1,8,30,30,30,1\n\n'
            '[Events]\n'
            'Format: Layer, Start, End, Style, Name, MarginL, MarginR, '
            'MarginV, Effect, Text\n'
            'Dialogue: 0,0:00:00.50,0:00:03.00,Default,,0,0,0,,'
            '{\\pos(640,620)}これは合成された日本語字幕です。\n'
            'Dialogue: 0,0:00:04.00,0:00:07.00,Sign,,0,0,0,,'
            '{\\fad(200,200)}夜の港 ─ オリジナル映像\n'
            'Dialogue: 0,0:00:08.00,0:00:11.00,Default,,0,0,0,,'
            '{\\i1}An original styled ASS line.{\\i0}\n'
            'Dialogue: 0,0:00:12.00,0:00:15.00,Sign,,0,0,0,,'
            '{\\b1}合成字幕{\\b0}\n')


# ---------------------------------------------------------------------- TV

def _ep_title(show, season, episode, used):
    base = f'{_pick(ADJ, show, season, episode)} ' \
           f'{_pick(NOUN, show, episode, season)}'
    title = base
    n = 2
    while title in used:
        title = f'{base} {n}'
        n += 1
    used.add(title)
    return title


def _show_nfo(folder, show):
    nfo(folder / 'tvshow.nfo', 'tvshow', {
        'title': show['title'],
        'plot': ('%s. An original Portico synthetic series; every name and '
                 'picture is invented.' % show['premise'].capitalize()),
        'year': show['year'],
        'premiered': f'{show["year"]}-09-01',
        'mpaa': show.get('cert', 'TV-14'),
        'rating': round(5.4 + _h(show['title']) % 42 / 10, 1),
        'votes': 400 + _h(show['title'], 'v') % 400000,
        'genre': [GENRES[_h(show['title']) % len(GENRES)],
                  GENRES[_h(show['title'], 'g') % len(GENRES)]],
        'studio': _pick(STUDIOS, show['title'], 'studio'),
        'country': _pick(COUNTRIES, show['title'], 'country'),
        'status': 'Ended' if show['year'] < 2019 else 'Continuing',
        'tag': ['synthetic', 'television'],
        'actor': [{'name': _pick(NAMES, show['title'], 'a', i),
                   'role': _pick(NAMES, show['title'], 'r', i),
                   'order': i} for i in range(3)],
    })


def _episode_fields(show, season, episode, title, absolute=None):
    fields = {
        'title': title,
        'showtitle': show['title'],
        'plot': f'{show["title"]} · S{season:02d}E{episode:02d} — {title}. '
                'Original synthetic episode; generated picture and tone.',
        'aired': f'{show["year"] + max(0, season - 1)}-'
                 f'{1 + _h(show["title"], season, episode) % 12:02d}-'
                 f'{1 + _h(show["title"], episode, season) % 27:02d}',
        'rating': round(6.0 + _h(show['title'], season, episode) % 35 / 10, 1),
        'runtime': 2,
        'director': _pick(NAMES, show['title'], 'director', episode),
        'credits': _pick(NAMES, show['title'], 'writer', episode),
    }
    if absolute is None:
        fields['season'] = season
        fields['episode'] = episode
    else:
        fields['episode'] = absolute
        fields['absolute_number'] = absolute
    return fields


def _episode_nfo(path, show, season, episode, title, absolute=None):
    fields = _episode_fields(show, season, episode, title, absolute)
    nfo(path, 'episodedetails', fields)


def _multi_episode_nfo(path, show, season, first, last, titles):
    """Two episodedetails roots; the parser reads up to eight per file."""
    bodies = []
    for episode, title in zip(range(first, last + 1), titles):
        fields = _episode_fields(show, season, episode, title)
        root = ET.Element('episodedetails')
        for key, value in fields.items():
            for item in value if isinstance(value, list) else [value]:
                child = ET.SubElement(root, key)
                if isinstance(item, dict):
                    for k, val in item.items():
                        ET.SubElement(child, k).text = str(val)
                else:
                    child.text = str(item)
        ET.indent(root)
        bodies.append(ET.tostring(root, encoding='unicode'))
    _write(path, '<?xml version="1.0" encoding="utf-8"?>\n'
                 + '\n'.join(bodies) + '\n')


def _episode_file(folder, stem, show, season, episode, tpl, ffmpeg, root,
                  entries, mode, chapters=False, absolute=None, still=False):
    title = _ep_title(show['title'], season, episode, show['_used'])
    features = ['episode', f'season:{season}', f'episode:{episode}']
    if absolute is not None:
        features.append(f'absolute_number:{absolute}')
    audio = []
    subs = []
    if mode == 'embedded':
        audio = [('eng', 'English', tpl['audio_eng']),
                 ('jpn', 'Japanese', tpl['audio_jpn']),
                 ('eng', 'Commentary', tpl['audio_commentary'])]
        subs = [('eng', 'English', tpl['srt'], False),
                ('jpn', 'Japanese (styled)', tpl['ass'], False),
                ('eng', 'English (forced)', tpl['forced'], True)]
        features += ['audio:eng', 'audio:jpn', 'audio:commentary',
                     'subtitle:embedded:srt',
                     'subtitle:embedded:ass-styled',
                     'subtitle:embedded:forced']
    elif mode == 'embedded_multi':
        audio = [('eng', 'English 5.1', tpl['audio_51']),
                 ('jpn', 'Japanese', tpl['audio_jpn'])]
        subs = [('eng', 'English', tpl['srt'], False),
                ('jpn', 'Japanese (styled)', tpl['ass'], False),
                ('eng', 'English (forced)', tpl['forced'], True)]
        features += ['audio:eng-5.1', 'audio:jpn',
                     'subtitle:embedded:srt',
                     'subtitle:embedded:ass-styled',
                     'subtitle:embedded:forced']
    chapter_rows = None
    if chapters:
        duration = _duration_seconds(ffmpeg, tpl['ep_video'])
        chapter_rows = _marker_rows(duration)
        features += ['chapters:markers', 'marker:recap', 'marker:intro',
                     'marker:credits', 'marker:outro']
    target = folder / (
        f'{stem}{".mkv" if (audio or subs or chapter_rows) else ".mp4"}')
    if audio or subs or chapter_rows:
        chapter_path = None
        if chapter_rows:
            chapter_path = folder / '.chapters.txt'
            _chapters(chapter_path, chapter_rows,
                      _duration_seconds(ffmpeg, tpl['ep_video']))
        _mux(ffmpeg, target, tpl['ep_video'], audio=audio, subs=subs,
             chapters=chapter_path)
        if chapter_path:
            chapter_path.unlink()
        features.append('container:mkv')
    else:
        copy_media(tpl['ep'], target)
        features += ['source:720p', 'container:mp4', 'video:h264',
                     'resolution:1280x720', 'audio:aac',
                     'transcode_source:light']
    if mode == 'sidecar_srt':
        _srt_sidecar(folder / f'{stem}.en.srt', _sub_en())
        features.append('subtitle:sidecar:srt')
    elif mode == 'sidecar_forced':
        _srt_sidecar(folder / f'{stem}.en.forced.srt', _sub_forced())
        features.append('subtitle:sidecar:forced')
    elif mode == 'sidecar_set':
        _srt_sidecar(folder / f'{stem}.en.srt', _sub_en())
        _srt_sidecar(folder / f'{stem}.en.forced.srt', _sub_forced())
        _srt_sidecar(folder / f'{stem}.en.sdh.srt', _sub_sdh())
        _write(folder / f'{stem}.ja.ass', _sub_ass())
        features += ['subtitle:sidecar:srt', 'subtitle:sidecar:forced',
                     'subtitle:sidecar:sdh',
                     'subtitle:sidecar:ass-styled']
    elif mode == 'ass_sidecar':
        subs_dir = folder / 'Subs'
        _write(subs_dir / f'{stem}.en.ass', _sub_ass())
        features.append('subtitle:sidecar-subs-folder:ass-styled')
    if still:
        art(folder / f'{stem}-thumb.jpg',
            f'{show["title"]} · S{season:02d}E{episode:02d}',
            (640, 360), _h(show['title'], season, episode) % 100000)
        features.append('artwork:episode-still')
    if absolute is None:
        _episode_nfo(folder / f'{stem}.nfo', show, season, episode, title)
    else:
        _episode_nfo(folder / f'{stem}.nfo', show, 1, absolute, title,
                     absolute=absolute)
    entries.append({
        'kind': 'episode',
        'title': f'{show["title"]} S{season:02d}E{episode:02d}',
        'path': target.relative_to(root).as_posix(),
        'features': features})
    return target, title


def _shows(root, tpl, ffmpeg):
    entries = []
    tv = Path(root) / 'TV'
    for show in SHOWS:
        folder = tv / f'{show["title"]} ({show["year"]})'
        folder.mkdir(parents=True, exist_ok=True)
        _show_nfo(folder, show)
        if show['title'] not in {'Static Garden', 'November Rain'}:
            _art_set(folder, show['title'], _h(show['title']) % 100000)
        entries.append({'kind': 'show', 'title': show['title'],
                        'path': folder.relative_to(root).as_posix(),
                        'features': ['series',
                                     f'seasons:{len(show["seasons"])}',
                                     f'certification:{show["cert"]}']})
    for show in SHOWS:
        show = dict(show)
        show['_used'] = set()
        folder = tv / f'{show["title"]} ({show["year"]})'
        for season, count in sorted(show['seasons'].items()):
            season_dir = folder / f'Season {season:02d}'
            season_dir.mkdir(parents=True, exist_ok=True)
            art(folder / f'season{season:02d}.jpg',
                f'{show["title"]} · Season {season}', (600, 900),
                _h(show['title'], season, 'art') % 100000)
            entries.append({'kind': 'season',
                            'title': f'{show["title"]} · Season {season}',
                            'path': season_dir.relative_to(root).as_posix(),
                            'features': [f'season:{season}',
                                         f'episodes:{count}']})
            linked_episodes = set()
            for episode in range(1, count + 1):
                if (season, episode) in show.get('missing', []):
                    continue
                if episode in linked_episodes:
                    continue
                multi = next((m for m in show.get('multi', [])
                              if m[0] == season and m[1] == episode), None)
                mode = show.get('subtitles', {}).get((season, episode), 'plain')
                stem = f'{show["title"]} - s{season:02d}e{episode:02d}'
                if multi:
                    stem = (f'{show["title"]} - s{season:02d}e{episode:02d}'
                            f'-e{multi[2]:02d}')
                chapters = bool(show.get('chapters'))
                still = bool(show.get('stills'))
                if multi:
                    target, first_title = _episode_file(
                        season_dir, stem, show, season, episode, tpl, ffmpeg,
                        root, entries, mode, chapters=True, still=still)
                    titles = [first_title]
                    for linked in range(episode + 1, multi[2] + 1):
                        linked_episodes.add(linked)
                        titles.append(_ep_title(show['title'], season, linked,
                                                show['_used']))
                        entries.append({
                            'kind': 'episode',
                            'title': f'{show["title"]} '
                                     f'S{season:02d}E{linked:02d}',
                            'path': target.relative_to(root).as_posix(),
                            'features': ['episode', f'season:{season}',
                                         f'episode:{linked}',
                                         'multi_episode_file',
                                         f'file_episode:{episode}']})
                    _multi_episode_nfo(season_dir / f'{stem}.nfo', show,
                                       season, episode, multi[2], titles)
                    continue
                _episode_file(season_dir, stem, show, season, episode, tpl,
                              ffmpeg, root, entries, mode, chapters=chapters,
                              still=still)
        if show.get('specials'):
            special_dir = folder / 'Specials'
            special_dir.mkdir(parents=True, exist_ok=True)
            art(folder / 'season-specials-poster.jpg',
                f'{show["title"]} · Specials', (600, 900),
                _h(show['title'], 'specials', 'art') % 100000)
            entries.append({'kind': 'season',
                            'title': f'{show["title"]} · Specials',
                            'path': special_dir.relative_to(root).as_posix(),
                            'features': ['season:0',
                                         f'episodes:{show["specials"]}']})
            for episode in range(1, show['specials'] + 1):
                stem = f'{show["title"]} - s00e{episode:02d}'
                _episode_file(special_dir, stem, show, 0, episode, tpl,
                              ffmpeg, root, entries, 'plain')
        for folder_name, suffix, extra_kind, extra_title in show.get(
                'extras', []):
            _extra(folder, folder_name, suffix, extra_kind, extra_title, tpl,
                   root, entries, owner=show['title'],
                   owner_stem=show['title'])
    return entries


def _anime(root, tpl, ffmpeg):
    entries = []
    anime = Path(root) / 'Anime'
    for show in ANIME:
        show = dict(show)
        show['_used'] = set()
        folder = anime / f'{show["title"]} ({show["year"]})'
        folder.mkdir(parents=True, exist_ok=True)
        _show_nfo(folder, show)
        _art_set(folder, show['title'], _h(show['title']) % 100000)
        entries.append({'kind': 'show', 'title': show['title'],
                        'path': folder.relative_to(root).as_posix(),
                        'features': ['series', 'anime', 'absolute_numbering',
                                     f'episodes:{show["episodes"]}']})
        for episode in range(1, show['episodes'] + 1):
            mode = 'plain'
            if episode in (1, 5, 12):
                mode = 'embedded'
            if episode == 7:
                mode = 'sidecar_set'
            stem = f'{show["title"]} - {episode:03d}'
            _episode_file(folder, stem, show, 1, episode, tpl, ffmpeg, root,
                          entries, mode, absolute=episode)
    return entries


# -------------------------------------------------------------------- extras

def _extra(folder, folder_name, suffix, extra_kind, extra_title, tpl, root,
           entries, owner, owner_stem):
    if folder_name:
        target_dir = folder / folder_name
        target_dir.mkdir(parents=True, exist_ok=True)
        target = target_dir / f'{extra_title}{suffix}.mp4'
    else:
        target = folder / f'{owner_stem}{suffix}.mp4'
    copy_media(tpl['extra'], target)
    entries.append({'kind': 'extra',
                    'title': extra_title if folder_name else owner_stem,
                    'path': target.relative_to(root).as_posix(),
                    'features': [f'extra:{extra_kind}', f'owner:{owner}',
                                 'short', 'container:mp4', 'video:h264',
                                 'resolution:854x480', 'audio:aac']})
    return target


# --------------------------------------------------------------------- disc

def _discs(root):
    entries = []
    if not DISC_SOURCE.exists():
        return entries
    target = Path(root) / 'Movies' / 'Chapters of the Deep (2004)'
    if not (target / 'VIDEO_TS').exists():
        shutil.copytree(DISC_SOURCE, target, dirs_exist_ok=True)
    _art_set(target, 'Chapters of the Deep', 424242, poster=True,
             backdrop=False, logo=False)
    entries.append({
        'kind': 'disc', 'title': 'Chapters of the Deep',
        'path': target.relative_to(root).as_posix(),
        'features': ['disc_layout:dvd', 'packaging:folder',
                     'container:VIDEO_TS', 'video:mpeg2',
                     'audio:ac3', 'title_count:2',
                     'chapters:2_per_title',
                     'source:fixtures/discs/dvd-two-title-video-ts',
                     'requires:bwrap', 'true_disc_layout',
                     'not_a_plain_file', 'distinct_from:chaptered_file']})
    return entries


# ------------------------------------------------------------------- public

def expected_counts(entries):
    """Catalogue item counts implied by file-level manifest entries."""
    counts = {}
    for entry in entries:
        kind = entry['kind']
        if kind in ('movie', 'show', 'disc'):
            counts.setdefault(kind, set()).add(entry['title'])
        elif kind == 'season':
            counts.setdefault('season', set()).add(entry['path'])
        elif kind == 'episode':
            counts['episode'] = counts.get('episode', 0) + 1
        else:
            counts[kind] = counts.get(kind, 0) + 1
    return {key: (len(value) if isinstance(value, set) else value)
            for key, value in counts.items()}


def _disk_bytes(root, entries):
    seen = set()
    total = 0
    for entry in entries:
        path = Path(root) / entry['path']
        if path.is_file():
            candidates = [path]
        elif path.exists():
            candidates = [item for item in path.rglob('*') if item.is_file()]
        else:
            candidates = []
        for item in candidates:
            try:
                stat = item.stat()
            except OSError:
                continue
            if stat.st_ino in seen:
                continue
            seen.add(stat.st_ino)
            total += stat.st_size
    return total


def generate(root: Path, ffmpeg: str, full: bool = True) -> list[dict]:
    root = Path(root)
    root.mkdir(parents=True, exist_ok=True)
    tpl = _templates(root, ffmpeg, full)
    entries = []
    entries += _films(root, tpl, ffmpeg, full)
    entries += _shows(root, tpl, ffmpeg)
    entries += _anime(root, tpl, ffmpeg)
    entries += _discs(root)
    if tpl['dv'] is None:
        print('video: Dolby Vision item skipped; this FFmpeg has no verified '
              'dovi_profile signalling (fixtures/video/hdr-generated/'
              'dv-8.1.hevc remains an uncontainered RPU stream).', flush=True)
    size = _disk_bytes(root, entries)
    counts = expected_counts(entries)
    print('video: %d files, %d items %s, %.1f MiB unique inodes'
          % (len(entries), sum(counts.values()), counts, size / 1048576),
          flush=True)
    return entries


if __name__ == '__main__':
    import sys
    generate(Path(sys.argv[1]), sys.argv[2] if len(sys.argv) > 2 else 'ffmpeg')
