# Foreground read qualification recipes

This package is selected explicitly by tests and qualification harnesses. No
shipped route uses it. `HarnessDefinitions` returns inert compiled definitions;
the harness must obtain a fresh `plan.Capture` receipt on its actual Reader
configuration and fixture, then compose `stmt.New`, `governor.NewSnapshots`,
`NewBrowseReader` or `NewForegroundReader`. Stored compressed goldens are
comparison baselines, never execution authority.

Trusted composition provides the database incarnation, already-hashed local
token, server time, original deadline, request and separately admitted
`BrowseOutput`. It retains output through encoding and the last consumer. Do
not retire it when a native snapshot ends. The upcoming #1119 LIVE grant is a
separate admission dependency: it must cover all windows and consumer release.
This branch does not import its unmerged implementation or claim LIVE admission.

The shared publication helper performs the full exact shared fence first and
in a fresh final window. Composite reads capture root/asset/link/probe source
values in the first window and compare them at final publication. Middle windows
run business reads only, with no shared or item/source re-fencing. Each window has
at most eight registered calls and uses the original operation deadline. One
whole-operation retry is allowed for source/revision drift; a second drift gives
`ErrStale` plus typed governor Conflict refusal and zero output. Every failure,
current denial or cancellation returns a zero payload. Raw copied values across
all windows and retries are conservatively bounded against retained custody.

Callable recipes:

- `Detail`: direct-child ID keysets, effective locked/matched artwork per role,
  accepted sealed credit-image rows (100 cast/50 crew per window, with raw
  continuation for additional manual credits), up to16 version descriptors and
  optional selected-file streams/chapters. A child or credit tail is explicit,
  including empty non-exhausted windows. Children are direct relations, not an
  eager recursive hierarchy or final product-specific episode ordering.
- `Playback`: selected same-library asset/link, current file path and origin
  witnesses, size/mtime-matched ready probe facts, up to255 streams per type and
  512 chapters. This is a lookup, not a durable start, media capability, complete
  playback options policy, or first-frame measurement.
- `ContinueWatching`: leaf resume candidates from the current profile's own
  `personal_items` activity stream. It reads at most64 raw positions per turn,
  advancing over denied, retired, completed and other-library/kind candidates.
  Ties use equality-tail then next timestamp with the exact index order. This is
  the leaf recipe; container/on-deck projection and retained-merge resolution
  remain separate qualification/cutover work. No write or user fan-out occurs.

The policy envelope is inherited from browse: local principal, supported kinds,
current library age summary, age restrictions on leaves, empty blocked-label
policy, and no account access policy. Unsupported policies/restricted containers
fail closed. These recipes do not qualify missing authorization predicates.

Exact normal counts (including shared/source fences and final validation):

| Recipe | Statements | Physical snapshots |
| --- | ---: | ---: |
| Browse |11–12|2|
| Detail without selected file |17|3|
| Detail with selected file |24|4|
| Playback lookup |19|3|
| Continue watching |11–12|2|

A complete retry may double a recipe's work; all attempts/windows and statement
counts are returned in `BrowseReadResult`. `Elapsed` includes them, admission and
cleanup; browse's `LookupElapsed` remains only its business lookup. Failed,
stale or refused operations must not be counted as fast successes.

These counts and ready/VM/memory targets are **UNQUALIFIED budget debt**. QL owns
paired qualification against B-READS: B-R09 detail, B-R13/B-R14 playback, and
B-R02 for continue watching. Existing12MiB read/8MiB retained-output fixture
funding is prospective custody, not a 2GB fit or maximum-body proof. The test
Reader's64KiB native value/row limit can refuse larger legal descriptions rather
than truncate them; that legal-maximum/decoder composition needs qualification.
No production, hosted or reference-host run is implied by local tests.
