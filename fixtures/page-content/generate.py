#!/usr/bin/env python3
"""Generate the synthetic page-content fixtures: short clips at the relative paths
fixtures/page-content/manifest.json lists, so title/artist/album pages have real
catalogue shapes (multi-season shows with Specials, an anime, an artist with
albums/EP/single/appearance, a multi-disc album, a two-book audiobook series)
without any copyrighted media.

Media is synthetic: a colour card with a moving test-pattern strip for video, a
tremolo sine tone for audio. Real titles live in folder/file names and tags so
the online metadata agents (TVDB, MusicBrainz, Open Library) match.

Usage: generate.py --out <target-dir> [--dry-run] [--list] [--force]
        [--video-seconds N] [--audio-seconds N] [--only tv|music|audiobooks]
Creates <target>/tv, <target>/music, <target>/audiobooks.

--dry-run / --list prints every file that would be generated (with the tags
that would be embedded) and generates nothing.
"""
import argparse
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys

HERE = pathlib.Path(__file__).resolve().parent
MANIFEST = json.load(open(HERE / "manifest.json"))

# Release year per music album folder (folder name -> year). Written as the
# `date` tag; the server reads the album year from `date`/`year`.
ALBUM_YEARS = {
    "OK Computer": "1997",
    "In Rainbows": "2007",
    "My Iron Lung": "1994",
    "Creep": "1992",
    "Romeo + Juliet": "1996",
    "The Beatles": "1968",
}

# Track titles the file name cannot spell exactly ("/" is a path separator).
TRACK_TITLE_OVERRIDES = {
    "Radiohead/In Rainbows/04 - Weird Fishes-Arpeggi.flac": "Weird Fishes/Arpeggi",
}

# The one compilation appearance: the track artist differs from the album artist.
APPEARANCE_TRACK_ARTIST = {
    "Various Artists/Romeo + Juliet/11 - Talk Show Host.flac": "Radiohead",
}

# Audiobook series tags per book folder (book folder -> tags). The server reads
# `album` (book title), `artist`/`author`, `narrator`, `series` and `series_part`
# (catalog/audio_repository.go, audio_evidence.go, listening_book_context view).
AUDIOBOOKS = {
    "The Fellowship of the Ring": {
        "author": "J.R.R. Tolkien",
        "narrator": "Rob Inglis",
        "series": "The Lord of the Rings",
        "series_part": "1",
        "date": "1954",
    },
    "The Two Towers": {
        "author": "J.R.R. Tolkien",
        "narrator": "Rob Inglis",
        "series": "The Lord of the Rings",
        "series_part": "2",
        "date": "1954",
    },
}

TV_PATTERN = re.compile(r"(.*) - (S\d+E\d+) - (.*)\.mp4$")
TRACK_PATTERN = re.compile(r"(\d+) - (.*)\.flac$")
DISC_PATTERN = re.compile(r"(?i)^(disc|disk|cd)[ ._-]*([0-9]{1,3})$")


def resolve_ffmpeg():
    found = os.environ.get("PORTICO_FFMPEG") or shutil.which("ffmpeg")
    if found:
        return found
    fallback = "/opt/homebrew/bin/ffmpeg"
    if pathlib.Path(fallback).exists():
        return fallback
    sys.exit("ffmpeg not found: set PORTICO_FFMPEG or put ffmpeg on PATH")


def plan(kind, rel):
    """Return (output subpath, tag dict, human-readable description) for a manifest entry."""
    if kind == "tv":
        m = TV_PATTERN.match(rel.split("/")[-1])
        show = rel.split("/")[0]
        if not m:
            sys.exit(f"bad tv entry (want '<Show>/<Show> - SxxEyy - <Title>.mp4'): {rel}")
        return rel, {"title": f"{m.group(1)} {m.group(2)}: {m.group(3)}",
                     "comment": f"{m.group(2)} - {m.group(3)}"}, f"TV {show} {m.group(2)} '{m.group(3)}'"
    if kind == "music":
        parts = rel.split("/")
        disc = None
        if len(parts) == 4 and DISC_PATTERN.match(parts[2]):
            artist, album, discpart, filename = parts
            disc = DISC_PATTERN.match(parts[2]).group(2).lstrip("0") or "0"
        elif len(parts) == 3:
            artist, album, filename = parts
        else:
            sys.exit(f"bad music entry (want '<Artist>/<Album>/[<Disc N>/]<NN> - <Title>.flac'): {rel}")
        m = TRACK_PATTERN.match(filename)
        if not m:
            sys.exit(f"bad music file name (want '<NN> - <Title>.flac'): {rel}")
        track, title = m.group(1), m.group(2)
        title = TRACK_TITLE_OVERRIDES.get(rel, title)
        track_artist = APPEARANCE_TRACK_ARTIST.get(rel, artist)
        tags = {"title": title, "artist": track_artist, "album_artist": artist,
                "album": album, "track": str(int(track))}
        if artist == "Various Artists":
            tags["compilation"] = "1"
        if disc is not None:
            tags["disc"] = disc
        if album in ALBUM_YEARS:
            tags["date"] = ALBUM_YEARS[album]
        desc = f"music {track_artist} - {album} ({tags.get('date', '?')}) track {track} '{title}'"
        if disc is not None:
            desc += f" disc {disc}"
        return rel, tags, desc
    if kind == "audiobooks":
        parts = rel.split("/")
        if len(parts) != 3:
            sys.exit(f"bad audiobook entry (want '<Author>/<Book>/<NN> - <Part>.flac'): {rel}")
        author, book, filename = parts
        if book not in AUDIOBOOKS:
            sys.exit(f"audiobook '{book}' has no series tags in AUDIOBOOKS: {rel}")
        m = TRACK_PATTERN.match(filename)
        if not m:
            sys.exit(f"bad audiobook file name (want '<NN> - <Part>.flac'): {rel}")
        track, part = m.group(1), m.group(2)
        info = AUDIOBOOKS[book]
        tags = {"title": f"{book}: {part}", "album": book, "artist": author,
                "author": info["author"], "narrator": info["narrator"],
                "series": info["series"], "series_part": info["series_part"],
                "track": str(int(track)), "date": info["date"]}
        return rel, tags, (f"audiobook {author} - {book} ({info['series']} #{info['series_part']}) "
                           f"part {track} '{part}' narrated by {info['narrator']}")
    sys.exit(f"unknown manifest section: {kind}")


def video(ffmpeg, dst, tags, seconds):
    """A colour card with a moving test pattern strip so seeking is visible. Titles are
    not drawn: the qualified ffmpeg builds ship without drawtext."""
    dst.parent.mkdir(parents=True, exist_ok=True)
    hue = f"{hash(dst.name) & 0xffffff:06x}"
    graph = ("[0:v]drawbox=x=0:y=0:w=iw:h=ih:color=0x" + hue + ":t=fill[bg];"
             "[1:v]scale=640:72[strip];[bg][strip]overlay=0:288[out]")
    cmd = [ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
           "-f", "lavfi", "-i", f"color=c=black:s=640x360:r=24:d={seconds}",
           "-f", "lavfi", "-i", f"testsrc2=s=640x72:r=24:d={seconds}",
           "-f", "lavfi", "-i", f"sine=frequency=330:sample_rate=48000:d={seconds}",
           "-filter_complex", graph, "-map", "[out]", "-map", "2:a",
           "-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-pix_fmt", "yuv420p",
           "-c:a", "aac", "-b:a", "96k", "-shortest", "-movflags", "+faststart"]
    for key, value in tags.items():
        cmd += ["-metadata", f"{key}={value}"]
    cmd.append(str(dst))
    subprocess.run(cmd, check=True)


def audio(ffmpeg, dst, tags, seconds, index):
    dst.parent.mkdir(parents=True, exist_ok=True)
    hz = 220 + 20 * (index % 12)
    cmd = [ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
           "-f", "lavfi", "-i", f"sine=frequency={hz}:sample_rate=44100:d={seconds}",
           "-af", "tremolo=f=2:d=0.4,afade=t=in:d=1,afade=t=out:st=11:d=1",
           "-c:a", "flac"]
    for key, value in tags.items():
        cmd += ["-metadata", f"{key}={value}"]
    cmd.append(str(dst))
    subprocess.run(cmd, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--out", required=True, type=pathlib.Path,
                        help="target directory; creates tv/, music/, audiobooks/ under it")
    parser.add_argument("--dry-run", "--list", dest="dry_run", action="store_true",
                        help="list every file (and its tags) without generating anything")
    parser.add_argument("--force", action="store_true",
                        help="regenerate files that already exist")
    parser.add_argument("--video-seconds", type=int, default=5)
    parser.add_argument("--audio-seconds", type=int, default=8)
    parser.add_argument("--only", choices=("tv", "music", "audiobooks"), default=None)
    args = parser.parse_args()

    kinds = [args.only] if args.only else ("tv", "music", "audiobooks")
    jobs = []
    for kind in kinds:
        for rel in MANIFEST[kind]:
            subpath, tags, desc = plan(kind, rel)
            jobs.append((kind, args.out / kind / subpath, tags, desc))

    if args.dry_run:
        for kind, dst, tags, desc in jobs:
            print(f"{dst}")
            print(f"    {desc}")
            print(f"    tags: " + ", ".join(f"{k}={v}" for k, v in sorted(tags.items())))
        print(f"({len(jobs)} files)")
        return

    ffmpeg = resolve_ffmpeg()
    n = 0
    for index, (kind, dst, tags, desc) in enumerate(jobs):
        if dst.exists() and not args.force:
            continue
        if kind == "tv":
            video(ffmpeg, dst, tags, args.video_seconds)
        else:
            audio(ffmpeg, dst, tags, args.audio_seconds, index)
        n += 1
    print(f"generated {n} files under {args.out} ({len(jobs) - n} already present)")


main()
