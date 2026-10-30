# The player engine contract

Every video engine in the app implements this one contract between JavaScript and native code.
Apple has one video engine, **AVPlayer**, exposed as **native**. Android retains its native
Media3 and mpv engines; every mpv-specific section below applies to Android only. The player
screen drives the platform's installed engine through `src-next/platform/playback/NativePlaybackCoordinator.ts`
and holds a lease on one engine at a time.

JavaScript owns the playback session. The engine owns the player. JavaScript sends **commands**,
each of which is the whole desired state. The engine answers with **facts**. The engine never
changes what is wanted (it never starts, pauses or seeks of its own accord); where the system
forces its hand it says so with a fact and JavaScript changes the intent.

An engine written against this file must reproduce every rule in it, including the error texts:
JavaScript and the tests match on them.

## 1. Names

| | native | mpv |
| --- | --- | --- |
| Module | `PorticoPlaybackModule` | `PorticoMpvPlaybackModule` |
| Fact event | `PorticoPlaybackFact` | `PorticoMpvPlaybackFact` |
| Picture in Picture event | `PorticoPictureInPicture` | `PorticoMpvPictureInPicture` |
| View | `PorticoPlayer` | `PorticoMpvPlayer` |

The names are the same on every platform. A build without an engine has no module for it (or a
module whose `available` constant is false), and JavaScript then never selects it.

## 2. Module

All methods run on the main thread. Promises reject with the code shown.

| Method | Resolves | Rejects |
| --- | --- | --- |
| `acquire(owner: string)` | `{lease: string, instanceId: string, engine: 'native' \| 'mpv'}` | `bridge_closed` (module invalidated), `owner_busy` (another owner holds the lease), `engine_unavailable` (mpv not in this build) |
| `command(value: object)` | `true` | `bridge_closed`; `command_rejected` with one of the messages in section 4 |
| `release(lease: string)` | `true` (always; a lease that is not current is ignored) | never |
| `facts()` (mpv only) | the device facts in section 8 | never |

Constants (never change while the app runs):

| Constant | Type | Meaning |
| --- | --- | --- |
| `engine` | `'native' \| 'mpv'` | Which engine this module is. |
| `available` | boolean | The engine is built in and can start. |
| `features.pictureInPicture` | boolean | It can float its picture. mpv: false. |
| `features.airPlayVideo` | boolean | It can send video to an AirPlay receiver. mpv: false; native: true on iPhone and iPad. |
| `features.liveChannels` | boolean | It plays channels (`timelineOriginMs`). mpv: the module's `liveChannels` switch (off until played on a device; see §10). |
| `features.musicSessions` | boolean | It plays the listening owner's music and audiobooks. mpv: false. |
| `features.audioTrackSwitching` | boolean | It switches a file's audio track itself (`audioTrack`). native: false; mpv: true. |
| `features.subtitleRendering` | string[] | Subtitle kinds it draws itself: `text`, `ass`, `pgs`, `vobsub`, `dvb`. native: `['text']`. |
| `features.chapters` | boolean | Its `mediaInfo` fact carries the file's chapters. native: false; mpv: true. |

The player hides Picture in Picture and AirPlay while an engine without them is active, and
never gives a channel or a music session to an engine that does not play them.

The native module has further methods for the listening owner (`adoptListeningSession`,
`listeningAction`, and so on). They are not part of this contract; mpv has none of them.

## 3. Lease

- `acquire(owner)` gives the one lease of this engine. `owner` is 1 to 128 characters. While a
  lease is held, the same owner gets the same lease again and any other owner is refused.
- `instanceId` names this engine instance for as long as the module lives. Every fact carries it.
- A new lease starts `commandRevision` and `factSequence` at 0.
- `release(lease)` stops playback, closes the media, forgets the binding and the command, and
  ends the lease. Facts stop at once. The audio session a video activated is given back half a
  second later unless this app is still playing something.
- JavaScript holds a lease on one engine at a time. To move a title to the other engine it
  releases the lease, which makes the playback service ask for the position already reached as a
  seek; it then acquires the other engine's lease and sends the same session with that
  `pendingSeek`. The session, and so the queue, do not change.

## 4. Commands

A command is one object. Unless it is accepted in full nothing changes. Checks run in this order
and the first failure is the rejection message.

| Field | Type | Rule | Message when broken |
| --- | --- | --- | --- |
| (module) | | Not invalidated. | `Playback controller is closed.` |
| `binding` | object | Present. | `Invalid playback binding.` |
| `binding.lease` | string | Equals the current lease. | `Playback lease is no longer active.` |
| `paused` | boolean | A real boolean, not 0 or 1. | `Invalid paused flag.` |
| `source`, `rate` | string, number | `rate` finite. | `Invalid playback command.` |
| `binding.intentId`, `binding.generation` | integer | 1 to 2^53-1. | `Invalid playback binding.` |
| `commandRevision` | integer | 1 to 2^53-1. | `Invalid playback revision.` |
| | | Lower than the last accepted. | `Stale playback command.` |
| | | Equal to the last accepted: accepted without effect if the whole command is identical, otherwise refused. | `Conflicting playback command.` |
| `binding.sessionId`, `binding.itemId` | string | Present. | `Invalid playback binding.` |
| `timelineOriginMs` | integer or null | Positive integer when present. | `Invalid channel clock.` |
| | | mpv with its `liveChannels` switch off: any non-null value. | `This player does not play live channels.` |
| `linearClock` | array or absent | mpv only, with `timelineOriginMs`: the channel's clock epochs, oldest first, at most 64: `{offsetMs}` then `{startMs, offsetMs}` with increasing starts. Channel position = media time + offset. Absent without `timelineOriginMs`. | `Invalid channel clock.` |
| `connectionState`, `routeRevision` | `'ready' \| 'verifying'`, integer >= 0 | When `connectionState` is present both must be valid. | `Invalid route transition.` |
| | | `routeRevision` absent or lower than the last accepted command's. | `Stale route transition.` |
| `listeningOwner` | string or null | mpv: must be null or absent. | `This player does not play music sessions.` |
| `audioRender` | object or null | mpv: must be null or absent. (native validates the render plan.) | `Unsupported audio render context.` |
| `binding` against the previous one | | `intentId` lower than before; or equal with a different binding or a different source (a new `routeRevision` with the same path and query is the same source). A binding with the same lease, `intentId`, `sessionId` and `itemId` and a higher `generation` is not stale: the server planned the session again in place (a quality, version, audio or subtitle change, a channel's next window), and the engine opens the new source. | `Stale playback binding.` |
| `pendingSeek` | `{revision, positionSeconds}` or null | Both finite numbers, not negative (native allows a negative position for a channel). | `Invalid seek command.` |
| | | `revision` a whole number >= 1. | `Invalid seek revision.` |
| | | Same binding and a `revision` lower than the last accepted seek. | `Stale seek command.` |
| | | Same binding, the same `revision`, and either a different position or a seek already retired (a later command carried none). | `Conflicting or retired seek command.` |
| `audioTrack` (mpv) | `{revision, streamIndex}` or null | `revision` >= 1, `streamIndex` 0 to 65535. | `Invalid audio track command.` |

Fields with no rejection of their own:

| Field | Type | Meaning |
| --- | --- | --- |
| `source` | string | The media address: `http`/`https` with a host and no credentials, or a `file:` address inside the app's Downloads folder (checked after symbolic links are resolved). Anything else is answered with an `error` fact, `Invalid media address.` |
| `rate` | number | The wanted speed. Supported: 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2 (native leaves out the two below 1 when the media cannot play slower; a channel is 1 only). An unsupported rate keeps the player paused and is reported in `rateError`. |
| `audio` | object or null | Section 6. |
| `subtitles` | object or null | Section 7. |

What an accepted command does:

1. **Different binding**: route state is cleared.
2. **`connectionState: 'verifying'`** (same binding): the position reached is kept, the media is
   closed, and the engine waits. No facts are sent until a `ready` command arrives. The same
   happens to the kept position when the source changes within a binding.
3. **Load.** The media is (re)opened when the engine was waiting, the `intentId` changed, the
   source changed, or nothing is open. A new `itemInstanceId` is made. With a kept position the
   engine returns to it before saying `ready`; if it cannot get within half a second it sends
   `error`: `The previous playback position is unavailable on this connection. Your position has
   been retained.` and, if ten seconds pass first, `error`: `Connection restored, but the
   retained position is not available. Your playback session was not replaced.`
4. **`pendingSeek`** with a `revision` higher than the last one performed is performed once the
   media is ready: an exact seek, then `seekApplied` if the engine landed within 0.5 s of the
   target, otherwise `seekFailed` (`That position is not available yet. Try seeking again.`). A
   seek present in the command that opens the media is where the media opens. A seek replaces a
   kept route position.
5. **Intent.** The player plays at `rate` only when `paused` is false, no seek is pending in the
   command or in flight, the route is ready and the kept position (if any) is restored. While a
   `pendingSeek` is still in the command the player stays paused: JavaScript removes it after
   `seekApplied`. Then a `rateStatus` fact is sent.

## 5. Facts

Every fact is one object on the engine's fact event. Fields on every fact:

| Field | Type | Meaning |
| --- | --- | --- |
| `type` | string | One of the types below. |
| `instanceId` | string | The engine instance. |
| `intentId` | integer | The `intentId` of the media the fact is about. |
| `binding` | object | The binding of the last accepted command, unchanged. |
| `itemInstanceId` | string | Changes with every load. |
| `factSequence` | integer | Rises by 1 with every fact of a lease. |
| `lastCommandRevision` | integer | The last accepted `commandRevision`. |
| `seeking` | boolean | A seek is in flight or still in the command. |
| `position` | number | Seconds in the media's own timeline (0 when unknown). |
| `paused` | boolean | The player is paused and not seeking. |
| `error` | string | Empty unless the type is an error. |
| `effectiveRate` | number | The speed the picture is moving at now; 0 when paused or waiting. |
| `supportedRates` | number[] | Empty until the media is ready. |
| `rateReady` | boolean | The media is ready. |
| `rateWaiting` | boolean | Wanted to play but waiting for data. |
| `rateError` | string | Set when the wanted rate is not supported. |
| `monotonicSeconds` | number | The device's uptime clock. |
| `engine` | string | mpv sends `'mpv'`. Optional. |

No fact is sent while the engine waits on `verifying`. While a kept position is being restored
only `error` facts are sent. JavaScript drops any fact whose `instanceId`, binding, sequence or
`lastCommandRevision` shows it belongs to a superseded lease, item, route or seek.

| Type | When | Extra fields |
| --- | --- | --- |
| `ready` | The media is open and can play (after a kept position is restored). | |
| `firstFrame` | The first picture is on screen. | |
| `progress` | Twice a second while the position moves, and once when it stops. Never repeated for a paused picture. | Optional, for a stream from the server only: `throughputKbps` (what the engine measured of the connection while it was downloading as fast as it could; absent when nothing fresh was measured, never an estimate made before any transfer), `bufferedSeconds` (media held ahead of the position). JavaScript feeds Automatic quality and the session's timeline from them. |
| `stalled` | The buffer ran dry after playback had begun. Not an error. | |
| `ended` | The media played to its end. Once per load (again after a seek back). | |
| `rateStatus` | The intent was applied, or the pause, speed or waiting state changed. | |
| `seekApplied`, `seekFailed` | A `pendingSeek` finished. | `seekRevision` |
| `error` | The media cannot continue. `paused` is true. The Android native engine also sends it, with `engineCode: 'source_not_supported'`, when a seek lands at the start of a streamed file that has no index to seek by: the server then delivers the title another way at the position asked for. | `networkError`, `deliveryError` (booleans), `httpStatus` (0 or 400-599), `hasPlayed` (a frame was shown or time advanced), `uncertain` (the cause is not the media: an interruption, an output that would not start), `engineCode` (`''`, `decode_error` or `source_not_supported`; empty when it is a network or delivery error), `errorDetail` (a short technical string) |
| `subtitleStatus` | The subtitle command was taken up or could not be. `error` is empty on success. | |
| `externalPlayback` (Apple) | AirPlay video started or ended. JavaScript burns in the captions it draws while it is active (SUP-007). | `active` (boolean) |
| `systemPaused` | The system paused a video the app wanted playing (the app left the screen). JavaScript sets its intent to paused. | `reason` (mpv: `'background'`) |
| `audioMapped`, `audioObserved`, `audioApplied`, `audioFailed`, `audioUnavailable` | Section 6. | |
| `audioTrackApplied`, `audioTrackFailed` (mpv) | An `audioTrack` choice finished. | `audioTrackRevision`, `streamIndex` |
| `mediaInfo` (mpv; native local audio) | When the current inventory changes. mpv also reports after `ready`. | mpv: `durationSeconds`; `chapters`: `{startSeconds, title}[]`; `video`: `{width, height, framesPerSecond, codec, dynamicRange (sdr, hdr10, hlg, dolby_vision), outputDynamicRange (sdr, hdr), hardwareDecoding}`; `audioTracks`, `subtitleTracks`: `{streamIndex, language, title, codec, channels, selected, external, default, forced}[]`. Native local audio: `audioTrackCatalogRevision`, `audioTracks: {id, selected, language?, title?, codec?, channels?}[]`. Local audio can include the optional proven `sourceTrackIdentity` described in section 6. |

Error classification, in order:

1. An HTTP status of 400-599 from the media's server: `httpStatus` is set, `deliveryError` is
   true, and `networkError` is true for 408, 429 and 500-599.
2. A connection failure with no answer: `networkError`, unless the failure is one waiting will
   not change (file missing, no permission, transport security, cancelled), which is
   `deliveryError`.
3. The engine refusing the media: `engineCode` `source_not_supported` (not a format it reads) or
   `decode_error` (a track it cannot decode; mpv reports this when a file's picture track will
   not decode, rather than playing sound over black).
4. Anything else: `uncertain` is true and nothing is blamed.

The native engine also sends `audioPrepared`, `audioCommit`, `audioBoundary`,
`audioPreparationFailed` and `audioRenderFailed` for the listening owner's music. They are not
part of this contract.

## 6. Audio

`audio` chooses between the audio renditions the server named for a stream:

```
{binding: {intentId, sessionId, generation, sourceId, planRevision}, epoch: number,
 renditions: [{id: string, name: string, streamIndex?: number}], defaultRenditionId: string,
 pending: {revision: number, renditionId: string} | null}
```

It applies only when `binding.intentId`, `sessionId` and `generation` equal the command's
binding. Every audio fact carries `audioBinding` (the command's audio binding) and `audioEpoch`.

- Once per load the engine maps every rendition to exactly one of its audio tracks and sends
  `audioMapped {renditionIds: string[]}`. If any rendition cannot be mapped, none is:
  `audioMapped {renditionIds: [], audioMappingFailure}` (`timeout`, `load_failed`,
  `unsupported_metadata`). Native maps by the HLS rendition name. mpv maps by `streamIndex` when
  given, else by the name (the track title, or the HLS name), else by order when the counts agree.
- `audioObserved {audioSequence, renditionId | null}` whenever the track in use changes, and after
  mapping.
- A `pending` with a new `revision` is selected, then `audioApplied {audioRevision, renditionId}`
  once the track in use is that rendition, or `audioFailed {audioRevision, error}` if it is not in
  the map.
- `audioUnavailable {renditionId}` (native) when the server refuses a rendition's playlist; the
  engine returns to `defaultRenditionId`.

`audioTrack` (mpv; engines with `features.audioTrackSwitching`) chooses a track inside a file that
is played as it is, without a new session: `{revision, streamIndex}`, where `streamIndex` is the
stream's index in the file. The engine answers `audioTrackApplied` or `audioTrackFailed` with the
same `audioTrackRevision`. JavaScript sends the field only to an engine that has the feature, and
only for the playback it was chosen in.

Android mpv applies the property under the expected core load epoch, then samples `aid` on
that core thread for the exact command revision. An already-selected target is acknowledged
from this fresh readback, rather than a cached pre-command snapshot.

`localAudioTrack` (native, downloaded files only) is `{revision, catalogRevision, trackId}`.
`trackId` is an opaque token from the current `mediaInfo.audioTracks[].id`; `catalogRevision`
must equal that inventory's `audioTrackCatalogRevision`. The engine answers
`localAudioTrackApplied` only after actual selection readback, or `localAudioTrackFailed`,
with `{audioTrackRevision, catalogRevision, trackId}`. No audio ordinal or file stream index
can substitute for this token. Native catalogues retire when their loaded item/inventory changes.

For one admitted downloaded file, an audio inventory entry may additionally carry
`sourceTrackIdentity: {container: 'isobmff', trackId: string}`. The decimal `trackId` is the
container's positive `tkhd.track_ID`, currently limited to 1..2147483647 for the pinned engines.
It is independent of a native catalogue token, Media3's output-track number, mpv's user track
ID, and mpv's absolute `ff-index`/`streamIndex`.

Android emits this optional fact only after reading that exact local source with a bounded
random-access parser (at most 64 KiB read, 1024 boxes, 128 container tracks and 64 audio tracks;
media payload is skipped). The proof subset is classic ISO-BMFF with a supported major brand,
unique `tkhd` IDs, audio handlers and one supported ordinary audio sample description. Damaged,
fragmented, compressed, referenced or complex audio containers and exhausted limits omit proof.
The single-entry cache is bound to the producer's loaded source and file size/mtime, retired
on load replacement or observed file change; no file bytes or per-title choices are persisted.

The producer must also prove the demux meaning of its engine fact. With pinned Media3 1.10.1,
the actual MP4 `Format.containerMimeType` must be `audio/mp4` or `video/mp4`, and its exact
`Format.id` must match one parsed audio `tkhd` ID. Duplicate format IDs omit proof. The
[Media3 MP4 BoxParser](https://github.com/androidx/media/blob/1.10.1/libraries/extractor/src/main/java/androidx/media3/extractor/mp4/BoxParser.java)
sets audio format IDs from the track header; [Mp4Extractor](https://github.com/androidx/media/blob/1.10.1/libraries/extractor/src/main/java/androidx/media3/extractor/mp4/Mp4Extractor.java)
preserves that format ID and sets its MP4 container MIME type. This never interprets an arbitrary
extractor's `Format.id` as an absolute stream index.

With pinned mpv 0.41.0 / FFmpeg 8.1.2, the epoch-bound actual `file-format` must equal
`mov,mp4,m4a,3gp,3g2,mj2`, and a unique nonexternal audio `src-id` must match the parsed ID.
[mpv track properties](https://github.com/mpv-player/mpv/blob/v0.41.0/player/command.c)
expose `src-id` and `ff-index` separately; [demux_lavf](https://github.com/mpv-player/mpv/blob/v0.41.0/demux/demux_lavf.c)
maps MP4 `src-id` to the AVStream ID and reports the actual lavf format;
[FFmpeg mov_read_tkhd](https://github.com/FFmpeg/FFmpeg/blob/n8.1.2/libavformat/mov.c)
reads that ID from `tkhd`. Language, title, codec and ordering never supply identity.

Only an explicit choice is retained, and only inside its `NativeOfflineTracks` playback/file
binding. After an engine/instance replacement, JavaScript resolves exactly one current entry
with the same proven source identity, sends a newly revisioned engine-specific command, and
waits for that command's exact readback. Native also requires the replacement catalogue revision.
The coordinator first fences facts by lease/instance/binding/sequence and command lifetime.
Missing/ambiguous replacement proof fails the retained choice honestly; an original choice
without proof has no cross-engine retention. Apple omits source identities it cannot prove.

## 7. Subtitles

```
{binding: {intentId, sessionId, generation}, mode: 'off' | 'track', revision: number,
 resourceId: string, resourceRevision: number, offsetUs: string, refreshRevision: number,
 documentUrl?: string, manifestName?: string, origin?: string, language?: string, title?: string,
 localSessionOffsetUs?: string,
 style?: {scale: number, background: 'none' | 'translucent' | 'opaque'},
 render?: {kind: 'text' | 'ass' | 'pgs' | 'vobsub' | 'dvb',
           embedded?: {streamIndex?: number, ordinal?: number, language?: string, title?: string},
           url?: string, fonts?: [{name: string, url: string}]}}
```

`style` is Portico's caption style (the profile's subtitle size and background), for text the
device draws: the app's own line, and plain text an engine draws itself. `scale` (0.5 to 3)
multiplies the size; it is multiplied again by the caption size the viewer set in the system's
Accessibility settings. `background` is a soft black box (the default), a solid one, or none (the
line then has an edge of its own). Where the viewer's system caption style names a colour, a
background or a typeface of its own and says it overrides what a video provides (Apple), or
captions are switched on in the system with a style that names one (Android), the system's wins.
Styled (ASS) tracks keep their own look.

It applies only when its binding matches the command's. `mode: 'off'`, or no `subtitles`, shows
nothing. `offsetUs` is a decimal string of microseconds, -600 s to +600 s; a positive value shows
every line later.

For Apple downloaded WebVTT only, `offsetUs` can already be baked into the saved document.
Its optional `localSessionOffsetUs` (decimal microseconds, -600 s to +600 s, default zero)
supplies only the additional playback-session adjustment. The Apple downloaded-VTT reader
does not apply baked `offsetUs` again. Rich originals and Android/mpv retain their existing
`offsetUs` behavior.

Two ways to draw a track:

1. **The cue document** (`documentUrl`): the server's plain-text cues as JSON. The engine fetches
   it and the app draws the line over the picture, inside the picture area: white on a 70 % black
   box, 3.5 % of the picture's width (12 to 40 points, times `style.scale` and the viewer's system
   caption size), bottom centred, or at the top for a cue marked `top`, italic for a cue marked
   `italic`. For a downloaded title (a `file:` source) the address is instead a WebVTT file
   (`.vtt`) inside the app's Downloads folder, checked like the source: the engine reads it once
   and draws its cues the same way, with no re-check. Otherwise the
   address must be on the media's own origin, under the media's grant
   (`/v1/media/subtitles/<grant>/<resourceId>/<resourceRevision>`), `https` or `http` as the media is,
   with no credentials, query or fragment; otherwise `subtitleStatus`: `The subtitle
   address does not belong to this playback session.` The document is re-checked every 25 seconds
   with its ETag and blanked after 40 seconds without a good answer. A subtitle file or font is
   accepted from any host over `http` or `https` under the same origin and grant rule (the media
   source itself stays restricted, and redirects stay refused). On Android, plain text that also
   has a cue document is always drawn from the cue document. Native also lets the system
   draw an embedded text track when it can match one by `manifestName` or by language and title.
   Both engines implement this. It is the only way the native engine draws subtitles.
2. **The engine draws the track itself** (`render`; engines whose `features.subtitleRendering`
   lists the `kind`). The engine first looks for the track inside the media (`embedded`: by
   stream index, else by position among the file's own subtitle tracks, else by language and
   title, and only among tracks of that kind), then for the original subtitle file at `url`
   (same origin and grant rule as above; for VobSub the `.idx` address, the `.sub` beside it: a
   file played as it is may open nothing but itself, so the engine allows that one companion to
   be opened for as long as the adding of the track takes, and no longer).
   `fonts` are the fonts attached to a styled file: at most 16, fetched before the file is added.
   On success `subtitleStatus` with an empty `error`. If neither can be done the engine falls back
   to the cue document when the command has one, and otherwise sends `subtitleStatus`: `This
   subtitle track is unavailable in the current player.`

JavaScript sends `render` only to an engine that draws that kind. Plain text drawn by mpv itself
(`kind: 'text'`) uses the same look as the cue line.

## 8. View

The view shows the engine's picture for the lease it is given, and nothing for any other lease.
The engine outlives its views.

| Prop | Type | Meaning |
| --- | --- | --- |
| `lease` | string | The lease whose picture to show. |
| `pictureInPictureRequest` | integer | Each change asks for Picture in Picture to start, or to stop if it is running. |
| `pictureInPictureBinding` (Apple) | binding | The exact installed `{lease,intentId,sessionId,generation,itemId}`. Required for an explicit PiP request. |

The Picture in Picture event carries `{lease, state, subtitlesCarried?}`. `state` is `started`,
`stopped`, `restore` (the viewer came back to the app), or `unavailable`. mpv always answers
`unavailable`. `subtitlesCarried` is false when the chosen subtitles are drawn by the app and so
do not appear in the floating picture.

Apple events additionally carry `binding` (the immutable start binding, or null) and `requestId`
(the explicit request integer; 0 for an automatic OS start). Native settles the two props for
one main loop, checks equality with the actual source-ready binding, and refuses an app-drawn
caption or rich overlay. The runtime first replans the same session to burn or native WebVTT,
preserving the selected track, complete source-relative offset and source position. It waits
for the resulting generation's real ready fact before installing these props. Apple automatic
PiP and external playback are disabled while an app-drawn overlay is selected. A stale callback
cannot acknowledge a newer request.

`facts()` (mpv) answers what the capability profile is built from:

```
{engine: 'mpv', available: boolean, memoryBytes: number, processorCount: number,
 hardwareDecode: {h264, hevc, vp9, av1: boolean}, hdrOutput: boolean,
 screen: {width, height, maxFrameRate}, audio: {maxOutputChannels}}
```

Android also sends, and Apple leaves out: `hardwareLimits: {<codec>: {maxWidth, maxHeight,
maxFrameRate, tenBit}}` (each hardware decoder's own limits), `hdrOutputRanges: string[]`,
`dolbyVisionProfiles: number[]`, `softwareDeinterlace: boolean`.

## 9. System behaviour every engine keeps

- **Audio session.** Opening a video sets the session to playback, movie mode, multichannel
  allowed, and activates it. `release` gives it back (section 3).
- **Now Playing and remote commands** belong to `PorticoSystemMedia`, not to the engine. On Apple,
  it reads the playing video's position, rate and length from the single playback controller's
  AVPlayer, and turns every
  lock-screen or remote press into an event for JavaScript.
- **Interruptions and route changes** reach JavaScript through `PorticoSystemMedia` as pause and
  play commands. The engine only makes sure its output works again afterwards.
- **Leaving the app.** An engine that cannot keep drawing (mpv; AVPlayer without Picture in
  Picture or AirPlay) ends up paused. It sends `systemPaused` when the app wanted it playing.
- **Display matching (television).** Before the first `ready`, the engine asks the display to
  match the title (its frame rate; on Apple TV its dynamic range too) and holds playback until
  the switch has ended (10 s at most; a title that needs no switch is held well under a second).
  Every engine does this. Apple: the system's Match Content settings decide, and with them off
  nothing is waited for. Android: Android 11 and later, through the system's frame-rate request,
  so its "match content frame rate" setting decides; an HDR title on a television that shows HDR
  is also held for a moment after its first picture, which is when the television changes to HDR.
  Never on a phone or tablet, and never for music.
  Native tvOS commands independently accept `matchDynamicRange: auto | off`. Automatic
  permits an app-owned `AVDisplayManager.preferredDisplayCriteria` request subject to the
  system's Match Content settings. Off cancels pending matching and withdraws only criteria
  still owned by this controller; it never selects SDR or changes system output policy.
  Replacing or retiring the item withdraws that same request, including video-to-audio and
  connection-verification transitions. The separate Quality Turn off HDR restriction remains
  a delivery capability restriction and is never read or written by this port.
  Only tvOS exports `dynamicRangeMatching: true`. Android Media3 and mpv currently expose
  `Surface.setFrameRate` for cadence; HDR output follows decoded Surface content (mpv uses
  `mediacodec_embed` when supported). Neither exposes a Portico-owned dynamic-range matching
  enable/disable request independent of HDR delivery or fixed system output policy. iOS also
  has no television display-matching port. These paths do not offer the matching control;
  current output range is not inferred from a saved preference.
- **Network.** The engine reads only the command's `source` and addresses under the same media
  grant. TLS is validated by the system. Redirects of subtitle and font fetches are refused.

## 10. What mpv does not do

Live channels are written but switched off (`liveChannels` in each module) until they have been
played on a device. mpv places itself on a channel by `linearClock` (media time with
`rebase-start-time` off, plus the epoch's offset), seeks within what it has read, and when it
cannot (a live playlist is not seekable beyond its cache) reopens the playlist at the target
(`?at=`, `live_start_index=0`) and seeks there once.

Everywhere: no Picture in Picture, no AirPlay video, no music sessions, no bitstream
passthrough (sound is decoded to PCM in as many channels as the output takes), and
Dolby Vision is never output as Dolby Vision. The engine is Portico's own build
(`third_party/media-engine/`), built for Android only. Apple rich subtitles use the separate
`third_party/apple-subtitles/` recipe with the exact patched Jellyfin FFmpeg source; no mpv engine
or Apple mpv dependency is built.

- **Android, Android TV and Fire TV.** An HDR10 or HLG title on a screen that shows it is shown in
  HDR (direct output), except while mpv draws the subtitles itself (ASS, PGS, VobSub, DVB): the
  title is then tone-mapped to standard range on the device. Dolby Vision profiles 7 and 8 play
  their HDR10 layer; profile 5 is not offered. No deinterlacing of interlaced video.

Each is declared in `features` or in the capability profile (Android's per-engine display ranges
come from `facts()`), and the player hides what does not apply.

## 11. Apple transition snapshots

`PorticoVideoSnapshot` uses the existing cross-platform envelope:

```
capture(id, {reactTag, engine: 'native', lease,
  binding: {intentId, sessionId, generation, itemId}})
  -> {id, uri, width, height}
release(id) -> boolean
```

The actual retained `PorticoPlayerView` tag must resolve to the same attached controller,
AVPlayer and lease. The complete current source-ready binding is checked before copying a
decoded `AVPlayerItemVideoOutput` buffer and again after JPEG encoding. There is no asset
image generator, seek, remount, view rasterization or replacement playback source. Missing
bounds leave that attachment intact. The app must be active and the retained view must
still belong to a window; geometry and hidden state are not source authority.

One encoding operation and one retained image are allowed. The native deadline is 1.5 seconds;
late work is discarded. JPEGs live in a private per-module cache directory, at most 1280 pixels
on either side and 921600 pixels total. Release, cancellation, binding replacement, detachment,
backgrounding and invalidation remove owned files. Phone/tablet helpers require the actual
bridge; TV remains unavailable. Capture can decline until a real decoded buffer arrives.
The live surface remains the fallback. This API alone does not qualify frame 9's composition,
caption continuity, HDR appearance or the common presentation's one-pass fade.

## 12. Apple device-local audio processing

The playback module exposes `audioProcessing()`, `setAudioSampleRateMatching(value)`,
`setAudioEqualizer({enabled, preampDb, bands})` and `resetAudioEqualizer()`. Each returns the
committed native snapshot. Matching is `smart | strict | off`, default `smart`. EQ is one
app/device record with ten gains and a preamp, each -12..12 dB in 0.5 dB steps. Reset flattens
the gains/preamp while preserving its switch and matching mode. There are no output-specific
keys, presets, account writes, aliases or migrations.

`sampleRateMatching`, `eqEnabled`, `eqPreampDb` and `eqBands` describe committed settings.
Requested/output rates and channel counts are actual audio-session observations; failures
remain machine codes. Persisted intent never proves processing. Current-song `eqApplied`
and `levelingApplied` come from rendered-deck/connected-graph observations and clear for
new, failed, ended or stopped playback. These facts do not claim physical bit-perfect output.

Optional `levelingGainDb` is the current deck's actual Float32 coefficient converted to dB
after it has been multiplied into decoded buffers. Apple also requires the node clock to have
heard that deck; Android requires the real sink to accept a complete current-stream PCM piece.
It is finite within -60..30 and only accompanies `levelingApplied: true` under the same
current listening binding. It excludes the crossfade node's volume and the device EQ preamp;
a requested plan gain or an unobserved Android processor target is not this fact.

Android's gain queue and rate stage retain optional original-stream coefficient witnesses
through retries, interpolation, format drains and end tails. The coefficient comes from
the gain call itself, and mixed/ramped/unknown samples or non-unity fade suppress it.
The Media3 1.10.1 renderer resolves its exact input MediaPeriodId to an immutable tag issued
when the original bound source was installed, before any seek-index await. It queues that
origin before super.onStreamChanged and follows each actual output-stream assignment,
including the first stream and equal numeric offsets. Reset mirrors Media3's last-pending
selection and clears pending custody; a non-codec-reset seek remains unknown until another
actual output-stream boundary. Sink discontinuities, PTS, configure/flush and today's binding
at acceptance never issue or replace a stamp. Retries keep the original input and piece proof.

Android publishes optional applied/amount facts only for accepted settled, unity-fade PCM
whose original stream, source, gain controls, session, generation, item, plan, lease and intent
are still current, while the sole deck is playing and no seek/route recovery is pending. The
owner removes them for a mismatched heard binding, ended or terminal playback. These are PCM
processing/acceptance observations, not measurements of the physical output. Prepared or
crossfade successors installed before their original session binding is available stay
untagged even after commit; bypass batches, encoded passthrough/offload, unknown mappings,
ramps and mixed pieces produce no facts. Coverage of those omitted paths is not claimed.

## Listening sleep boundaries

End-item and chapter policies carry the admitted session, presentation generation, item,
queue occurrence, client playback run and optional native owner. The original run is supplied
at native handover; an older handover without it can play, but cannot arm these end policies.
An end-book policy additionally carries the bounded ordered parts and revision read from the
scoped book timeline. Only an accepted immediate automatic successor in the same queue can
carry its custody. Manual navigation, source replacement, another book or unknown membership
retires the boundary; the same item ID never restores it.

New end policies are refused while an advance, start or uncertain/admitted commit owns the
edge. An accepted policy revokes uncommitted preparation, and a late prepare response cannot
reuse its old policy revision. Native persistence/acceptance must complete before the client
shows an armed timer. An already expired receipt is not armed. Existing minute/inactivity
clocks remain absolute; an expired policy cannot be overwritten by successor promotion.
A source-bound Pause remains available during committed handover, without promoting it;
other guarded source commands refuse that ambiguous transition. These are policy and source
custody rules, not measurements of rendered audio or runtime qualification.
