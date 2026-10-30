# Synthetic test library

Original geometric artwork, fictional metadata and text, procedural video and
tone audio. No commercial media, online metadata or design assets are used.
Generated files stay outside Git. `manifest.json` records the full catalogue;
its movie entries describe files (versions), not grouped movie identities.

## Generate

Use Python 3.11, DejaVu Sans fonts and the qualified Portico FFmpeg bundle.
Keep at least 8 GiB free. From a full checkout:

```sh
python3 -m venv /absolute/runtime/venv
/absolute/runtime/venv/bin/pip install -r fixtures/test-library-v3/requirements.txt
/absolute/runtime/venv/bin/python fixtures/test-library-v3/generate.py \
  --root /absolute/synthetic-library \
  --ffmpeg /opt/portico-ffmpeg/bin/ffmpeg
```

Use a fresh, dedicated output directory to rebuild. Existing media is reused
on reruns, so regenerate in a new directory after generator changes. `--small`
selects a developer subset; `--sections video audio reading live` selects
sections and aggregates existing section manifests. Media copies have independent
inodes so separate episode names remain separate inventory assets; reserve space
for every copied file. Lengths are deliberately short; the high bitrate
case is 96 seconds with a keyframe interval above 36 MB. Light conversion
sources are explicitly marked in each entry's `features`.

The generator uses committed inputs under `fixtures/video/hdr-generated/`,
`fixtures/video/dovi-probe-r40/` and `fixtures/discs/dvd-two-title-video-ts/`.
Dolby Vision preserves the procedural MEL/FEL MP4 configuration rather than
guessing metadata from raw HEVC. Folder-disc ingestion requires the qualified
`portico-disc` helper and bwrap. EPUB/CBZ/CBR/PDF generation is deterministic;
FFmpeg output enables bitexact flags. Compare bytes using the same pinned
Python/font/FFmpeg environment; different encoder versions can change bytes.

## Register and seed

Run the operator scripts against the private tunnel. The root is a path on
the server, while `--manifest` is a local downloaded file. Credentials must
be owned by the operator with mode 0600 and remain outside Git.

Hardening is exact-file only: the private parent directory is 0700 and each
credential or report file is 0600. Never `chmod -R` through the fixture library,
media roots or hardlink aliases: recursive changes traverse shared inodes and
rewrite the source ctime every copy records. A local QA helper did exactly that
at 12:34:22.711Z on 29 October (`chmod -R go-rwx`), changing all fixture source
ctimes. Do not "repair" that with copies or timestamps; a separate server P0
fix removes ctime's content meaning. No operator or private file is edited here.

```sh
python3 fixtures/test-library-v3/seed_state.py \
  --base http://localhost:49300 \
  --root /home/runner/portico-test-library-v3 \
  --manifest /private/manifest.json \
  --live-base http://127.0.0.1:32511/ \
  --report /private/seed-state.json
python3 fixtures/test-library-v3/verify.py \
  --manifest /private/manifest.json --reading --report /private/verify.json
python3 fixtures/test-library-v3/reading_smoke.py --report /private/reading.json
python3 fixtures/test-library-v3/playback_smoke.py --report /private/playback.json
```

The seeder uses real API calls only. All libraries have the `Synthetic `
prefix and local metadata agents. Read reports before treating a run as ready.
The presence of a generator is not evidence that state and playback passed.

`serve_live.py` serves the generated playlist, XMLTV and rolling HLS on
loopback 32511 and a local webhook sink on 32512. Run it on the media host.
Live registration uses the real URL preview/approval flow, allowing only the
explicit loopback stream root. The XMLTV fixture starts on 27 October 2026;
regenerate its date anchor when using it for a later campaign.

The ten-client smoke is a bounded service check: JSON browsing, actual media
decoding and session timeline reports for ten minutes. It does not substitute
for device acceptance or the full load test.

CBR ingestion requires a server built with the official reader capability,
`portico_rar7z`, which every official build takes from
`scripts/release-build-tags.py` (alongside `portico_cb7`). A plain untagged
`go build` intentionally omits the decoder and does not advertise CBR. Check
`go version -m /path/to/portico-server` before a CBR walk: a build that shows
only `-tags release` predates this and cannot admit CBR. Do not rename CBR
files to CBZ or bypass archive admission.

The offline CI smoke runs the real generator naming/copy helpers and fixture
API clients with disposable inputs. It asserts 200 episode entries across all
15 fixture shows and independent physical identities for distinct paths. It
does not replace an actual scan and UI/playback qualification.
