#!/usr/bin/env python3
"""Synthetic music and audiobook fixtures for the Portico test server.

Two clear library sections are produced under ``root``:

    Music/       music library       (kind "music")
    Audiobooks/  audiobook library   (kind "audiobook")

Everything is original and deterministic: fictional artists, albums, books and
narrators; short tremolo-tone recordings made by FFmpeg; geometric artwork drawn
by ``common.art``. No remote metadata, artwork or fonts are used.

Generated media is tiny by design (1.6-2.4 s songs, 6 s audiobook chapters) and
lands under the parent's current generation root
``/home/runner/portico-test-library-v3``. Generated media is never committed;
only this generator is.

Shape of the set
----------------
Music
  40 named artists (38 regular, "Various Artists", "The Quarry Session");
  a 41st, "Unknown artist", appears only because the gap album is untagged.
  120 tagged albums, 1500 tagged songs, released 1972-2023.
  A few extra folders deliberately hold gaps: one packed album read through a
  cue sheet (12 virtual tracks from one FLAC) and one untagged album under
  "Unknown Artist" (album and artist come from the path only).

Audiobooks
  15 books by 8 authors: single-file M4B with embedded chapters, multi-file
  MP3 books (with a cue sheet, a book.nfo or folder-only evidence) and one
  two-disc book. Series, series positions, narrators (one book has two),
  descriptions and identifiers are present.

How the tags were chosen
------------------------
Every descriptive field written here is one the server actually imports
(server/internal/localmetadata/{audio,evidence,sidecars,audio_evidence_ingestion}.go,
server/internal/assets/audio_tags.go, server/internal/catalog/audio_repository.go):

  music      title, artist, album_artist, album, track, disc, date, genre,
             compilation, composer, the *_sort tags, and the "artists" JSON
             array from a ``<file>.portico.json`` sidecar for true multi-credit
             featured tracks.
  audiobook  title, album, artist, album_artist, author, narrator, track, disc,
             date, genre, description/comment, and series/series_position from
             either tags, metadata.opf, metadata.json, book.nfo or the folders.

Local artwork locations follow ``localmetadata.sidecars``: ``cover.jpg`` in an
album folder, ``artist.jpg``/``backdrop.jpg`` in the artist folder (music) and
``cover.jpg`` (book) beside an audiobook. The current analysis path collects
sidecar art for movies, TV, anime and music only, so an audiobook's
``artist.jpg`` (author portrait) and the generated ``narrator.jpg`` sit beside
the books as original art but do not import yet: recorded gaps, not fixture
omissions.

``generate`` returns one manifest entry ``{kind, title, path, features}`` per
playable item (``song`` or audiobook ``part``), with ``path`` POSIX-style and
relative to ``root``, so the per-library expected count is the number of
entries under that library's path. Album and book facts are repeated on each of
their tracks; ``expected_counts`` totals them. The optional ``full=False`` mode
writes a tiny slice for quick checks.
"""
from pathlib import Path
import hashlib
import json
import re
import shutil
import sys
import xml.etree.ElementTree as ET

HERE = Path(__file__).resolve().parent
if str(HERE) not in sys.path:
    sys.path.insert(0, str(HERE))
import common  # noqa: E402  (sibling helper, resolved at import time)


# --------------------------------------------------------------------------
# Library declaration: the state helper creates one library per entry.
# --------------------------------------------------------------------------
LIBRARIES = [
    {
        "name": "Design Test Music",
        "kind": "music",
        "path": "Music",
        "artists": 41,  # 40 named artists plus "Unknown artist" from the gap album
        "albums": 122,
        "songs": 1516,  # 1500 tagged + 4 untagged + 12 cue-sheet virtual tracks
    },
    {
        "name": "Design Test Audiobooks",
        "kind": "audiobook",
        "path": "Audiobooks",
        "books": 15,
        "authors": 8,
        "parts": 46,  # 12 single-file books + 34 multi-file parts
    },
]


# --------------------------------------------------------------------------
# Fictional catalogue data
# --------------------------------------------------------------------------
ARTISTS = [
    "Marisol Vane",
    "The Lantern Keepers",
    "Oren Pike",
    "Halcyon Drift",
    "Juniper Ash",
    "Sable & Stone",
    "Neve Calder",
    "The Quiet Meridian",
    "Cassian Wolde",
    "Ember Lune",
    "The Paper Lanterns",
    "Rookery",
    "Tilda Marsh",
    "Vellum Choir",
    "Ansel Grey",
    "Morrow Field",
    "The Cobalt Hours",
    "Yuki Sorensen",
    "Dovi Aren",
    "Harbour Signal",
    "Priya Raman",
    "Static Orchard",
    "Magnus Halo",
    "The Salt Compass",
    "Esme Wilder",
    "Kite & Compass",
    "Nadia Rahim",
    "Café Marisol",
    "The Long Room",
    "Ivo Tarrant",
    "Blue Verdant",
    "The Stillwater Band",
    "Odile Marchand",
    "Fennec Radio",
    "The Analog Coast",
    "Teodora Lark",
    "Ninefold",
    "Wander & Co.",
]

GENRES = [
    "Art Rock",
    "Ambient",
    "Indie Folk",
    "Post-Rock",
    "Jazz",
    "Electronic",
    "Chamber Pop",
    "Dream Pop",
    "Americana",
    "Modern Classical",
    "Synthwave",
    "Soul",
    "Trip Hop",
    "Bluegrass",
    "Shoegaze",
    "Krautrock",
]

ADJECTIVES = [
    "Quiet", "Copper", "Distant", "Violet", "Northern", "Paper", "Restless",
    "Amber", "Slow", "Hollow", "Marble", "Late", "Wandering", "Bright",
    "Winter", "Salt", "Velvet", "Open", "Long", "Small",
]

NOUNS = [
    "Lantern", "Harbour", "Meridian", "Orchard", "Signal", "Vellum", "Ember",
    "Marble", "Tunnel", "Glass", "Copper", "Prairie", "Neon", "Raven",
    "Cobalt", "Quarry", "Meadow", "Circuit", "Anchor", "Compass", "Ledger",
    "Chapel", "Furnace", "Delta", "Rooftop", "Basement", "Corridor", "Kite",
    "Almanac", "Foundry", "Pilot", "Borrowed Room", "Weather", "Crossing",
    "Assembly", "Aperture", "Foghorn", "Carpenter", "Sundial", "Telegram",
]

TRACK_NOUNS = [
    "Harbour", "Wire", "Window", "Field", "Engine", "Meadow", "Ticket",
    "Statue", "Anchor", "Letter", "Mirror", "Copper", "Bicycle", "Avenue",
    "Radio", "Orchard", "Summer", "Cathedral", "Rowboat", "Paper",
]

TRACK_VERBS = [
    "Waiting", "Drifting", "Burning", "Leaving", "Turning", "Calling",
    "Falling", "Walking", "Listening", "Remembering",
]

ALBUM_TITLE_OVERRIDES = {
    0: "The Lantern Hours",
    33: "Static Bloom",
    50: "Half Light, Half Signal",
    79: "ليل المدينة",
    81: "Saudade em Dezembro",
    114: "Now That's Synthetic, Vol. 1",
    115: "Late Night Signals: A Portico Compilation",
    116: "The Quarry Tapes",
    117: "Winter Assembly",
    118: "Studio Sessions, Volume Two",
    119: "Songs for Empty Rooms",
}

# Album ordinals with particular shapes. Ordinals are artist-major:
# ordinal = artist_index * 3 + album_index for the 38 regular artists.
MULTIDISC = {0, 13, 27, 39, 52, 63, 76, 89, 102, 113, 117}
HIRES = {7, 41, 68, 95}
EMBED_ART = {9: "flac", 42: "mp3", 66: "m4a"}
ALBUM_NFO = {0, 45, 60}
LRC_ALBUM = {0, 27, 48, 69}
NO_COVER = {12, 75, 119}
NO_YEAR = {15, 96}
NO_GENRE = {18, 84}
NO_TRACK_TAGS = {21, 108}
FEATURED_JSON = {24, 69}
FEATURED_FEAT = {24, 51}
LONG_TRACKS = {27}
SORT_TAGS = {3, 30, 57, 88}
GAPLESS = {6, 54}
MIXED = {50}
BACKDROP = {2, 15, 34, 57, 80, 97, 110, 116}
NO_ARTIST_IMAGE = {12, 25}

# Quarter-tone-free, low-volume original signals. 1.6-2.4 s is enough for
# browsing and keeps 1500 tracks well under 200 MB; the one longer playback
# album exists only so seek and progress have a real duration to work with.
DURATION = 1.6
DURATION_HIRES = 2.4
DURATION_LOSSY = 2.0
DURATION_LONG = 10.0
CHAPTER_SECONDS = 6.0
PART_SECONDS = 16.0


def _digest(*parts):
    return hashlib.sha256("|".join(str(p) for p in parts).encode()).digest()


def _pick(items, *seed):
    return items[int.from_bytes(_digest(*seed)[:2], "big") % len(items)]


def _slug(text, limit=90):
    """A file-system-safe stem; keeps Unicode and right-to-left titles."""
    text = str(text).replace("/", "-").replace("\\", "-").replace(":", " -")
    text = re.sub(r"[\x00-\x1f\x7f]", "", text)
    text = re.sub(r"\s+", " ", text).strip()
    return text[:limit].strip(" .") or "Untitled"


def _album_title(ordinal, artist):
    if ordinal in ALBUM_TITLE_OVERRIDES:
        return ALBUM_TITLE_OVERRIDES[ordinal]
    pattern = int.from_bytes(_digest("album", ordinal)[:1], "big") % 6
    if pattern == 0:
        return f"The {_pick(ADJECTIVES, ordinal, 0)} {_pick(NOUNS, ordinal, 1)}"
    if pattern == 1:
        return f"{_pick(NOUNS, ordinal, 2)} of {_pick(NOUNS, ordinal, 3)}"
    if pattern == 2:
        return f"{_pick(ADJECTIVES, ordinal, 4)} {_pick(NOUNS, ordinal, 5)}"
    if pattern == 3:
        return f"Notes from the {_pick(NOUNS, ordinal, 6)}"
    if pattern == 4:
        return f"{_pick(NOUNS, ordinal, 7)} & {_pick(NOUNS, ordinal, 8)}"
    return f"Songs for {_pick(NOUNS, ordinal, 9)}"


ARABIC_TRACK_TITLES = [
    "باب المدينة",
    "صوت المطر",
    "آخر قطار",
    "ضوء بعيد",
]


def _track_title(ordinal, index, count):
    if ordinal == 79 and index < len(ARABIC_TRACK_TITLES):
        return ARABIC_TRACK_TITLES[index]
    pattern = int.from_bytes(_digest("track", ordinal, index)[:1], "big") % 5
    if pattern == 0:
        return f"{_pick(ADJECTIVES, ordinal, index, 0)} {_pick(TRACK_NOUNS, ordinal, index, 1)}"
    if pattern == 1:
        return f"{_pick(TRACK_VERBS, ordinal, index, 2)} the {_pick(TRACK_NOUNS, ordinal, index, 3)}"
    if pattern == 2:
        return f"The {_pick(TRACK_NOUNS, ordinal, index, 4)} {_pick(TRACK_VERBS, ordinal, index, 5)}"
    if pattern == 3:
        return f"{_pick(TRACK_NOUNS, ordinal, index, 6)} in {_pick(ADJECTIVES, ordinal, index, 7)} Light"
    return f"Small {_pick(TRACK_NOUNS, ordinal, index, 8)}"


def _album_tracks(ordinal, album_title, count):
    """Track numbers, titles and per-disc placement for one album."""
    discs = 2 if ordinal in MULTIDISC else 1
    if discs == 2:
        split = count // 2 + count % 2
        sizes = [split, count - split]
    else:
        sizes = [count]
    out = []
    used = set()
    for disc, size in enumerate(sizes, start=1):
        for track in range(1, size + 1):
            for attempt in range(40):
                title = _track_title(ordinal, len(out), count)
                if attempt:
                    title = f"{title} ({attempt + 1})"
                if title.lower() not in used:
                    used.add(title.lower())
                    break
            out.append({"disc": disc, "discs": discs, "track": track, "title": title})
    return out


def _format_for(ordinal):
    if ordinal in EMBED_ART:
        return EMBED_ART[ordinal]
    if ordinal in HIRES:
        return "flac96"
    if ordinal in MIXED:
        return "mixed"
    if ordinal in (26, 83):
        return "opus"
    if ordinal % 9 == 4:
        return "mp3"
    if ordinal % 11 == 5:
        return "m4a"
    return "flac"


def _compilation_formats(ordinal):
    if ordinal in (115, 118):
        return "mp3"
    return "flac"


def _music_plan(full=True):
    plan = []
    used_titles = set()
    for artist_index, artist in enumerate(ARTISTS):
        for album_index in range(3):
            ordinal = artist_index * 3 + album_index
            title = _album_title(ordinal, artist)
            if title.lower() in used_titles:
                title = f"{title} II"
            used_titles.add(title.lower())
            plan.append({
                "ordinal": ordinal,
                "artist": artist,
                "title": title,
                "year": str(1972 + (ordinal * 5) % 52),
                "genre": GENRES[ordinal % len(GENRES)],
                "format": _format_for(ordinal),
                "count": 10 + (ordinal * 7) % 6,
                "compilation": False,
            })
    compilations = [
        ("Now That's Synthetic, Vol. 1", 1988, "Synthwave, Pop"),
        ("Late Night Signals: A Portico Compilation", 1994, "Trip Hop, Ambient"),
        ("The Quarry Tapes", 2001, "Post-Rock, Krautrock"),
        ("Winter Assembly", 2007, "Indie Folk, Chamber Pop"),
        ("Studio Sessions, Volume Two", 2013, "Soul, Jazz"),
        ("Songs for Empty Rooms", 2019, "Ambient, Modern Classical"),
    ]
    for offset, (title, year, genre) in enumerate(compilations):
        ordinal = 114 + offset
        plan.append({
            "ordinal": ordinal,
            "artist": "Various Artists",
            "title": title,
            "year": str(year),
            "genre": genre,
            "format": _compilation_formats(ordinal),
            "count": 10 + (ordinal * 7) % 6,
            "compilation": True,
        })
    if not full:
        plan = plan[:9]
    return plan


# --------------------------------------------------------------------------
# Media primitives
# --------------------------------------------------------------------------
class _Writer:
    """Templates, artwork and FFmpeg calls for one generation run."""

    def __init__(self, root, ffmpeg):
        self.root = root
        self.ffmpeg = str(ffmpeg)
        self.templates = root / ".audio-templates"
        self.templates.mkdir(parents=True, exist_ok=True)
        self.entries = []

    # -- templates ---------------------------------------------------------
    def _tone(self, name, seconds, codec, rate=44100, bitrate=None,
              frequency=330, filters="tremolo=f=3:d=0.5,volume=0.12"):
        path = self.templates / name
        if path.exists():
            return path
        args = [self.ffmpeg, "-y", "-f", "lavfi",
                "-i", f"sine=frequency={frequency}:sample_rate={rate}:d={seconds}"]
        args += ["-af", filters]
        if codec == "flac":
            args += ["-c:a", "flac"]
            if rate >= 88200:
                args += ["-sample_fmt", "s32"]
        elif codec == "mp3":
            args += ["-c:a", "libmp3lame", "-b:a", bitrate or "96k", "-ac", "1"]
        elif codec == "aac":
            args += ["-c:a", "aac", "-b:a", bitrate or "96k", "-ac", "1"]
        elif codec == "opus":
            args += ["-c:a", "libopus", "-b:a", bitrate or "64k", "-ac", "1"]
        else:
            raise ValueError(f"unknown tone codec: {codec}")
        args.append(path)
        common.run(args)
        return path

    def template(self, kind, seconds):
        """One shared tone template per codec and duration.

        ``flacgapless`` is a constant 440 Hz tone with no tremolo or fade. At
        1.6 s that is exactly 704 cycles, so the sample at the end of one track
        is the sample before the start of the next: a genuinely continuous
        album, not merely a set of tracks stored without gaps.
        """
        table = {
            "flac": ("flac44", "flac", "flac", 44100, None, {}),
            "flacgapless": ("flac44gapless", "flac", "flac", 44100, None,
                            {"frequency": 440, "filters": "volume=0.10"}),
            "flac96": ("flac96", "flac", "flac", 96000, None, {}),
            "mp3": ("mp3", "mp3", "mp3", 44100, "320k", {}),
            "m4a": ("aac", "m4a", "aac", 44100, "96k", {}),
            "opus": ("opus", "opus", "opus", 48000, "64k", {}),
            "mp3part": ("mp3part", "mp3", "mp3", 44100, "64k", {}),
        }
        label, ext, codec, rate, bitrate, options = table[kind]
        name = f"tone-{label}-{seconds:g}.{ext}"
        return self._tone(name, seconds, codec, rate=rate, bitrate=bitrate,
                          **options)

    # -- tags --------------------------------------------------------------
    @staticmethod
    def _ffmeta_text(tags, chapters=None):
        def esc(value):
            text = str(value)
            for char in ("\\", "=", ";", "#", "\n"):
                text = text.replace(char, "\\" + char)
            return text

        lines = [";FFMETADATA1"]
        for key, value in tags.items():
            if value not in (None, ""):
                lines.append(f"{key}={esc(value)}")
        for chapter in chapters or ():
            lines.append("[CHAPTER]")
            lines.append("TIMEBASE=1/1000")
            lines.append(f"START={chapter['start']}")
            lines.append(f"END={chapter['end']}")
            lines.append(f"title={esc(chapter['title'])}")
        return "\n".join(lines) + "\n"

    def retag(self, template, dst, tags, cover=None, path_key=None):
        """Write one file by copying the template's audio and its real tags."""
        dst = Path(dst)
        if dst.exists() and dst.stat().st_size:
            return dst
        dst.parent.mkdir(parents=True, exist_ok=True)
        args = [self.ffmpeg, "-y", "-i", str(template)]
        if cover:
            args += ["-i", str(cover)]
        args += ["-map", "0:a"]
        if cover:
            args += ["-map", "1:v", "-c:v", "copy", "-disposition:v", "attached_pic"]
        args += ["-map_metadata", "-1", "-c:a", "copy"]
        for key, value in tags.items():
            if value not in (None, ""):
                args += ["-metadata", f"{key}={value}"]
        args.append(str(dst))
        common.run(args)
        return dst

    # -- artwork -----------------------------------------------------------
    @staticmethod
    def image(path, title, size, seed, transparent=False):
        path = Path(path)
        if path.exists():
            return path
        common.art(path, title, size=size, seed=seed, transparent=transparent)
        return path

    # -- sidecars ----------------------------------------------------------
    @staticmethod
    def lrc(path, artist, album, title, seconds, seed):
        path = Path(path)
        if path.exists():
            return
        lines = [
            f"[ar:{artist}]",
            f"[ti:{title}]",
            f"[al:{album}]",
            "[by:Portico synthetic fixtures]",
        ]
        words = [
            f"{_pick(ADJECTIVES, seed, 0)} {_pick(TRACK_NOUNS, seed, 1)} on the {_pick(NOUNS, seed, 2)}",
            f"We {_pick(TRACK_VERBS, seed, 3).lower()} through the {_pick(NOUNS, seed, 4)}",
            f"Every {_pick(TRACK_NOUNS, seed, 5)} is a {_pick(ADJECTIVES, seed, 6).lower()} door",
            f"And the {_pick(NOUNS, seed, 7)} keeps {_pick(TRACK_VERBS, seed, 8).lower()}",
        ]
        stamps = [0.20, 0.65, 1.10, max(0.3, seconds - 0.35)]
        for stamp, word in zip(stamps, words):
            lines.append(f"[{int(stamp // 60):02d}:{stamp % 60:05.2f}]{word}")
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")

    @staticmethod
    def portico_json(path, payload):
        Path(path).write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")

    @staticmethod
    def cue(path, title, performer, file_name, tracks, track_seconds):
        """A cue sheet; times are mm:ss:ff at 75 frames a second."""
        def stamp(seconds):
            minutes, rest = divmod(seconds, 60)
            return f"{int(minutes):02d}:{int(rest):02d}:{int(round((rest % 1) * 75)):02d}"

        lines = [f'PERFORMER "{performer}"', f'TITLE "{title}"',
                 f'FILE "{file_name}" WAVE']
        for index, name in enumerate(tracks, start=1):
            lines += [f"  TRACK {index:02d} AUDIO", f'    TITLE "{name}"',
                      f"    INDEX 01 {stamp((index - 1) * track_seconds)}"]
        Path(path).write_text("\n".join(lines) + "\n", encoding="utf-8")


# --------------------------------------------------------------------------
# Music
# --------------------------------------------------------------------------
def _artist_images(writer, music, artist, artist_index, features):
    folder = music / artist
    if artist_index in NO_ARTIST_IMAGE:
        features.append("artist_without_image")
    else:
        writer.image(folder / "artist.jpg", artist, (600, 900), f"artist-{artist}")
    if artist_index in BACKDROP:
        writer.image(folder / "backdrop.jpg", f"{artist} live", (1280, 720),
                     f"backdrop-{artist}")


def _write_album(writer, music, album, artist_index):
    ordinal = album["ordinal"]
    artist = album["artist"]
    title = album["title"]
    year = album["year"]
    genre = album["genre"]
    fmt = album["format"]
    features = []

    folder = music / artist / _slug(title)
    common_cover = folder / "cover.jpg"
    cover = None
    if ordinal not in NO_COVER and ordinal not in EMBED_ART:
        cover = writer.image(common_cover, f"{title} · {artist}", (700, 700),
                             f"album-{ordinal}")
    if ordinal in EMBED_ART:
        cover = writer.image(writer.templates / f"embed-{ordinal}.jpg",
                             f"{title} · {artist}", (700, 700), f"album-{ordinal}")
        features.append(f"artwork_embedded_{fmt}")
    if ordinal in NO_COVER:
        features.append("no_cover_art")
        if (folder / "cover.jpg").exists():
            (folder / "cover.jpg").unlink()

    if ordinal in BACKDROP:
        writer.image(folder / "backdrop.jpg", title, (1280, 720), f"album-art-{ordinal}")
        features.append("album_backdrop")

    if ordinal in ALBUM_NFO:
        common.nfo(folder / "album.nfo", "album", {
            "title": title, "artist": artist, "year": year, "album": title,
        })
        features.append("album_nfo")

    if ordinal in MULTIDISC:
        features.append("multi_disc")
    if ordinal in HIRES:
        features.append("flac_hi_res")
    if fmt == "mp3":
        features.append("mp3_320")
    elif fmt == "m4a":
        features.append("aac_lossy")
    elif fmt == "opus":
        features.append("opus")
    elif fmt == "mixed":
        features.append("mixed_formats_album")
    if ordinal in GAPLESS:
        features.append("gapless_album")
        features.append("continuous_integer_cycle_waveform")
    if ordinal in LONG_TRACKS:
        features.append("long_tracks")
    if ordinal in SORT_TAGS:
        features.append("sort_tags")
    if ordinal in NO_YEAR:
        features.append("no_year")
    if ordinal in NO_GENRE:
        features.append("no_genre")
    if ordinal in NO_TRACK_TAGS:
        features.append("track_number_from_filename")
    if ordinal in LRC_ALBUM:
        features.append("synced_lyrics")
    if ordinal in FEATURED_JSON:
        features.append("featured_multi_credit")
    if ordinal in FEATURED_FEAT:
        features.append("featured_credit")
    if album["compilation"]:
        features.append("compilation_various_artists")
    if ordinal == 79:
        features.append("rtl_titles")
    if ordinal == 81:
        features.append("unicode_titles")

    tracks = _album_tracks(ordinal, title, album["count"])
    if ordinal in LONG_TRACKS:
        seconds = DURATION_LONG
    elif ordinal in HIRES:
        seconds = DURATION_HIRES
    elif fmt in ("mp3", "m4a", "opus", "mixed"):
        seconds = DURATION_LOSSY
    else:
        seconds = DURATION

    for position, spec in enumerate(tracks):
        track_no = spec["track"]
        disc = spec["disc"]
        disc_folder = folder / f"Disc {disc}" if spec["discs"] > 1 else folder
        ext = {"flac": "flac", "flac96": "flac", "mp3": "mp3",
               "m4a": "m4a", "opus": "opus"}.get(fmt, "flac")
        if fmt == "mixed":
            ext = "mp3" if position % 4 == 2 else "flac"
        stem = f"{track_no:02d} - {_slug(spec['title'])}"
        dst = disc_folder / f"{stem}.{ext}"

        track_artist = artist
        if album["compilation"]:
            track_artist = ARTISTS[(ordinal * 5 + position * 3) % len(ARTISTS)]
        tags = {
            "title": spec["title"],
            "artist": track_artist,
            "album_artist": artist,
            "album": title,
            "track": "" if ordinal in NO_TRACK_TAGS else str(track_no),
            "disc": "" if ordinal in NO_TRACK_TAGS or spec["discs"] == 1 else str(disc),
            "date": "" if ordinal in NO_YEAR else year,
            "genre": "" if ordinal in NO_GENRE else genre,
        }
        if album["compilation"]:
            tags["compilation"] = "1"
        if ordinal in SORT_TAGS:
            tags["artist_sort"] = artist.replace("The ", "", 1).lower()
            tags["album_artist_sort"] = artist.replace("The ", "", 1).lower()
            tags["album_sort"] = title.lower()
            tags["title_sort"] = spec["title"].lower()
        if ordinal in GAPLESS:
            tags["genre"] = "Post-Rock, Ambient"
        guest = None
        if ordinal in FEATURED_JSON and position in (2, 7):
            guest = ARTISTS[(artist_index + 7) % len(ARTISTS)]
            writer.portico_json(dst.with_name(dst.name + ".portico.json"),
                                {"artists": [track_artist, guest]})
        elif ordinal in FEATURED_FEAT and position in (4, 9):
            guest = ARTISTS[(artist_index + 11) % len(ARTISTS)]
            tags["artist"] = f"{track_artist} feat. {guest}"

        if fmt == "mixed":
            template = writer.template("mp3" if ext == "mp3" else "flac", seconds)
        elif ordinal in GAPLESS:
            template = writer.template("flacgapless", seconds)
        else:
            template = writer.template(fmt, seconds)
        embedded = cover if ordinal in EMBED_ART else None
        writer.retag(template, dst, tags, cover=embedded)

        if ordinal in LRC_ALBUM:
            writer.lrc(dst.with_suffix(".lrc"), track_artist, title,
                       spec["title"], seconds, f"{ordinal}-{position}")

        # One manifest entry per catalogue item (song). Album-level facts are
        # repeated on each track they apply to, so a file entry is self-describing
        # and the per-library expected count is the number of songs.
        item_features = list(features) or ["standard_album"]
        item_features.append(f"album:{_slug(title)}")
        item_features.append(f"artist:{track_artist}")
        item_features.append(f"track:{track_no}")
        if spec["discs"] > 1:
            item_features.append(f"disc:{disc}/{spec['discs']}")
        item_features.append(f"format:{ext}")
        if ordinal in LRC_ALBUM:
            item_features.append("lyrics_lrc_sidecar")
        if ordinal in EMBED_ART:
            item_features.append("artwork_embedded")
        if guest is not None:
            item_features.append("featured_guest")
        writer.entries.append({
            "kind": "song",
            "title": spec["title"],
            "path": dst.relative_to(writer.root).as_posix(),
            "features": item_features,
        })


def _write_packed_album(writer, music):
    artist = "The Quarry Session"
    title = "Live at the Quarry"
    folder = music / artist / title
    tracks = [
        "First Light", "Low Tide", "The Long Walk", "Copper Wire", "Nightfall",
        "Second Set: Ember", "Quarry Floor", "Signal Fire", "The Slow Return",
        "Encore: Paper Lantern", "Last Train Home", "House Lights",
    ]
    seconds = 24.0
    writer.image(folder / "cover.jpg", f"{title} · {artist}", (700, 700), "album-packed")
    writer.image(music / artist / "artist.jpg", artist, (600, 900), "artist-packed")
    stem = _slug(title)
    writer.cue(folder / f"{stem}.cue", title, artist, f"{stem}.flac", tracks,
               seconds / len(tracks))
    writer.retag(writer.template("flac", seconds), folder / f"{stem}.flac", {
        "title": title, "album": title, "artist": artist, "album_artist": artist,
        "date": "2005", "genre": "Post-Rock, Live",
    })
    # The cue sheet publishes twelve virtual songs from this one physical file;
    # each is one catalogue item in the server, so each gets its own entry.
    for index, name in enumerate(tracks, start=1):
        writer.entries.append({
            "kind": "song",
            "title": name,
            "path": (Path("Music") / artist / title / f"{stem}.flac").as_posix(),
            "features": ["packed_album_single_flac", f"cue_virtual_track:{index}",
                         "no_embedded_track_tags", "format:flac"],
        })


def _write_untagged_album(writer, music):
    folder = music / "Unknown Artist" / "Untitled Sessions"
    names = ["Untitled One", "Untitled Two", "Untitled Three", "Untitled Four"]
    for index, name in enumerate(names, start=1):
        dst = folder / f"{index:02d} - {name}.flac"
        writer.retag(writer.template("flac", DURATION), dst, {})
        writer.entries.append({
            "kind": "song",
            "title": name,
            "path": dst.relative_to(writer.root).as_posix(),
            "features": ["no_tags", "identity_from_folders_only", "no_cover_art",
                         "format:flac"],
        })


def _write_music(writer, root, full):
    music = root / "Music"
    plan = _music_plan(full=full)
    written_artists = set()
    for album in plan:
        artist = album["artist"]
        artist_index = ARTISTS.index(artist) if artist in ARTISTS else 38
        if artist not in written_artists and artist != "Various Artists":
            _artist_images(writer, music, artist, artist_index, [])
            written_artists.add(artist)
        _write_album(writer, music, album, artist_index)
    if full:
        _write_packed_album(writer, music)
        _write_untagged_album(writer, music)
    return [entry for entry in writer.entries if entry["kind"] == "song"]


# --------------------------------------------------------------------------
# Audiobooks
# --------------------------------------------------------------------------
BOOKS = [
    {
        "author": "Tamsin Okafor", "title": "The Salt Road", "year": "2019",
        "genres": ["Literary Fiction"], "narrators": ["Eleanor Voss"],
        "layout": "m4b", "chapters": 9, "sidecar": "opf",
        "publisher": "Driftwood Audio", "language": "en",
        "description": "A surveyor follows an old salt route inland and finds the towns have moved.",
    },
    {
        "author": "Tamsin Okafor", "title": "Glass Harbor", "year": "2021",
        "genres": ["Literary Fiction", "Mystery"], "narrators": ["Eleanor Voss"],
        "layout": "m4b", "chapters": 6, "sidecar": "embedded",
        "cover": "folder.jpg",
        "description": "Three siblings return to a harbor town to settle a house nobody wants.",
    },
    {
        "author": "Perrin Voss", "title": "Nightjar", "year": "2017",
        "genres": ["Fantasy"], "narrators": ["Malik Chen"],
        "layout": "m4b", "chapters": 12, "sidecar": "abs",
        "publisher": "Rook & Ledger", "language": "en", "isbn": "9781000000017",
        "description": "A night courier carries letters between two cities that share one sky.",
    },
    {
        "author": "Perrin Voss", "title": "Field Notes from the Long Dark",
        "year": "2020", "genres": ["Science Fiction"],
        "narrators": ["Perrin Voss"], "layout": "parts", "parts": 14,
        "sidecar": "book_nfo",
        "description": "Notes from a research station through a winter without sunrise.",
    },
    {
        "author": "Ines Marchetti", "title": "The Paper Coast",
        "series": "The Cartographers", "position": "1", "year": "2014",
        "genres": ["Historical Fiction"], "narrators": ["Saoirse Bell"],
        "layout": "m4b", "chapters": 8, "sidecar": "opf",
        "series_folder": True,
        "publisher": "Vellum House", "language": "en",
        "description": "A mapmaker's apprentice is sent to chart a coast that keeps redrawing itself.",
    },
    {
        "author": "Ines Marchetti", "title": "The Glass Meridian",
        "series": "The Cartographers", "position": "2", "year": "2016",
        "genres": ["Historical Fiction"], "narrators": ["Saoirse Bell"],
        "layout": "m4b", "chapters": 7, "sidecar": "folder",
        "series_folder": True, "narrator_folder": True,
        "description": "The second survey crosses the meridian and loses a season.",
    },
    {
        "author": "Ines Marchetti", "title": "The Last Survey",
        "series": "The Cartographers", "position": "3", "year": "2018",
        "genres": ["Historical Fiction"], "narrators": ["Saoirse Bell"],
        "layout": "m4b", "chapters": 10, "sidecar": "portico_json",
        "series_folder": True,
        "description": "The last expedition closes the atlas, one page at a time.",
    },
    {
        "author": "Adaeze Nwosu", "title": "Undertow",
        "series": "The Tidewater Cycle", "position": "1", "year": "2012",
        "genres": ["Fantasy"], "narrators": ["June Adebayo"],
        "layout": "parts", "parts": 10, "sidecar": "cue",
        "series_folder": True,
        "description": "A river town bargains with the tide every hundred years.",
    },
    {
        "author": "Adaeze Nwosu", "title": "Ebb",
        "series": "The Tidewater Cycle", "position": "2", "year": "2015",
        "genres": ["Fantasy"], "narrators": ["June Adebayo"],
        "layout": "m4b", "chapters": 8, "sidecar": "embedded",
        "series_folder": True,
        "description": "The bargain comes due and the river remembers every name.",
    },
    {
        "author": "Hollis Grey", "title": "Quarry Light", "year": "2009",
        "genres": ["Memoir"], "narrators": ["Hollis Grey"],
        "layout": "discs", "discs": 2, "parts": 10, "sidecar": "none",
        "description": "A working quarry, a family, and forty years of stone.",
    },
    {
        "author": "Bram Solloway", "title": "Atlas of Small Weather",
        "year": "2022", "genres": ["Nature Writing"], "narrators": ["Nell Farrar"],
        "layout": "m4b", "chapters": 10, "sidecar": "desc_reader",
        "description": "Short reports on the weather of one valley, kept for a decade.",
    },
    {
        "author": "Ruth Halloran", "title": "The Long Quiet", "year": "2011",
        "genres": ["Literary Fiction"], "narrators": ["Ruth Halloran"],
        "layout": "m4b", "chapters": 7, "sidecar": "none", "no_cover": True,
        "description": "A caretaker spends a winter in a shuttered hotel.",
    },
    {
        "author": "Cyrus Adeyemi", "title": "A Map of Borrowed Rooms",
        "year": "2023", "genres": ["Literary Fiction"],
        "narrators": ["Cyrus Adeyemi", "Mira Sol"], "layout": "m4b",
        "chapters": 11, "sidecar": "opf_two_narrators",
        "publisher": "Second Door Audio", "language": "en",
        "description": "Two voices trace a life told through the rooms it passed through.",
    },
    {
        "author": "Tamsin Okafor", "title": "The Weight of Paper",
        "year": "2008", "genres": ["Literary Fiction"], "narrators": ["Eleanor Voss"],
        "layout": "m4b", "chapters": 5, "sidecar": "folder_asin",
        "asin": "B0SYNTH001", "asin_folder": True,
        "description": "A printer's daughter inherits an archive and a press that still runs.",
    },
    {
        "author": "Hollis Grey", "title": "Low Country Almanac",
        "year": "2013", "genres": ["Nature Writing", "Memoir"],
        "narrators": ["Hollis Grey"], "layout": "m4b", "chapters": 20,
        "sidecar": "none",
        "description": "A year of tides, birds and fieldwork on a low country estate.",
    },
]


def _book_folder(root, book, features):
    """Author/[Series/][Vol. N - ]Title[{Narrator}][ [ASIN]] as the importer reads it."""
    folder = root / "Audiobooks" / book["author"]
    if book.get("series_folder"):
        folder = folder / book["series"]
    title = book["title"]
    if book.get("series_folder"):
        title = f"Vol. {book['position']} - {title}"
    if book.get("narrator_folder"):
        title = f"{title} {{{book['narrators'][0]}}}"
        features.append("narrator_from_folder")
    if book.get("asin_folder"):
        title = f"{title} [{book['asin']}]"
        features.append("asin_from_folder")
    return folder / title


def _opf(path, book):
    package = ET.Element("package", {
        "xmlns": "http://www.idpf.org/2007/opf", "version": "2.0",
        "unique-identifier": "bookid",
    })
    metadata = ET.SubElement(package, "metadata", {
        "xmlns:dc": "http://purl.org/dc/elements/1.1/",
        "xmlns:opf": "http://www.idpf.org/2007/opf",
    })
    ET.SubElement(metadata, "dc:title").text = book["title"]
    ET.SubElement(metadata, "dc:language").text = book.get("language", "en")
    ET.SubElement(metadata, "dc:publisher").text = book.get("publisher", "Portico Audio")
    ET.SubElement(metadata, "dc:date").text = book["year"]
    ET.SubElement(metadata, "dc:description").text = book["description"]
    for author in [book["author"]]:
        creator = ET.SubElement(metadata, "dc:creator")
        creator.set("opf:role", "aut")
        creator.text = author
    for narrator in book["narrators"]:
        contributor = ET.SubElement(metadata, "dc:contributor")
        contributor.set("opf:role", "nrt")
        contributor.text = narrator
    ET.SubElement(metadata, "dc:identifier", {"id": "bookid"}).text = \
        f"urn:uuid:portico-{_slug(book['title']).lower().replace(' ', '-')}"
    if book.get("isbn"):
        ET.SubElement(metadata, "dc:identifier", {"opf:scheme": "ISBN"}).text = book["isbn"]
    if book.get("series"):
        meta = ET.SubElement(metadata, "meta", {
            "property": "belongs-to-collection", "id": "series1"})
        meta.text = book["series"]
        ET.SubElement(metadata, "meta", {
            "refines": "#series1", "property": "group-position"}).text = book["position"]
    path.parent.mkdir(parents=True, exist_ok=True)
    ET.indent(package)
    path.write_bytes(ET.tostring(package, encoding="utf-8", xml_declaration=True))


def _abs_json(path, book, chapters):
    payload = {
        "title": book["title"],
        "authors": [book["author"]],
        "narrators": book["narrators"],
        "series": [f"{book['series']} #{book['position']}"] if book.get("series") else [],
        "genres": book["genres"],
        "publishedYear": book["year"],
        "publisher": book.get("publisher", "Portico Audio"),
        "description": book["description"],
        "isbn": book.get("isbn", ""),
        "asin": book.get("asin", "B0SYNTH003"),
        "language": book.get("language", "en"),
        "chapters": chapters,
    }
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")


def _book_tags(book, section):
    tags = {
        "title": book["title"],
        "album": book["title"],
        "artist": book["author"],
        "album_artist": book["author"],
        "author": book["author"],
        "date": book["year"],
        "genre": ", ".join(book["genres"]),
        "description": book["description"],
    }
    if section.get("embedded_series"):
        tags["series"] = book["series"]
        tags["series_position"] = book["position"]
    return tags


def _write_m4b(writer, folder, book, features, sections):
    total = sections * CHAPTER_SECONDS
    chapter_seconds = total / sections
    chapters = []
    for index in range(sections):
        start = round(index * chapter_seconds, 2)
        end = round((index + 1) * chapter_seconds, 2)
        chapters.append({"start": int(start * 1000), "end": int(end * 1000),
                         "title": f"Chapter {index + 1}"})
    tags = _book_tags(book, {})
    if book["sidecar"] == "embedded":
        tags["composer"] = "; ".join(book["narrators"])
        features.append("narrator_in_composer")
    if book["sidecar"] == "folder":
        # Folder evidence only applies when the file does not name its album or
        # author, which is exactly the gap this book covers.
        tags = {"title": book["title"], "date": book["year"],
                "genre": ", ".join(book["genres"])}
        features.append("metadata_from_folders")
    if book["sidecar"] == "folder_asin":
        # No album or author in the file: the folder's ASIN and the author
        # folder become the book's evidence, as Audiobookshelf also reads them.
        tags = {"title": book["title"], "date": book["year"],
                "genre": ", ".join(book["genres"])}
        features.append("asin_from_folders")
    meta = writer.templates / f"book-{_slug(book['title'])}.txt"
    meta.write_text(writer._ffmeta_text(tags, chapters), encoding="utf-8")
    dst = folder / f"{_slug(book['title'])}.m4b"
    if not (dst.exists() and dst.stat().st_size):
        dst.parent.mkdir(parents=True, exist_ok=True)
        common.run([
            writer.ffmpeg, "-y",
            "-f", "lavfi", "-i",
            f"sine=frequency=190:sample_rate=44100:d={total + 0.6:.2f}",
            "-i", str(meta), "-map", "0:a", "-map_metadata", "1",
            "-af", "tremolo=f=0.4:d=0.2,volume=0.07",
            "-c:a", "aac", "-b:a", "48k", "-ac", "1",
            "-movflags", "+faststart", str(dst),
        ])
    features.append(f"m4b_chapters={sections}")
    return chapters, dst


def _write_parts(writer, folder, book, features, parts, disc_layout=False):
    writer.template("mp3part", PART_SECONDS)
    chapter_list = []
    section = 1
    discs = book.get("discs", 1)
    for disc in range(1, discs + 1):
        per_disc = parts // discs if disc_layout else parts
        for part in range(1, per_disc + 1):
            disc_folder = folder / f"Disc {disc}" if disc_layout else folder
            stem = f"{part:02d} - Part {part}"
            dst = disc_folder / f"{stem}.mp3"
            tags = _book_tags(book, {})
            tags.update({
                "title": f"Part {part}",
                "track": str(part),
                "disc": str(disc) if disc_layout else "",
                "narrator": "; ".join(book["narrators"]),
                "composer": "; ".join(book["narrators"]),
            })
            if book.get("series"):
                tags["series"] = book["series"]
                tags["series_position"] = book["position"]
            writer.retag(writer.template("mp3part", PART_SECONDS), dst, tags)
            chapter_list.append({"file": dst.name, "part": part, "disc": disc,
                                 "path": dst, "title": f"Part {part}"})
            section += 1
    if disc_layout:
        features.append(f"disc_folders={discs}")
    features.append(f"multifile_parts={parts}")
    return chapter_list


def _write_book_cue(folder, book, chapters):
    title = _slug(folder.name)
    lines = [f'PERFORMER "{book["author"]}"', f'TITLE "{book["title"]}"']
    for entry in chapters:
        lines.append(f'FILE "{entry["file"]}" MP3')
        for offset, label in enumerate(("Opening", "Second Half")):
            lines.append("  TRACK %02d AUDIO" % (offset + 1))
            lines.append(f'    TITLE "Part {entry["part"]}: {label}"')
            lines.append("    INDEX 01 %s" % ("00:00:00" if offset == 0 else "00:08:00"))
    (folder / f"{title}.cue").write_text("\n".join(lines) + "\n", encoding="utf-8")


def _write_audiobook(writer, root, book):
    features = []
    folder = _book_folder(root, book, features)
    if book.get("series"):
        features.append(f"series={book['series']}")
        features.append(f"series_position={book['position']}")
    if len(book["narrators"]) > 1:
        features.append("multiple_narrators")
    if book.get("no_cover"):
        features.append("no_cover_art")
    else:
        name = book.get("cover", "cover.jpg")
        writer.image(folder / name, f"{book['title']} · {book['author']}",
                     (800, 1200), f"book-{book['title']}")

    if book["layout"] == "m4b":
        chapters, media_path = _write_m4b(writer, folder, book, features, book["chapters"])
        items = [{"path": media_path, "title": book["title"], "disc": 0, "part": 0}]
    else:
        parts = book.get("parts", 10)
        items = _write_parts(writer, folder, book, features, parts,
                             disc_layout=book["layout"] == "discs")
        chapters = items

    sidecar = book["sidecar"]
    if sidecar in ("opf", "opf_two_narrators"):
        _opf(folder / "metadata.opf", book)
        features.append("metadata_opf")
    elif sidecar == "abs":
        _abs_json(folder / "metadata.json", book, [
            {"start": index * CHAPTER_SECONDS, "end": (index + 1) * CHAPTER_SECONDS,
             "title": f"Chapter {index + 1}"} for index in range(book["chapters"])
        ])
        features.append("metadata_json_chapters")
    elif sidecar == "book_nfo":
        common.nfo(folder / "book.nfo", "book", {
            "title": book["title"], "author": book["author"],
            "narrator": "; ".join(book["narrators"]), "year": book["year"],
            "plot": book["description"], "publisher": book.get("publisher", "Portico Audio"),
            "language": book.get("language", "en"),
        })
        features.append("book_nfo")
    elif sidecar == "portico_json":
        writer.portico_json(folder / f"{_slug(book['title'])}.m4b.portico.json", {
            "narrator": "; ".join(book["narrators"]),
            "series": book["series"], "series_position": book["position"],
        })
        features.append("portico_json_sidecar")
    elif sidecar == "folder_asin":
        writer.portico_json(folder / f"{_slug(book['title'])}.m4b.portico.json", {
            "narrator": "; ".join(book["narrators"]),
        })
        features.append("portico_json_sidecar")
    elif sidecar == "desc_reader":
        (folder / "desc.txt").write_text(book["description"] + "\n", encoding="utf-8")
        (folder / "reader.txt").write_text(
            "\n".join(book["narrators"]) + "\n", encoding="utf-8")
        features.append("desc_txt")
        features.append("reader_txt")
    elif sidecar == "cue":
        _write_book_cue(folder, book, chapters)
        features.append("cue_chapters")

    author_dir = root / "Audiobooks" / book["author"]
    if book.get("series_folder"):
        author_dir = author_dir / book["series"]
    writer.image(author_dir / "artist.jpg", book["author"], (600, 900),
                 f"author-{book['author']}")
    features.append("author_portrait_file")

    # Narrator portraits are generated original art beside the book. The server
    # has no narrator-artwork slot, and its analysis path does not read audiobook
    # sidecar art at all yet, so these files are supplied for the asset and walk
    # sets without claiming they import; see the module notes.
    narrators = [name for name in book["narrators"] if name != book["author"]]
    for index, narrator in enumerate(narrators, start=1):
        name = "narrator.jpg" if index == 1 else f"narrator-{index}.jpg"
        writer.image(folder / name, narrator, (600, 900), f"narrator-{narrator}")
    if narrators:
        features.append("narrator_portrait_file")

    # One entry per playable audiobook file (a book may be one M4B or many
    # parts), matching the item count the audiobook library will publish.
    for item in items:
        item_features = list(features) or ["standard_book"]
        item_features.append(f"book:{_slug(book['title'])}")
        item_features.append(f"format:{item['path'].suffix.lstrip('.')}")
        if item.get("part"):
            item_features.append(f"part:{item['part']}")
        if item.get("disc"):
            item_features.append(f"disc:{item['disc']}")
        writer.entries.append({
            "kind": "part",
            "title": item["title"],
            "path": item["path"].relative_to(root).as_posix(),
            "features": item_features,
        })


def expected_counts(entries):
    """Catalogue item counts implied by these file-level manifest entries."""
    counts = {}
    for entry in entries:
        counts[entry["kind"]] = counts.get(entry["kind"], 0) + 1
    return counts


def _write_audiobooks(writer, root, full):
    books = BOOKS if full else BOOKS[:2]
    for book in books:
        _write_audiobook(writer, root, book)
    return [entry for entry in writer.entries if entry["kind"] == "part"]


# --------------------------------------------------------------------------
# Entry point
# --------------------------------------------------------------------------
def generate(root: Path, ffmpeg: str, full: bool = True) -> list[dict]:
    """Write the music and audiobook sections under ``root``; return manifest.

    ``root`` is the test library root (the parent server uses
    /home/runner/portico-test-library-v3). ``ffmpeg`` is the bundled binary.
    """
    root = Path(root)
    root.mkdir(parents=True, exist_ok=True)
    writer = _Writer(root, ffmpeg)
    try:
        entries = _write_music(writer, root, full)
        entries += _write_audiobooks(writer, root, full)
    finally:
        shutil.rmtree(writer.templates, ignore_errors=True)
    return entries


def main():
    import argparse
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--out", type=Path,
                        help="test library root; Music/ and Audiobooks/ are created")
    parser.add_argument("--ffmpeg", default=shutil.which("ffmpeg") or "ffmpeg")
    parser.add_argument("--small", action="store_true",
                        help="write a tiny slice for a smoke check")
    parser.add_argument("--list", action="store_true",
                        help="print the planned manifest without generating")
    args = parser.parse_args()
    if args.list:
        print(json.dumps({"libraries": LIBRARIES,
                          "expected": {"music": 1516, "audiobook_parts": 46}},
                         indent=2))
        return
    if not args.out:
        parser.error("--out is required unless --list is used")
    entries = generate(args.out, args.ffmpeg, full=not args.small)
    print(f"wrote {len(entries)} entries under {args.out}")


if __name__ == "__main__":
    main()
