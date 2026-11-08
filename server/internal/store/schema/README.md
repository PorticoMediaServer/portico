# Schema v1

`SQL()` and `SHA256` expose the exact change-controlled bootstrap schema. `schema.sql`
matches canonical `planning/v3/schema-v1/phase0/SCHEMA.sql` byte for byte.
`Contracts()` pins the companion artifacts, `ReadContract(name)` returns their
bytes, and `SHA256SUMS` supports external harnesses. `INT-INDEX-POLICY.json` is
the shared partial-predicate authority. Contribution SQL is already included in
the full schema; do not execute the fragment separately.

INT owns this package and the canonical DDL. Every DDL or contract change must
update the canonical source and embedded copy together in the same reviewed
change, with the reason and R review. The DDL stays under change control until
the phase-1a go/no-go passes, then locks. Other store lanes consume these bytes
and predicates without editing them.

For a fresh harness file, open an exclusive bundled-SQLite connection, configure
its resource/WAL policy and `foreign_keys=ON` through DSN/connector settings on every physical connection, then call
`InitializeEmpty(ctx, conn)` before starting any runtime connection users. It
installs atomically and refuses every non-empty database. It opens no handles,
adds no seeds and performs no migration. Runtime write authority stays with the
private typed writer. This package is not yet wired into server startup.

For a restart, call `AssertExisting(ctx, conn)` on the exclusive opening
connection before starting runtime users. It requires enforcement and the pinned
bundled engine, then compares every native `main.sqlite_schema` object against
`NativeLayoutSHA256` and `NativeLayoutObjects`. Missing, modified, extra and
legacy layouts fail closed. It executes no bootstrap DDL, ANALYZE, migration,
repair or seed. It does not inspect user data, prove its integrity or authorize
access; those are separate startup responsibilities.

The adopted010 file header is explicitly `application_id=0`, `user_version=0`
and `encoding=UTF-8`. The zeros preserve the reviewed bootstrap; they are not
layout identity markers. Verification checks them without rewriting them.
Fresh bootstrap refuses incompatible headers rather than adopting them.

Connection enforcement has one authority: `sqliteconn.AssertEnforcement` checks
FK=1 and ignore_check_constraints/defer_foreign_keys/writable_schema/
read_uncommitted=0, returning typed evidence without repair. Both bootstrap and
restart verification use it, and full `sqliteconn.Assert` calls it before the
resource/durability checks. The connector pins every flag on every physical
connection. Schema owns native objects and persistent header/encoding; it does
not duplicate Config or choose WAL/cache/synchronous policy. Zero canonical
triggers make recursive_triggers irrelevant to this layout; permissions,
registered functions and private statement custody remain separate contracts.

The fingerprint recipe is `portico-native-layout-sha256-v1` plus NUL, then
length-prefixed schema SHA and SQLite version, followed by every object sorted
by BINARY `(type,name,tbl_name)`. Each tuple contains those three length-prefixed
UTF-8 strings and SQL: byte 0 for NULL, or byte 1 and the length-prefixed native
SQL text. Lengths are unsigned 64-bit big endian byte counts. It includes native
autoindexes, FTS shadow objects and views; table contents and physical root-page
numbers are excluded. This proves the exact stored native layout, not the bytes
of an unavailable historical bootstrap script. New indexes, statistics tables,
bank declarations or engine changes require an INT-reviewed pin update. Runtime
code must not create unregistered layout objects. DDL/engine changes regenerate
the pins from a fresh canonical bootstrap; the restart and mutation controls
must pass on the new exact bytes. No version PRAGMA or compiled SQL hash alone
substitutes for this existing-file check.

INT-CHANGE-001 adds scan/search/enrichment operation receipts and acknowledgement
floors, per-image search seal/reclaim custody, exact own-play quarter/day/month
and exact playing-span edges, a bounded pending occurrence cursor, and six
admin-only event/cohort time access paths. It is adopted after R review.
Receipt result IDs do not pin retired mutable work resources;
private writer commands enforce immutable results, complete CAS and acknowledgement
ordering. The package initializer does not implement those commands. No performance pass follows from these storage contracts.

INT-CHANGE-002 is adopted at5f3c7f67 after independent R agreement. Credits
use staged effective images and a same-entity head, with verified small deltas.
Retained owner/source cohorts have exact incarnation/revision provenance;
metadata_repair_cascades owns single-target authored input and immutable command
receipts, and image headers own bounded retirement through final acknowledgment.
The provider100cast/50crew selector is not a manual-credit cap: preserve current
512explicit/2048normalized repair input and use complete bounded staged
preparation/pages. No provider re-trim of authored decisions is permitted.
The six artifacts and native-layout fingerprint move together; actual writer,
producer, crash/replay,1M/both-profile,quiet-max,2GB and R-09b gates remain open.
PB witness/next-bank changes remain separate later reviews.

INT-CHANGE-003 is adopted at abf43774 after independent R agreement. It adds
only the closed InventoryRefuse receipt variant and eight typed binary-safe
path/cursor fields. FIRST execution is Linux stable singleton
filename_unrepresentable only; future cursor tags are representable, not
authorised for execution. The schema gate opens after this embed merges, while
R-26 diagnostics, R-27 byte debt/retention/bounded cleanup and R-28 correct native
protection from escaped diagnostics remain gates on the actual Refuse command
and qualification. No source acceptance, EOF, availability or physical pass is
implied by a refusal receipt or by this embedding.


INT-CHANGE-005 is adopted at 45c96755 after independent R agreement. It adds
operation-to-evidence-build custody, captured actual owner incarnation/generation,
a single active root per owner and bounded owner/state discovery indexes. Provider
operations remain the actual command/receipt owners; the build is not a new job.
Original receipt resolution precedes fresh checks. Owner generation is historical;
fresh commands still verify the full current source/provider/entity/root vector.
ACK-before-detach and discoverable charged retirement debt remain mandatory.
The evidence-normalized-charge-v1 recipe counts actual persisted TEXT/BLOB bytes;
resource limits are trusted runtime policy, not DDL caps or performance evidence.
The companion contracts retain their reviewed proposal-era provenance verbatim;
the adoption supersedes their old status qualifiers. No CHANGE-004/native banks,
006 diagnostic cleanup, accepting result wire, runtime connection, seed or
migration is included. W result/receipt semantics, fresh predicates, ACK/reclaim,
resource enforcement, crash and runtime/physical qualifications remain open.


INT-CHANGE-007 is adopted at09726d2b after R agreement197. It adds only
account-owned denied labels, schedule windows and channel rules, with owner/ordinal
primary keys and NO ACTION to access_limits. Caps remain writer-enforced; every
accepted replacement fences the whole policy through its parent revision. Active
policy edit/removal is atomic FULL; policy_present=0 is not an inactive cleanup
fence. Channel IDs are opaque policy facts without a mutable-inventory FK. The
reviewed contracts preserve their historical provenance wording verbatim.
The separate CHANGE-008 security command owner/result/ACK/floor/lifetime protocol,
actual W/RD enforcement and label producer/verifier, trusted clock/timezone,
complete budgets, crash/runtime/physical/2GB and security-cadence qualification
remain open. No read policy is enabled, no seed/migration runs and no runtime
cutover is included by this embed. Existing005 files fail the changed layout pin;
test-server cutover creates a fresh file under the approved no-conversion rule.


INT-CHANGE-010a is adopted at14acdcd7 after R agreement207. It adds immutable
original run scope and stable creation identity/digest, typed zero-member
create/claim receipts and two run indexes, reusing the existing native lease,
generation, revision, nonce and last/ACK fields. Lifecycle source_revision is
SQL NULL; existing kinds retain required TEXT. The actual root directory
cursor is its named scalar result, never an object-revision/copy-slot alias.
FIRST includes V2 fenced same-owner LIVE renewal under exact current ownership,
settled proof, physical old-work exclusion and whole old/new deadline coverage.
Expiry monotonicity is a writer-code rule, with no DDL constraint preventing a
lower replacement expiry under separately reviewed epoch recovery.

This embed opens the storage gate only after merge; it does not issue a driver,
enable fresh commands, seed/migrate existing files or authorize startup cutover.
Creation-only absence/current issuer/same-file/no-GC proof, separate closed
wire and kind/NULL-before-string decoder, physical old-work exclusion and full
resource/time/Capture/profile/terminal/crash/runtime qualification remain open.
ACK's zero-member lifecycle cleanup is bounded/floor-only with no refund;
historical lease echoes and stable create fields cannot reconstruct Applied or
fresh authority. Directory/EOF/frontier/refusal/Basic and006/009 remain separate.
The native layout fingerprint moves with these exact six artifacts. Existing007
files fail the new layout assertion and remain untouched; approved test-server
cutover uses a fresh file, with no conversion or compatibility shim.


INT-CHANGE-008 is adopted at debd5c77 after R agreement211. It adds retained
security command identity/results and exact playback capture/drain receipts,
original membership/profile births, occurrence/START custody and a bounded
acknowledged cleanup slot. The authorization view binds those native births;
equal ID strings cannot revive an old family. The six artifacts are verbatim
canonical5454be94 bytes, including the complete type and release-index contracts.

This embed opens the storage gate after merge. It does not enable the waiting
security/progress commands, mint W's internal pending capability, migrate or seed
existing files, or authorize runtime cutover. Current authority, typed wire/codec,
producer thresholds, bounded event retention, complete admission and physical
cleanup, crash/replay and runtime qualification remain implementation gates.
The native layout fingerprint moves with all six artifacts. Existing010a files
fail the changed layout assertion and remain untouched; approved test-server
cutover uses a fresh file with no conversion or compatibility shim.


INT-CHANGE-009 is adopted atf83d526e after R agreement215. The six artifacts
match canonical phase0 at internal e5dd62960c6a787b281efe7acd8ca878ceaeb414.
Distinct evidence_seal/credit_source_seal kinds and seal_version (no seal_purpose)
use actual provider-operation receipts; SOURCE has operation-owned custody and
R/NEXT, evidence keeps its own cursor/revision. Raw SourceOrdinal and cast
Billing are separate native facts. No new table or codec is introduced. Typed
proof references preserve source/result/image lifetime; parent member cleanup
is explicit child-first NO ACTION.

PINNER-PAYS-FULLk10, selected-only candidate retirement and the writer-only
64KiB aggregate retained control budget remain binding. P_max8,876,032B;
per-title88,760,320B; global17,752,064*T+71,008,256B. Evidence151rows/6MiB,
source tuple caps and32MiB outer request remain distinct. These record/index
bounds are not an allocator/RSS/WAL or physical qualification. W owns the ONE
future normalized codec; EN supplies independent spec-derived vectors. Actual
authority/producer/ledger/resource/receipt/ACK/cleanup integration and runtime
qualification stay open. Existing008files fail the changed fingerprint; this
package performs no migration or compatibility repair. No accepting command
or runtime cutover is enabled by this embed.


INT-CHANGE-010 is adopted at da7841e2 after R agreement219. The six artifacts
copy merged canonical90ee1ca1 verbatim. Four distinct directory purposes retain
actual native directory/parent/run scalar results, SQL NULL source_revision and
zero catalogue members. Directory states close to pending/done/blocked; bounded
admitted-frontier completion preserves authority0. EOF requires genuine stable
ZERO-RAW proof; unsupported raw entries and aliases stop intact. One invalidUTF8
directory component has one protected issue, with no descent/per-descendant issue.

All four directory ACKs are floor-only, with no header auto-delete or refund.
The same-run/operation/entry-kind NO ACTION pin survives ACK; physical cleanup
is child-first after original consumers finish or durably cancel. Fresh
wrong-type/same-path checks follow original history before effects. The full
parent UNIQUE index costs every receipt; a zero-member result is not a cleanup
permission. The narrow010a create/claim cleanup remains separate.

Only the merged product embed opens this storage gate. Actual W driver, issuer,
wire/codec and complete admission/custody, SC traversal bodies, R26/27/28/006
consumer/debt ordering, crash/runtime/physical qualification remain open. This
embed enables no command, migration or startup cutover. Existing009 files fail
the changed native-layout assertion and remain untouched; approved cutover uses
a fresh file with no conversion or compatibility shim.


CHANGE-012 and CHANGE-013 are adopted. The six artifacts copy canonical phase0
at internal 70953472cc725153d6e9df1250baf455eff1ddf7 verbatim, SQL9d9aa39d. CHANGE-012 retains actual guide/
library-channel/DVR owners, typed receipts, fixed cleanup slots and typed final
floors. It adds seven relations and changes only G parents; R-29 remains the
tracked follow-up in internal254. External-guide generation reclaim stays
disabled until the actual MP physical-session pin and exit/reader release seam
is adopted and embedded. No new codec or current-authority permission follows.

CHANGE-013 adds the independent People search_incarnation BLOB16 row birth and
the partial album_id/id inverse index, preserving the disc/track detail index.
Writers preserve birth on updates, use existing real People revision CAS and
read actual generated birth before binding a source intent. Artwork/token/ID
alone cannot replace it. Equal-ID recreation is a new birth. Readers still owe
actual bank/profile/accepted-publication/current-visible-work proof and bounded
ID-desc raw keysets; no serving path is enabled by the schema copy.

The native layout pin is 7c97f22e/2136 objects/708 tables on SQLite3.53.4. Existing
011 files fail exact layout verification and remain untouched; no migration,
startup repair, runtime cutover or phase2 A–F adoption is supplied. Actual
writer/read source, receipt, custody, resource and physical qualification gates
remain. Schema-dependent captures and exhaustive row models must match this
combined layout; unknown drift stops the embed.


CHANGE-014 is adopted: three exact sparse personal flag indexes and
notification_ack_heads.read_through_ms. The six artifacts copy canonical
phase0 verbatim. Flag membership is an own-profile BLOB public-ID raw keyset
before current authority/visibility checks; it is not an authorization index.
An individual notice read keeps its stored time; a bulk-only notice reports
the current covering read-through time, and zero means no stored bulk time.
No bulk-history relation or recipient fanout is introduced.

Existing012+013 files fail exact native-layout verification and remain untouched.
This bootstrap adds no migration, startup repair, runtime command or product
cutover. The #1556 shared schema baseline is regenerated once per embed; only
actual changed statement/VM/decoder plans require owner recapture. Writer
receipt/authority/custody and hold/physical qualification remain separate gates.
