# Inventory and scan lifecycle

The scanner uses the existing source-access registry, supervised storage helpers,
SQLite work gate, title catalogue and playback ownership. Filesystem/network
reads never run inside a catalogue writer transaction. A blocked mount stays in
a killable child and cannot hold the server's only scan worker.

## Files, titles and evidence

A source has a stable ID, configuration generation, root incarnation, admitted
root identity and owner confirmation. Source replacement requires the existing
explicit acceptance. Every page/result rechecks source, incarnation, generation,
job status and policy before publication. Root escape, protocol, byte, depth,
process and output limits remain enforced.

Inventory objects are locations, assets are physical files, and titles are
logical entities. Shared folder/filename/embedded evidence is read relative to
the source root. Meaningful file evidence wins contradictory folder evidence.
Matching receives typed title/year/ID/edition provenance; provider details still
validate candidates. Audio tags outrank path hints, and the canonical disc-folder
reader supplies untagged disc context.

A second copy attaches as a version through the catalogue's title identity.
Version links carry edition and split-part numbers. Exact same-file moves keep
asset/location identity and re-read their interpretation; explicit owner
placement stays pinned. Episode zero and stacked episode parts are admitted
within existing numeric/multi-episode bounds. Extras have one identifiable title
owner. BDMV/VIDEO_TS folders are represented once as unsupported disc titles;
standalone extracted streams still use ordinary media handling. No disc menus,
decryption or archive expansion is implemented.

Observed revisions are invalidation evidence, not content hashes or playback
byte-version grants. Device-renumber handling remains in storage/title identity;
strict playback source pins still apply independently.

## Durable traversal and tolerance

One active claim exists per source, including paused work. Manual, scheduled and
reported scans coalesce; reports received during active work remain durable for
the next pass. Phases are inventory, catalogue-only asset adoption, extras,
verification, missing reconciliation, sidecar reconciliation, analysis enqueue,
basic/deep execution and terminal completion. The existing adoption phase uses
the internal `legacy_inventory` name; it is not a database upgrade path.

A quantum commits a bounded page or unit together with its cursor. Local pages
contain 32/64/128 entries under gentle/normal/fast; the hard page maximum is 128.
Source concurrency is 2/4/8, with one quantum per source. Urgent continuation
runs as storage allows and yields through foreground work pacing. Playback from
the same source adds a bounded delay between analysis units; it does not cancel
or permanently suspend the backlog. Helper CPU/I/O priority remains effective.

Local scans list file facts every time. Directory timestamps alone cannot prove
that a file was not replaced or edited in place. Managed adapters retain their
paged listing/cursor protocol. Portable local listing retains a bounded readdir
stream in a supervised child. Lost/changed continuations protect that folder,
retain already committed observations and let other folders reach analysis.

Unreadable/changing subtrees, disappearing entries and unsafe/dangling links are
isolated and recorded. Those paths and their descendants never authorize
absence. A changing folder is queued for another limited pass. A root that cannot
be admitted/read still stops the source. Duplicate directory identities are
skipped before visited-index updates and protect prior alias coverage.

Hidden/system/sample rules and bounded .plexignore/.ignore configuration apply
during the walk. Invalid lines are skipped individually; excessive diagnostic
output is summarized. Configuration reads use retained-root containment and
existing helper/request budgets. Managed adapters identify the two configuration
names in their completed staged listings, keep an inherited scope cache bounded
to 128 scopes/512 KiB per run, and leave provider commit pages intact. Ignored entries are deliberately reconciled as
missing, rather than protected as an access failure.

Only complete stable coverage judges a file missing, with the nearest listed
ancestor rechecked outside the writer immediately before publication. Missing
is immediate; no second observation/grace period remains. Ordinary scans retain
empty-root protection. A paths-changed/native report can observe last-file
removal in a readable unchanged admitted root; an identity rebound retains the
empty-root safeguard. Missing titles follow the owner's existing removal rule,
and retained state is restored through the title identity model.

## Analysis and local changes

File List Only reads directory/stat facts and bounded ignore configuration; it
opens no media, NFO, artwork, subtitle or STRM contents. Basic uses bounded probe,
local metadata and subtitle stages. Complete/Custom select permitted deep stages.
Policy changes preempt obsolete reads and select missing permitted work.

Analysis rechecks the file before and after reads. A failed probe keeps its
name-based title and produces an owner issue. A changed file schedules another
folder observation. Source/configuration/policy/cancel fences still decide
whether results may publish.

Normal enqueue selects incomplete revision, policy, operation, algorithm,
generation, deep-head or enabled STRM-target evidence. It writes only pending
work rather than a done row for every available file. Completed deep facts stay
reusable when a sidecar changes. Disabled STRM analysis stays deferred without
recurring per-run work rows.

Sidecar observations persist path/revision and last-seen job. Complete trusted
coverage detects removal; partial/outage coverage preserves evidence. NFO/art/
subtitle changes invalidate affected media, including child seasons/discs under
parent metadata, in bounded batches. Negative directory listings are checked
against their revision; positive artwork caches are invalidated on changes.
Complete artwork observations release automatic carriers/selections backed by
removed/replaced sidecars, preserving owner picks and retained content-addressed
bytes. Embedded video names/IDs are normalized revision-bound evidence, separate
from sidecars and technical probe facts.

## Monitoring, history and consumers

Monitoring defaults on per library (`settings.monitorChanges`). One supervised
helper provides inotify, ReadDirectoryChangesW or FSEvents hints; non-cgo macOS
uses kqueue. Maximums are 64 roots, 4,096 directory watches and the existing
64 KiB helper request. Events debounce after two seconds quiet or ten seconds
continuous change and feed the ordinary ReportPaths path. Overflow collapses to
root observation. Network/unavailable-watch fallback runs every five minutes;
local monitored sources have a fifteen-minute safety inventory. Native hints
are never authority for absence. Monitoring off leaves manual reports/scans and
explicit source schedules available.

Terminal directory/analysis/listing work is deleted in chunks of at most 256
after fifteen minutes. Remote staged listings retire in the same bounded chunks;
one newest ready snapshot per current cached directory supplies inherited ignore
configuration, and superseded generations retire. Twenty terminal run summaries remain per source; older
runs/diagnostics are dismantled in bounded chunks. Current directory/sidecar
caches retire removed/excluded paths and obsolete incarnations. Integrity cadence
is stored on the source, not dependent on unbounded run history.

The owner change feed retains up to 10,000 entries per source or thirty days.
Sequences are global and can have gaps. The typed changes response adds
oldestSequence/resetRequired; an expired nonzero cursor requires reading current
inventory rather than assuming a complete change history. Active/paused runs are
never pruned.

Owner GET /v1/admin/libraries/{id}/scans/{job}/issues pages bounded path, reason,
line and string-catalogue message keys. Viewer scan status and scan events remain
path-free, with existing authorization rechecks and publication pacing. No
parallel credential/player system or viewer revision redesign is introduced.
