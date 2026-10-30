# Intro and credits detection

Portico finds the intro, the end credits and the scenes around them so players can offer
**Skip intro** and **Skip credits**, skip them unattended when a viewer asks for that, and offer the
next episode while the credits run. Detection is free background work on the owner's own server;
nothing about the media leaves it.

## Modes

One server setting, `library.segmentDetection` in the console settings
(`GET/PATCH /v1/admin/console/settings`), chooses what the server does:

| Mode | What is read | Cost |
| --- | --- | --- |
| `standard` (default) | The sound of each episode where an intro (the first quarter, at least five and at most ten minutes) and the credits (the last third, at most eight minutes) can be. Every file's chapter titles. | A few seconds of processor time per episode, and reading those two stretches of the file. |
| `thorough` | Also the picture near the end: one small grey frame a second over an episode's closing stretch, and over the last quarter (at most twenty minutes) of a film of ten minutes or more. | Decodes video: minutes per file on modest hardware. |
| `off` | Nothing. Detected markers stop reaching viewers; markers an owner made or approved stay. | None. |

Each library's settings carry `segmentDetection`: `server` follows the server's mode; `off`,
`standard` or `thorough` decide for that library alone. Music and audiobook libraries are never
measured.

Standard is the default because it is cheap enough for low-power hosts: on a 45-minute 1080p
episode it measured about 6 s of one core and 57 MB of memory for both stretches. Reading the
picture of the last 15 minutes of a 1080p HEVC or H.264 file took 3.5 to 4 minutes of one core on
the same machine, which is why thorough is a choice rather than the default.

## How it works

Detection runs as its own low-priority background work, one file at a time, through the same
sandboxed decoder and analysis slot as scan analysis, and pauses with every other background task.
It is resumable from the database and incremental:

1. New and changed files are noticed through the inventory change log; a full pass runs when a
   mode changes and once a day.
2. Each file is measured once per revision. The audio becomes a fingerprint (32 bits every 125 ms
   from smoothed log band energies, so it survives re-encoding, volume changes and half-point
   misalignment) and the picture, in thorough mode, a class per frame (black, type on a black
   field, ordinary picture) and its upward motion (a crawl). The measurement is kept as the
   file's `segment_evidence` analysis result.
3. A show is compared once none of its files is waiting. Each episode is compared with up to eight
   neighbours: the nearest of its own season, and the nearest of the seasons around it when its
   season is too small (an intro often outlives a season). A second version of the same episode is
   never a neighbour. An episode is compared again only when its own measurement or a
   neighbour's changes, so a new episode re-compares itself and the episodes around it.

An intro is a stretch of 15 s to 200 s, ending before the middle of the episode, that at least two
neighbours share (one when the season has a single neighbour). Starts within 5 s of the beginning
snap to it; the end runs on through up to 3 s of silence. The credits are the longest shared
stretch in the second half; when audible unshared material of at least 8 s follows (a stinger, a
preview), the marker ends where it begins, so **Skip credits** lands on it. A film's credits come
from its picture: blocks of type on black, split by any stretch of ordinary picture of 8 s or more
(a mid- or post-credits scene), which is never swallowed into a credits marker.

Chapter titles are a fast path: chapters named like an intro, a recap, the credits, a preview of
the next episode (published as an `outro`) or an advert become markers of their own.

## Trust

`segmentmarkers.AutomaticSafe` is the whole policy: a marker may be skipped without asking only
when the owner approved it, or when at least two other episodes confirmed it
(`season_audio_match:*` provenance at confidence 0.85 or more). A single pair is a suggestion at
0.7. Everything a single file says about itself (chapter titles 0.85, the picture 0.65 to 0.75,
less when credits were already running when the frames began) is a suggestion that viewers can act
on by pressing the button, never an unattended skip. Guesses below 0.5 and stretches that recorded
their own ambiguity (`semantic_ambiguous`) are not shown to viewers at all. Overlapping markers of
one kind reach viewers as the strongest one. The same rules apply to item detail, playback offers,
playback options, downloads, and the watched point: trusted credits finish a title early.

Re-detection never overwrites the owner. Each detector replaces only its own untouched markers; a
marker the owner approved, adjusted, dismissed or made stays, and no detector adds a marker of the
same kind over it.

## Owner review

All routes are the local owner's and answer `{protocolVersion, serverId, result}`.

- `GET /v1/admin/segments` — the server's mode and choices, each video library's choice and
  effective mode with its files by state (`pending`, `complete`, `failed`, `unsupported`), how many
  shows wait to be compared, whether the catch-up pass has settled, and the latest files detection
  gave up on.
- `GET /v1/admin/segments/titles?libraryId=&cursor=&limit=` — a library's shows and films by title
  (at most 100 a page), each with its files by state and its markers by trust (`automatic`,
  `suggested`, `approved`, `dismissed`).
- `GET /v1/admin/segments/titles/{id}` — one show by season (or one film): each episode with its
  state and every live marker (`kind`, `startUS`, `endUS`, `confidence`, `provenance`, `basis`:
  `season`, `chapter`, `picture`, `owner` or `earlier`; `approved`, `edited`, `automaticSafe`,
  `viewerVisible`), plus `sourceId`, `sourceRevision`, `mappingRevision` and `markerRevision`:
  exactly what `POST /v1/items/{id}/analysis/markers` takes to approve, adjust (`edit`), dismiss or
  add (`create`) a marker. `outro` is now one of the kinds an owner can add or adjust.
- `POST /v1/admin/segments/titles/{id}/approve` `{kinds, season?}` — approves every detected marker
  of those kinds the owner has not touched, in one season (`-1` for absolute numbering) or the
  whole title; each file's marker revision moves once and its history records the batch.
  `{changed}` counts the markers approved.
- `POST /v1/admin/segments/titles/{id}/redetect` — measures the title's files again and compares
  them afresh; the owner's decisions stay. `{changed}` counts the files queued.

## Online timestamp databases

None is used. TheIntroDB and AniSkip were considered; neither publishes terms or a data licence
for its crowd-sourced timestamps that permit a server to fetch and keep them (checked
2026-10-18). `mediaanalysis.SegmentSource` is the seam an adapter would implement once a source's
terms are confirmed: off by default, behind an owner setting, sending only public ids and the
file's duration, and producing suggestions (`online:*` provenance is never skipped unattended).

## Accuracy

`go test ./internal/mediaanalysis -run SegmentAccuracy` generates seasons with ffmpeg (shared
intro and credits music, cold opens of different lengths, a shortened intro, dialogue over an
intro, scenes after the credits, a season whose intro changes, a single-episode season, a special,
a show with no intro) and decodes them through the detector's own recipes; with
`PORTICO_SEGMENT_ACCURACY=1` it adds a film whose crawl has a mid- and a post-credits scene.
`PORTICO_SEGMENT_MEDIA_OUT=<folder> go test ./internal/mediaanalysis -run SegmentDemoMedia` writes
a small synthetic show and film for trying the feature on a server and its players.
