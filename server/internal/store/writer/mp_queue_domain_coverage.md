# MP queue, next and reading implementation draft

Base: exact `1fd526adbe6d782132134c5a06149ace221d0b59`. All changes are new
MP-owned files in `store/writer`. No legacy, shared registry, DDL, descriptor,
read, WC, security or personal writer changes. No product registration. This is
domain implementation evidence, not publication approval or qualification.

## Implemented components

| Purpose | Private implementation |
| --- | --- |
| 750 | `mpBeginQueue`: bounded birth of staging queue and 1–16 immutable snapshot descriptors; original per-snapshot birth values returned separately from subsequent queue sequence |
| 751 | `mpStageQueueChunk`: fixed-width compatible, owned 1–256-key chunks; exact expected built/count CAS, complete intermediate chunks, final partial chunk only, insert-only chunk identities |
| 752 | `mpSealQueue`: complete-prefix seal, ordered segment attachment, staging-owner promotion; no current result reconstructed on replay |
| 753 | `mpEditQueue`: sealed snapshot append at end/before/after, range splitting, tombstone removal, plain move to a one-item immutable snapshot, shuffled move preserving the original occurrence through explicit spans |
| 755 | `mpShuffleQueue`: current occurrence anchored at position zero; public four-round keyed permutation and inverse; unshuffled append tails; disable continues in source order; explicit no-op when already off |
| 756 | `mpRetireQueue`: current owner/revision/custody CAS; same no-effect answer for absent/foreign queue; record exact current and prepared media stop obligations, detach session, retire logically |
| 757 | `mpRetainQueue`, `mpBeginQueueSave`, `mpReadQueueSaveSlice`, `mpFinishQueueSave`: park custody without continuing old presentation; selected-order digest; <=48 entries per save slice; VALUES-ONLY append boundary to UO; cursor advances only after same-turn actual accepted count |
| 758 | `mpCleanupRetiredQueueStructure`, `mpCleanupQueueSnapshot`, `mpRecordQueueStopExit`: <=48 point deletes per child class; no arbitrary cascade; drain segments/spans before snapshots, chunks and removals; keep root, receipts and stop debt for parent lifetime protocol; trusted native cleanup can run after family revocation |
| 759 | `mpBeginQueueSnapshot`: separately stage an append snapshot without changing the selected queue before it is sealed/attached |
| 760 | `mpPrepareAdvanceSelection`, `mpAdvanceOptionItem`, `mpAcceptAdvanceSelection`, `mpFinalizeQueueAdvance`: next/previous/named/completion/repeat edges, <=200 withheld-entry skips, pending distinguished from absent, lap-specific tombstone cuts, exact SAME-turn selection; parent installs actual successor, then finalization verifies that native session and records the predecessor stop |
| 761 | `mpPrepareQueueNext`: original next birth projection; exact queue revision/custody/current entry, predecessor logical generation/state/lease, selected item and next position/lap; exact private media generation, original family, inventory revision and source/config/pin witnesses; deadline bounded by 60 seconds, family horizon/token and predecessor lease |
| 762 | `mpCommitQueueNext`: fresh CAS against all captured witnesses; return canceled outcome plus stop obligation for expiry/queue/session/source drift; mark preparation committed before canceling competing preparations; exact session-transfer VALUES for parent's core recipe |
| 763 | `mpCancelQueueNext`, `mpRevokeQueueNext`: idempotent logical cancel; native original-family revoke/deadline cleanup; no personal evidence and no physical resource release |
| 764 | `mpRecordNextPhysicalExit`: stop obligation can finish only with matching native media/generation exit evidence, never a caller-provided exited boolean |
| 770/771 | `mpReadingIntent`: source/publication CAS and format-specific location/clear/reread proposal; native reading revision check; exact item/public ID, asset, source, object identity/revision, incarnation/config, size/mtime and prepared revision; EPUB CFI/spine vs PDF/comic page/progression; captured source context survives to final finish; history pause returns unchanged proposal; UO remains the only personal/history/revision/root writer |

754 is neither declared nor used. All top-level effect functions reject a turn
whose actual command purpose does not match. No existing purpose is borrowed to
execute these commands. Every typed native release uses W's `finishCommand`.
Effects have no Applied, COMMIT or ACK state. All error paths require rollback
of the complete parent command, including earlier domain writes.

The structural bounds are 1000 segments, 10000 tombstones, 1000 selected-order
spans and 10M raw source positions. Layout allocation has an actual native byte
preflight; snapshot, chunk and reading source text are point-preflighted. The
worst span replacement is bounded to 2000 small row effects plus the limited
segment delta. This has **not** been measured against the normal hold budget.
Physical exit does not delete pins or return governor permits here.

## One consolidated component-needs packet for parent F

Compared only to the implemented component fields in
`changes/F-media/MP-001.sql`, SHA-256
`fb9f78bdd335c5b0731a6a0af7f2d9ab9e7e421dfa2dbfdcc4445f22233ca501`.
This is not another owner/index/lifetime audit and makes no DDL edits.

**Already available in F:** queue/next native incarnation, independent
last/ACK sequences, row/byte debt, reclaim cursors/due state and retained birth
owner links; separate `mp_queue_command_receipts` and `mp_next_command_receipts`;
typed retire-media children; current physical root retired/exited generation
and retire request ID. Keep those actual owners. Birth 750/761 uses the retained
birth owner; the new queue/next root's subsequent sequence begins at 1.

**Queue birth result cohort:** `mpBeginQueue` can create up to 16 ordered
snapshots. `mp_queue_births.result_snapshot_id` is only one scalar. The returned
`mpQueueBirthEffect.snapshots` requires an ordered typed birth child with
`birth_id`, `ordinal` (0..15), `snapshot_id`, `source_revision`, `count`, and an
exact cohort count in the birth header. No selector JSON/result graph is needed.
The native snapshot already retains kind/selector/label separately.

**Queue effect result mappings and missing scalars:** the provisional
`queue_v1_command_results` SQL is a typed queue-root result projection to map
into `mp_queue_command_receipts`, not a request for a second receipt lifetime.
Map `queue_id` to `owner_id`, `result_entry_id` to
`result_current_entry_id`, `result_position` to `result_current_position`,
`result_cursor` to the saved queue work cursor (`result_after_chunk` currently),
and the existing revision/custody/lap/session/changed columns directly. Its
`result_count_value` means the domain step's count (staged keys, save accepted
entries, or removed cleanup rows), **not** F's receipt cohort `result_count`.
F lacks that scalar and the saved `result_done` and cleanup `result_reason`
(segments/spans/next snapshot ID). Add typed equivalent projections for these
three meanings. The queue-root token is always empty and needs no new column.
The new completion policy component also writes `automatic_advances`; add
`result_automatic_advances INTEGER >=0` to F's original queue-header result,
which currently omits that returned post-play counter. Parent's existing full
queue-header/segment projection must also be saved in
the same command; this scalar effect codec is not the complete public DTO. Extend the final
composite result digest over the entire adopted header and child cohorts; the
component commitment alone is not the full public receipt proof.

**Prepared-next immutable fresh-path witnesses:** the provisional SQL selects
these nine columns from `playback_v1_prepared`, absent from F's current native
column additions:

* `mp_family_id TEXT`: original family, copied lifetime fact, no security FK.
  It may instead be read by joining the existing retained next birth owner.
* `mp_current_entry_id TEXT`: captured predecessor entry; the existing
  `entry_id` is the successor and must not be reused for this meaning.
* `mp_custody_generation INTEGER >=0`: captured queue custody. It can map to
  retained next birth's existing `result_custody_generation` if that owner is
  guaranteed retained for the entire preparation/command lifetime.
* `mp_next_lap INTEGER >=0`: selected successor lap. Existing `next_position`
  does not encode repeat/shuffle wrap.
* `mp_media_generation INTEGER >0`: exact prepared physical generation;
  logical `session_generation` is a different generation.
* `mp_source_revision TEXT`: captured inventory object's revision; do not
  replace it with today's object revision.
* `mp_predecessor_media_id TEXT` and `mp_predecessor_media_generation INTEGER >0`:
  exact predecessor physical occurrence; these differ from the successor media
  and the predecessor logical generation. Both are checked at commit.
* `mp_prepared_revision INTEGER >0`: captured asset prepared-source revision;
  distinct from inventory revision and media generation.

The returned original `mpNextBirthEffect.prepared` also includes existing
queue/device/logical session/state/generation, next entry/item/position, media,
expiry and created time. `mp_next_births` lacks the prepared media generation
and selected item/position/lap/source/prepared-revision part of this projection.
Save those original typed values in its result (or an immutable typed birth
child). A mutable current preparation/session lookup is not birth replay.

**Next command result mappings:** provisional
`playback_v1_next_command_results` maps into F's
`mp_next_command_receipts`, not a second lifetime. Queue/current-position/
entry/session/token/cancel-reason map directly. Its codec additionally saves
the original `result_shuffle_lap` and `result_changed`, absent in F's next
receipt projection. Next cursor/count are zero constants; done is a terminal
command outcome constant, so these need no additional next columns. Parent
supplies the full original session/presentation result from its core family.

**Stop child/exit adapter:** provisional tables
`queue_v1_stop_obligations` and `playback_v1_next_stop_obligations` represent the
same retained exact-stop need as F's typed retire-media children. Do not add a
generic stop owner/receipt. Queue calls record queue + logical session ID and
generation + physical media ID and generation + reason + requested time;
next calls record token + physical media ID/generation + reason + requested
time. Parent must attach its actual immutable `request_id` and keep its physical
pending debt independent of receipt ACK. The draft can cancel at most one
outstanding preparation; with a current session retirement, F's two retire
children suffice. More than one outstanding preparation is refused and can be
canceled one exact next root at a time.

`mp.next.exit`/`mp.queue.stop_exit` currently name a proposed
`playback_physical_exits(session_id,generation,supervisor_incarnation,exited_ms)`
point proof. F instead provides the actual physical owner columns
`mp_retired_generation`, `mp_exited_generation`, `mp_retire_request_id`.
Replace the provisional lookup with parent's exact physical-owner/exit receipt
adapter and original request ID. No extra physical exit table or fabricated
supervisor proof is required merely to preserve this provisional spelling.
Do not mutate saved original receipt children to manufacture a replay result.

**Reading and save:** 770/771 require no new personal DDL or F receipt owner.
UO's same-turn root read supplies actual profile key, authority/account/profile,
membership/profile lifetimes, current personal/reading revisions and history/
activity generations/pause policy. MP returns the exact selected publication,
location or clear/reread action, expected revisions, proposed next reading
revision and observation time. UO decides/applies actual history and shared
revision effects, and saves its actual result under its existing owner. Queue
save uses existing `queue_v1_saves` (no actor JSON), with selected-order digest,
cursor and actual accepted count; UO installs playlist values in the same turn.

## Remaining outer composition and publication work

1. Parent maps the explicitly provisional native result/stop/witness SQL above
   onto its final F packet, embeds adopted DDL through its normal process, and
   owns `commands_mp.go`. No hidden schema fallback is present in these files.
2. Parent adds immutable public constructors/canonical complete request
   digests, retained queue/next birth lookup and the one existing W dispatcher:
   current disclosure first, canonical receipt classification, typed original
   decode on replay, fresh source/CAS only for fresh commands, result + header
   + root counters/debt + required events, physical terminal, then final
   current disclosure/lifetime. Unknown COMMIT stays resolve-required. ACK and
   receipt reclaim remain W's existing protocol.
3. Parent supplies its core full current item/library/restriction/access-limit
   authority and UO post-play/autoplay/attention policy for each advance
   candidate, (`mpPrepareAdvancePostPlay` derives episode/listening from native item facts
   and enforces/increments MP's automatic edge counter), then the actual session
   installation/private render adoption,
   quality/track inheritance, stop request and pin/grant custody. Use the
   prepare/options/accept/finalize interfaces; no queue position effect alone
   completes a session transition. Device/profile controls preserve ordinary
   same-profile control at custody 0 and exact device control after narrowing.
   Group/cast/hosted require parent's explicit core authority adapter.
4. Parent coordinates exact predecessor queue replacement/retention at birth
   and bounded retirement continuations; this draft does not issue a full
   per-device/profile queue sweep. It installs one staged queue without
   silently inventing a current-queue pointer or a giant atomic cascade.
5. Actual source acquisition, archive/PDF/EPUB inspection and byte grants stay
   outside W; parent's checked adapter must supply the captured publication and
   actual source lifetime. Native source comparison and reading revision CAS
   are implemented here. UO consumes reading/save effects in the same turn.
6. HN captures native definitions and parent sizes W/GV work forecasts before
   registering execution. Inert definitions presently compose from
   `mpQueueDefinitions`, `mpQueueCleanupDefinitions`, `mpQueueSaveDefinitions`,
   `mpQueueResultDefinitions`, `mpNextDefinitions`, `mpNextResultDefinitions`,
   and `mpReadingDefinitions`. No plan golden or timing receipt is invented.

## Tests and runner handoff

Tiny local Go 1.26.10 / GOMAXPROCS=2 / `-p 1` checks cover codec malformed
input, immutable range edits, public shuffle vector/inverse, moved occurrence
identity, tombstones across a new lap, structural caps/mutants, owner/custody,
original-result commitments and separate actual root purposes, bounded skip
selection, native binding vectors, shared finish inventory, source-format
reading validation and VALUES-ONLY boundaries. These are millisecond pure
tests, not native acceptance or performance evidence.

Prepared **unrun** native runner tests: `TestMPNativeCanonicalSQLPrepare`,
`TestMPNativeQueueImmutableStageCASAndCleanup`, and
`TestMPNativeQueueOwnerRevisionCustodyCAS`. They use existing canonical fresh
fixture SQL; they do not alter DDL, fake an MP purpose, register a borrowed
command or claim an Applied result. Parent must run them and add integrated
matching-purpose F/receipt/ACK/crash/revoke tests after composition.

After parent cherry-picks, reviews and publishes its own source ref:

```sh
gh workflow run lane-focused.yml --repo PorticoMediaServer/portico --ref main \
  -f source_ref="$(git rev-parse HEAD)" \
  -f module=server -f runner=ubuntu-24.04 \
  -f packages='./internal/store/writer' -f run='^TestMP' -f shards=auto
```

The workflow uses the module-pinned Go 1.26.10. This command is a functional
runner target, not 1M qualification. Qualification still needs reviewed merged
code and QS/QL's canonical 1M seed: queue stage/seal/edit/settings/retire/save,
advance/next prepare/commit/cancel/physical exit and reading source/CAS,
including max structural metadata, writer fault rollback, revoke, source
replacement, replay after drift and withheld physical exit. No live provider
or product runtime was exercised; no push, PR or other-lane message was sent.
