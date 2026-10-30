# portico-disc schema 1

This is the bounded helper/protocol component, not complete disc playback. Linux host compilation against the pinned static public libraries and the generated fixture probes are qualified separately in `fixtures/discs/manifest.json` and `provenance.json`. `server/internal/disc` is the canonical Go consumer. This helper is GPL-3.0-or-later, under the repository licence. Hardware owns canonical target archives, no-decryption patches, notices, packaging, mediatools resolution and the bundle verifier. Never link, load or ship libaacs, libbdplus or libdvdcss. A pristine-library host build does not qualify the no-decryption closure; the hardware-owned checked patches and target tests are required.

`portico-disc probe <absolute path>` writes one JSON object and newline on stdout. Kind is `bluray` or `dvd`; packaging is `iso` or `folder`; encryption is `none`, `aacs`, `bdplus` or `css`. Encrypted results contain an empty titles array. There are no paths, URLs, credentials or arbitrary library strings in the response.

### Captured remote ISO probe foundation

On Unix, `portico-disc probe-image-stream <size>` reads an ISO through inherited
descriptors only. `<size>` is the original positive, 2048-byte-aligned extent;
it is not a locator or an acquisition request. Descriptor 3 writes requests to
the parent; descriptor 4 reads replies. The helper has no network permission,
URL, provider path, credential or source-directory access.

Each request is exactly `PDR1`, an unsigned 64-bit big-endian byte offset and an
unsigned 32-bit big-endian length. A response is `PDA1`, the exact unsigned 32-bit
big-endian length, then that complete range. Individual ranges are at most 2 MiB.
Channel closure, bad framing, truncated responses and out-of-extent reads fail
the invocation. Library callbacks use `bd_open_stream`, `DVDOpenStream` and
`dvdnav_open_stream`; they do not concatenate clips or resolve local paths.
The DVD seek callback uses byte offsets and returns 0 on success, as required by
the pinned libdvdread source. Read callbacks return bytes for DVD and blocks for
Blu-ray. DVD reader and navigation cursors remain separate.

The Go `disc.Tool.ProbeRemoteImage` caller must already own one captured
`storage.RemoteObject` with real immutable version evidence and finite random
access. The broker uses that same object's `OpenRange` and original context for
every request, retaining the adapter's conditional representation, source and
configuration checks. Size/mtime alone, a latest-source lookup or a sequential
object is insufficient. The complete invocation has one 20-second deadline,
at most 1024 requests and 16 MiB, including library fallback. Each range retains
the existing 15-second read bound. Only a complete range is framed as success;
the final probe is discarded unless the original object still observes the
same snapshot. The caller retains ownership of the object; all pipes, process
and broker goroutine belong to this invocation.

This foundation does not enable remote disc inventory or playback. Production
title-stream/seek integration, DVD streaming, BDMV file-open callbacks and
production admission are still required. Existing scans continue to report the remote-disc
unsupported message until the full stream-mode qualification. No fixture,
binary, source bundle or playback acceptance is established by this source.

### Captured Blu-ray ISO title stream foundation

`seek-image-stream <size> <playlist> <requestedMs>` returns the existing seek
JSON over the inherited range broker. It uses the same selected-playlist,
first-angle, minimum-duration, encryption and actual `bd_seek_time` /
`bd_tell_time` checks as local seeking. There is no requested-time-as-measured
fallback.

`stream-bluray-image <size> <playlist> <requestedMs> <byteOffset> <titleTimeMs>`
repeats that positioning and requires the exact original landing result before
publishing any title bytes. Only libbluray reads the selected title, including
its playitem/branching order; no loose clips are assembled. The output is the
actual selected-title byte stream. Source, broker, write and library failures
produce a nonzero exit; successful EOF requires the reader's real EOF.

The Go `Tool.StreamRemoteBluRay` requires the original structure, exact selected
title and a non-nil original-admission checker. It owns a copy of the structure,
compares a fresh probe against that original without adopting changes, and
obtains the real landing before opening the stream. The checker must retain
the original actor, source, selected title/facts, plan, session/run and caller
lifetime. It is called before source reads and again before complete ranges or
output buffers are published. All bytes continue through the same original
immutable `RemoteObject`; no latest-source or replacement-object lookup exists.

Stream I/O work is bounded by image size times the selected playitem count plus
two extents for reader overhead, with checked arithmetic and a finite request
bound. This is a work limit, not a measured title size or bitrate. The original
owner context retires the stream; each source read retains its 15-second bound.
The process, both broker pipes and wait goroutines belong to this invocation.
Close cancels them and joins them. The caller closes the original source object
after closing the stream.

The returned reader does not expose a raw descriptor: it buffers a bounded
read until the original caller is checked again, and never copies retired
bytes to its consumer. EOF waits for successful helper completion, a final
same-snapshot source observation and the original admission check. A failed
producer is an error, never a successful end-of-title. Future FFmpeg composition
must consume this checked reader and propagate its error/cancellation to the
original decoder invocation; bypassing it with the underlying pipe is not a
valid composition.

This is source foundation only. No remote catalogue, plan, session or decoder
adapter invokes it yet. DVD title timing/streaming, remote BDMV member callbacks,
normalized source/policy bindings and full playback/seek qualification remain
unfinished. Existing remote-disc admission stays unsupported.

Each title has `discOrder` (zero-based retained disc order), `durationMs` (integer title-relative milliseconds), `angleCount`, `clips`, `streams` and `chapters`. Blu-ray adds `playlist` (integer 0..99999, zero valid) and `playlistFile` (`NNNNN.mpls`). DVD adds `title` (one-based), `vts` and `pgc`. An array is always present, even when empty. Discs with no titles of at least 60000 ms return an empty array; that is not a playable source.

Blu-ray uses TITLES_RELEVANT with a 60-second minimum. DVD considers all titles and filters by their library-reported duration. The Go projection performs stable descending duration sorting; equal durations retain disc order. The displayed name is the sorted one-based `Title N`, independent of the separately persisted owner's main-title choice. Main-title changes never rename or reorder versions.

A clip has `sequence` (zero-based), `file` (`NNNNN.m2ts`), `inTime90k` and `outTime90k` (playlist clip clock at 90000 ticks/s). These are structure facts, never a server instruction to concatenate paths. A chapter has `startMs` relative to the title; the next start or title duration supplies its end. Chapters reach FFmpeg as an inherited ffmetadata pipe, with `TIMEBASE=1/1000`.

A stream has `clipSequence`, `pid`, `kind`, `coding`, `language`, `dolbyVisionEnhancementLayer` and `dolbyVisionEnhancementKnown`. Blu-ray returns every primary, secondary and Dolby Vision EL STN entry in clip order, deduplicated by PID within each clip; language is ISO 639-2 or empty. `coding` is the native STN coding byte. DVD returns enabled video/audio/subpicture presentations; `pid` is FFmpeg's elementary/private-substream ID and `coding` is the IFO audio_format or video MPEG version code. DVD's language is the two-letter IFO code or empty. A TrueHD stream and its AC-3 presentation inherit the same STN PID language, not the ordinal of a differently filtered FFprobe stream list.

A false enhancement flag with `dolbyVisionEnhancementKnown=false` is **unknown**, never proof that a video PID is a base layer. The pinned libbluray DV STN table supplies genuine EL membership. The helper also reads at most 1 MiB per selected playitem through libbluray, never by assembling loose clips. A valid current PAT/PMT, CRC, profile7/compatibility6 EL descriptor and same-program HEVC base PID can add `dolbyVisionBasePid`. Generic private-PES EL entries additionally require DOVI registration and an HEVC video descriptor. Fragmented PSI is assembled within fixed bounds; a material table change clears the dependency facts. STN EL membership without an observed PMT dependency leaves the base PID absent. The helper does not guess a neighbouring PID, associate by array position, validate RPU/pictures, or enable a join. The separate checked FFmpeg join requires its own exact-bundle torture gate and production composition. Until that path is qualified, a split-PID title plays HDR10 with an explicit Dolby Vision loss. No FEL residual reconstruction is claimed.

`portico-disc seek <absolute path> <playlist> <ms>` is Blu-ray only. It selects angle zero, seeks using libbluray, and writes schema, playlist, requestedMs, byteOffset and titleTimeMs. Offsets are bytes in the selected title stream, not filesystem or concatenated-clip offsets. titleTimeMs is the library-reported landing time and must not exceed requestedMs. The server maps that clock and applies `-skip_initial_bytes` to `bluray:`. DVD uses its demuxer's `-ss`, angle1 and preindex0. Cross-clip/cell one-GOP landing proof remains required.

Local execution: mediaexec, no network, one read-only path (ISO or the folder holding BDMV/VIDEO_TS), no output files, deadline20s, stdout limit1048576 bytes. Output is buffered by the helper; an overflow is an error, never truncated successful JSON. The JSON output uses a dedicated descriptor established before opening any library. Direct library stdout is redirected to stderr, because some DVD library builds print outside their logging callbacks. Stderr is private diagnostics, never wire data. A process error publishes no successful result. Helper qualification or detected library presence does not establish complete runtime media support.

Exit codes: 0 complete JSON; 2 invalid arguments or unrecognised input; 3 unreadable/malformed/library operation failure; 4 output/structure bound exceeded; 5 stdout write failure; 6 encrypted seek refused. The Go caller also treats malformed JSON, unknown schema/keys, mismatched seek authority and deadline/overflow as failures. Encrypted probe results use exit0 so the server can emit the exact Files skipped message: “Not supported. Portico can’t play encrypted discs.”

CSS gap: the current WIP has a bounded sector-prefix scrambled-PES detector and no decryption dependency. That is not proof that later sectors are unencrypted. All media reads must reject scrambled packets, and encrypted-disc fixtures must qualify both the scan and playback refusal before intake. Header/API, bounds, PGC/cell transitions, CSS classification and stdout purity remain review and runtime proof obligations.

Remote mode is required, but not implemented or enabled in this checkpoint. It depends on the remote owner's checked combined disc-root/listing/session fence and range producer, currently unavailable. The proposed private duplex inherited-fd broker carries bounded stat/list/read operations and bytes, never provider credentials/URLs. The server owns immutable source/root/session generation authority before and after each I/O. It may not invent an immutable Version from an rclone ETag or replace the revision while reconnecting. The C reader will use bd_open_stream/bd_open_files and dvdnav_open_stream; their pinned public headers must be qualified first. Remote scanning uses the exact interim message: “Not supported yet. Portico can’t play discs from remote folders.”

The disc producer, scan/normalized publication, title/version/main-title selection, FFmpeg source fence, chapter/PID composition, split-PID DV join and remote reader are all required. This helper component exposes no new catalogue eligibility, playback availability, UI route or scan classification by itself. Complete feature acceptance remains separate.

`schema1.json` documents the structural wire shape. The Go decoder additionally enforces kind-specific keys, playlist filename equality, unique title selection, monotonic chapters, clip boundaries, unknown/enhancement consistency and exact seek authority. Cross-field rules must not be replaced by JSON-schema validation alone. Production relational publication, source/member replacement fences, title-specific media facts, version/owner mutation and actual FFmpeg composition remain separate engine work. The input argument helper alone is not those production effects.

Generated fixtures are in `fixtures/discs`, authored by `scripts/generate-disc-fixtures.py`. The hardware verifier envelope is schema1 with `fixtures[]` entries containing exact id, relative path, every file SHA256 and observed `expected` probe JSON. The three ids are `bluray-two-clip-bdmv`, `dvd-two-title-video-ts` and `bluray-udf250-iso`. Actual durations and chapter/clip boundaries are preserved: the requested DVD chapter33s is authored at33.120s, and the Blu-ray split is at37s with total72.041s. The seek test therefore requests37.5s, after that actual boundary, and lands at37s. No playlist name is public UI copy. The ISO's real logical volume descriptor has the OSTA UDF domain and revision0x0250; its name is not the evidence.

Run `PORTICO_DISC_TEST_BINARY=/absolute/helper go test ./internal/disc` from `server` to include the real sandboxed fixture probes and seek. Without that binary the helper tests explicitly skip; parser/inventory tests still run. Synthetic scrambled-PES flags prove detection/refusal only. Real encrypted discs, multi-angle/complex PGCs, target runners, decoded seeking, DV join and remote streaming retain their separate acceptance obligations.

Supplemental procedural STN/PMT fixtures are in `fixtures/discs/dovi/manifest.json`, generated by `scripts/generate-dovi-disc-fixtures.py` from the qualified procedural HDR inventory. They retain the original three-fixture verifier envelope unchanged. The actual tsMuxer-authored STN-only disc has an EL entry and no explicit PMT dependency; its paired authoring variant adds an explicit dependency descriptor only inside existing TS stuffing, with CRC updated and all PES/packet positions untouched. Their small picture geometry is deliberately not UHD-disc conformance proof. Compile `tests/mpegts_dovi_test.c` as C11 with warnings as errors and run it without arguments for bounded PSI transport tests, or with `FILE PACKET_SIZE EXPECTED_BASE_PID` for actual short transport fixtures. These tests add no runtime capability.

An optional private `videoRole` comes directly from the pinned libbluray STN category (`primary`, `secondary`, `enhancement`) for DV-bearing clips. `Title.DolbyVisionCandidate` requires exactly one primary HEVC and one explicit EL in every ordered clip, with unchanged PIDs and no conflicting PMT dependency. It does not choose a first/largest/adjacent video track and does not turn that candidate into a known PMT relationship, capability or delivered join. Actual RPU/picture/clock qualification and runtime composition remain required. `FFmpegInput` also rejects any title whose complete facts are not exactly present in its probe, even if an arbitrary playlist/title number would otherwise parse. This membership guard is additional to the caller-owned live source/version fence.
