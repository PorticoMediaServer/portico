# The owner's clips for the media test suite

The media test suite (`media_matrix_test.go`) makes its own test files with ffmpeg. Six kinds
of file cannot be made that way, so they are cut from films the owner already has. They stay
outside the repository: they are copyrighted and large.

## Where they go

One folder outside the repository, for example `~/portico-owner-clips`, holding files with
exactly these names. A missing one is skipped with a plain message.

| File | What it must hold | Why it matters to a viewer |
|---|---|---|
| `dv-p5.mkv` | Dolby Vision **profile 5** picture (single layer, no HDR10 base), typically a streaming-service download | Without the right conversion it plays with purple and green colours, or is refused |
| `dv-p7.mkv` | Dolby Vision **profile 7** picture, both layers in the one video track: a UHD Blu-ray remux | Must play as its HDR10 base or as Dolby Vision 8.1, with the right colours |
| `dv-p8.mp4` | Dolby Vision **profile 8.1** picture in MP4, with an AAC, AC-3 or E-AC-3 sound track | Plays as Dolby Vision where the device has it, as HDR10 elsewhere |
| `hdr10plus.mkv` | HEVC with **HDR10+** dynamic metadata, typically a streaming-service download | Must keep at least its HDR10 picture |
| `truehd-atmos.mkv` | **TrueHD with Atmos**, 7.1, from a Blu-ray remux | Sound converted, picture copied, 7.1 kept where the device takes it |
| `dts-hd-ma.mkv` | **DTS-HD Master Audio**, 5.1 or 7.1, from a Blu-ray remux | ffmpeg can make plain DTS but not DTS-HD; must convert without silence |

Each one is **30 seconds** long, taken from a moment with light and dark in the picture (not a
black title card). Keep only the first video track and one sound track.

## How to cut one

One command each, from a film you own. `-ss` is where the clip starts. `-map 0:a:0` takes the
first sound track: change the number to pick another (`ffprobe "Film.mkv"` lists them).

```sh
mkdir -p ~/portico-owner-clips
ffmpeg -ss 00:10:00 -i "Film.mkv" -t 30 -map 0:v:0 -map 0:a:0 -c copy ~/portico-owner-clips/dv-p7.mkv
```

The same command makes each of the others: change the source film and the file name. For
`dv-p8.mp4` add `-strict unofficial` before the file name, so the MP4 keeps its Dolby Vision
description, and pick an AAC, AC-3 or E-AC-3 sound track (MP4 does not carry TrueHD or DTS).

To check a clip is what its name says:

```sh
# Dolby Vision: shows "DOVI configuration record" with dv_profile=5, 7 or 8
ffprobe -v error -select_streams v:0 -show_entries stream_side_data ~/portico-owner-clips/dv-p7.mkv
# HDR10+: shows "HDR Dynamic Metadata SMPTE2094-40"
ffprobe -v error -select_streams v:0 -read_intervals %+#1 -show_frames ~/portico-owner-clips/hdr10plus.mkv | grep -m1 2094
# Sound: shows "truehd" with "Dolby TrueHD + Dolby Atmos", or "dts" with "DTS-HD MA"
ffprobe -v error -select_streams a:0 -show_entries stream=codec_name,profile,channels ~/portico-owner-clips/truehd-atmos.mkv
```

## How to run them

From `server-next` in a checkout:

```sh
PORTICO_MEDIA_MATRIX=1 PORTICO_OWNER_CLIPS=~/portico-owner-clips go test -count=1 -run TestMediaMatrixOwnerClips -v ./internal/mediaengine
```

Each clip is played for the same four devices as the generated files, and what comes out is
checked against what was planned. A line starting `FINDING` is a disagreement: send it with
the log. A refusal is listed, not failed, because the plan may rightly refuse (Dolby Vision 5
on a server whose ffmpeg cannot reshape it). Set `PORTICO_FFMPEG` and `PORTICO_FFPROBE` to
check Portico's own ffmpeg bundle instead of the installed one.
