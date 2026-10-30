# Page-content demo fixtures

Synthetic media for the title-page work (`Spec — Page Content by Media Type.md`
§9 Delivery item 4): at least two multi-season shows (one with Specials), one
anime, an artist with albums, EPs, singles and an appearance, a multi-disc
album, and an audiobook series with two books by one author.

All media is **synthetic** (ffmpeg colour cards / sine tones, a few seconds
each). Real titles live in folder/file names and tags so the online metadata
agents match. **Never add copyrighted media here.**

## Contents (122 files)

| Library dir | Set |
|---|---|
| `tv/` | **Sherlock (2010)**: S01–S04 (3 eps each) + Specials **S00E01 The Abominable Bride** (13 files) |
| `tv/` | **Fleabag (2016)**: S01–S02 (6 eps each; episodes are officially just "Episode N") (12 files) |
| `tv/` | **Cowboy Bebop (1998)** (anime): S01, all 26 sessions in broadcast order (26 files) |
| `music/` | **Radiohead**: albums *OK Computer* (1997, 12 tracks) and *In Rainbows* (2007, 10 tracks), EP *My Iron Lung* (1994, 8-track version), single *Creep* (1992 UK 4-track: Creep, Lurgee, Inside My Head, Million Dollar Question) (34 files) |
| `music/` | **Radiohead appearance**: *Talk Show Host* (track 11) on **Various Artists – Romeo + Juliet** (1996); tagged `artist=Radiohead`, `album_artist=Various Artists`, `compilation=1` (1 file) |
| `music/` | **The Beatles – The Beatles** (White Album, 1968): `Disc 1/` (17 tracks) + `Disc 2/` (13 tracks); the `Disc N/` folders and `disc` tags exercise multi-disc headers (30 files) |
| `audiobooks/` | **J.R.R. Tolkien**: *The Fellowship of the Ring* and *The Two Towers*, 3 parts each, tagged `album`, `artist`+`author`, `narrator=Rob Inglis`, `series=The Lord of the Rings`, `series_part=1/2` — the tags `catalog/audio_repository.go` and the `listening_book_context` view read (6 files) |

Release-group types (album vs EP vs single) and the artist appearance come from
the MusicBrainz match on names/tags, not from local tags; the folders are named
exactly as the releases so the match succeeds.

## Generating

```bash
fixtures/page-content/generate.py --out /tmp/portico-page-content   # creates tv/ music/ audiobooks/
fixtures/page-content/generate.py --out /tmp/portico-page-content --dry-run  # list files + tags, generate nothing (--list works too)
fixtures/page-content/generate.py --out /tmp/portico-page-content --only tv --video-seconds 5
fixtures/page-content/generate.py --out /tmp/portico-page-content --force --audio-seconds 8
```

ffmpeg is taken from `PORTICO_FFMPEG` or `PATH` (falls back to
`/opt/homebrew/bin/ffmpeg`). Point a TV library at `<out>/tv`, a music library
at `<out>/music`, and an audiobook library at `<out>/audiobooks`.

## Format notes

- TV is `.mp4` (H.264 + AAC), music and audiobooks are `.flac` — the same
  containers `scripts/generate-demo-media.py` uses.
- Audiobooks are **FLAC, not m4b**: ffmpeg's MP4 muxer drops the custom tags
  the server groups books by (`author`, `narrator`, `series`, `series_part`
  are lost; only title/artist/album/track survive), while FLAC/Vorbis comments
  keep them all (verified with `ffprobe -show_entries format_tags`). The book
  identity comes from the audiobook library kind, not the container.
- `Weird Fishes/Arpeggi` cannot contain `/` in a file name, so the file uses
  `Weird Fishes-Arpeggi` and `generate.py` writes the real `/` title into the
  `title` tag (`TRACK_TITLE_OVERRIDES`).
- Durations default to 5 s video / 8 s audio: long enough to play and seek,
  short enough to generate the whole set in minutes.
