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

## Exact F mapping checkpoint

Continued from implementation commit `3305eca239dfff3a47a383a4324253b9031e621a`.
Mapping pin: parent `changes/F-media/MP-001.sql`, SHA-256
`6b3b42086a2fe8c6bf3f9e2585ed32f6cce0969b212e64f6d575aa0ba22f027d`.
Only this helper's existing files changed. F, descriptors, canonical DDL and
security/personal owners remain parent-owned.

* Queue scalar insert/load now uses **`mp_queue_command_receipts.owner_id`**.
  It writes and decodes operation ID, complete request digest, every required
  F scalar header field, `result_step_count`, `result_done`, `result_reason`,
  `result_automatic_advances`, all cursor/status/nullable duration values and
  the original change bit. Receipt cohort `result_count` remains separate
  from the domain step count. The full original header is mandatory input;
  the codec rejects a missing header rather than looking up today's DTO.
* Next scalar insert/load now uses **`mp_next_command_receipts.owner_id`**.
  The parent supplies the original session/presentation projection (state,
  session generation/revision, media ID/generation, expiry, URL and original
  physical-pending bit). Lap/change fields use F's actual names. Missing core
  projection is rejected. Cursor/step count remain zero and done is terminal
  for these next commands; nonrepresentable values are rejected.
* `mpQueueSavedCommitment` / `mpNextSavedCommitment` cover the actual owner,
  identity, request digest, kind/version/time, every saved scalar and bounded
  original child cohorts. Queue segment and queue/next retire-media children
  map to F's exact typed relations. Count, ordinal, boolean, digest or missing
  child disagreement resolves as incomplete proof. Exact request-ID bytes
  participate in the digest. The small `mpQueueEffectCommitment` is only an
  effect test utility and is **not** the F receipt digest.
* The complete decoder reads only the original receipt and its typed children.
  Queue/session/source drift or later exit cannot change the saved result.
  Parent still performs current disclosure and W classification/floor checks
  before decoding, then physical terminal/final lifetime checks before output.
  No function reports Applied, commits, ACKs or opens a writer.
* Mutable stop recording uses **`mp_queue_stop_obligations`** and
  **`mp_next_stop_obligations`** (`next_id` is the actual token root). These
  are separate from immutable receipt-retire children. Insert-select copies
  the physical owner's **exact `mp_retire_request_id`**; it does not derive,
  trim, hash or substitute a logical/media/start request ID. Parent must have
  established that trusted bridge for the exact generation before the domain
  step records a pending obligation. A native already-exited root creates no
  fresh pending obligation or fictitious later request timestamp.
* Exit matching uses physical `mp_retired_generation`, `mp_exited_generation`,
  `mp_retire_request_id`, `mp_exit_request_id`,
  `mp_exit_supervisor_incarnation` and `mp_exited_ms`. Both exit functions now
  require the original request ID alongside generation/time/supervisor, and
  compare it with the child and both physical-root request fields. Only the
  mutable child's `exited_ms` changes. Matching repeated exit observation is a
  no-op; changed request, generation, supervisor or time conflicts. ACK and
  receipt child reclamation do not change these obligations or physical pins.
  Parent must retain the exact physical bridge until its outstanding children
  are reconciled; this code will not borrow a newer generation's proof.

The parent's consolidated additions also cover the nine native next witnesses,
full next birth projection, and typed `mp_queue_birth_snapshots` cohort. The
existing begin/prepare effects return those original values; retained birth
insert/decode and birth/root sequence installation remain the parent's outer
composition, not a second helper-owned lifetime. The new root starts its own
sequence at 1 after the retained 750/761 birth proof.

**Remaining mapping gaps:** none in the implemented queue/next scalar or
mutable stop/exit definitions against the pin above. Birth codec, full public
DTO/session construction, trusted request-ID/supervisor bridge issuance,
owner counters/debt, ACK/reclaim, events and executable Capture registration
remain outer composition. Supplying incomplete public header/session values
is an error, not permission to reconstruct them during replay. The codec
accepts F's current maximum of 40 original segment children and 2 retire-media
children, and refuses a wider cohort without truncating it. Reading 770/771
continues to use UO's existing owner and values boundary; no personal DDL.

## Remaining outer composition and publication work

1. Parent embeds the adopted F packet through its normal process and
   owns `commands_mp.go`. No hidden schema fallback or alternate receipt table
   is present in these files.
2. Parent adds immutable public constructors/canonical complete request
   digests, retained queue/next birth lookup and the one existing W dispatcher:
   current disclosure first, canonical receipt classification, typed original
   decode on replay, fresh source/CAS only for fresh commands, result + header
   + root counters/debt + required events, physical terminal, then final
   current disclosure/lifetime. Unknown COMMIT stays resolve-required. ACK and
   receipt reclaim remain W's existing protocol.
3. Parent supplies its core full current item/library/restriction/access-limit
   authority and UO post-play/autoplay/attention policy for each advance
   candidate. `mpPrepareAdvancePostPlay` derives episode/listening from native
   item facts and enforces/increments MP's automatic edge counter. Parent
   installs the actual session/private render adoption,
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
   and `mpReadingDefinitions`. Next stop recording also needs the physical-root
   definitions in `mpQueueCleanupDefinitions`. No plan golden or timing receipt
   is invented.

## Tests and runner handoff

Tiny local Go 1.26.10 / GOMAXPROCS=2 / `-p 1` checks cover complete F scalar
argument layouts, full original header/session/nullable/child/request-ID digest
sensitivity, no DTO fallback, exact native physical-exit matching and repeated
exit semantics, plus malformed codec input, immutable range edits, public shuffle vector/inverse, moved occurrence
identity, tombstones across a new lap, structural caps/mutants, owner/custody,
original-result commitments and separate actual root purposes, bounded skip
selection, native binding vectors, shared finish inventory, source-format
reading validation and VALUES-ONLY boundaries. These are millisecond pure
tests, not native acceptance or performance evidence.

Prepared **unrun** native runner tests: `TestMPNativeCanonicalSQLPrepare`,
`TestMPNativeQueueImmutableStageCASAndCleanup`,
`TestMPNativeQueueOwnerRevisionCustodyCAS`,
`TestMPNativeFReceiptSQLAndScalarRoundTrip`, and
`TestMPNativeFStopDebtSurvivesReceiptReclaim`. The F fixtures prepare the exact F
receipt/witness/stop SQL and round-trips every original scalar through native
SQL. The stop fixture ACKs/reclaims immutable receipt cohorts while preserving
mutable stop rows and their NO ACTION owner FKs, rejects a changed request ID,
then matches the trusted physical-root exit without rewriting original IDs.
These run on embedded F, or accept an exact parent packet supplied through
`PORTICO_MP_F_PACKET` on the runner and applies it only to the disposable clone;
without either it explicitly skips, which is not F acceptance evidence.
The canonical tests use existing canonical fresh fixture SQL; they do not alter DDL, fake an MP purpose, register a borrowed
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
