# MP start preparation and prepared-media component draft

Base: exact parent `1fd526adb`, canonical010 `da7841e26dd347f65554466139dd8eae0f181c9250715dc26d462e0e33b3f1c4` (parent's internal planning base `6b6d2113`). All implementation is in new owned `writer/mp_start_prep*` and `writer/mp_transcode*` files. No legacy, schema, registry, octet registry, prepared read, session, HLS or source-pin file changes. No product wiring. This is code and unqualified effect-fixture coverage, not published accepting commands or performance qualification.

## Implemented families

| ID | Concrete effect |
|---|---|
| 700 | Create original preparation plus actor, source, queue, stable-result and pending lifecycle rows; device/start-key and body digest; original horizon/deadline/replacement witness; new root incarnation, independent of retained birth identity |
| 701 | Exact pending revision -> one immutable execution, input sealed before helper work, separate physical custody row; no lease-expiry takeover |
| 702 | Preparing -> prepared under exact execution/revision, current actor/source/item/part/queue witnesses and deadline; bounded result commitment, <=256 staged records / <=1 MiB |
| 703 | Prepared -> accepted with fresh original replacement session/generation and source/queue checks; returns original session/media/snapshot IDs for parent's same-turn session composition |
| 704 | Pending/preparing/prepared/failed -> retired; retain original identity/results and exact execution stop obligation; separate bounded physical-exit/cleanup slices under this same allocated purpose |
| 740 | Immutable job recipe/selected source + normalized historical actor/input row; independent new job root incarnation; initial settled custody and due time |
| 741 | Due queued + fully exited/cleaned old generation -> new owner/execution/attempt/lease generation, bounded retry checks; no reclaim from expired running custody |
| 742 | Extend only the exact live owner/generation/execution, without changing its identity |
| 743 | Queued -> cancelled; running -> cancelling retaining lease/execution/custody until physical exit |
| 744 | Publish one immutable artifact version from already completed external probe/encode/validation/checkpoint, exact source/recipe/execution/lease and digest/size; job succeeds, physical custody remains |
| 745 | Published -> deleting with exact version/revision/digest/size; indexed `prepared_media_session_pins` prevents retirement; reset one-version cleanup custody |
| 746 | Exact supervisor exit plus external cleanup cursor slice (<=64 steps); partial cleanup retains custody, final cleanup cancels/fails/requeues/succeeds and releases lease; explicit retry-at; one deleting version -> deleted with another pin fence |

Common SQL checks actual token/family/server/account/profile/device/epoch/current generation, inventory object/source/root/incarnation/revision/size/mtime, source readiness, catalogue item/library/asset/part linkage and source/queue CAS. It does not replace W's current opaque authorization. Effective configuration/read-policy/read-authority/profile witnesses must be freshly derived by the parent inside the same admitted turn. Cleanup keeps original actor as historical data and can finish after horizon/revocation/source drift under the parent's **current cleanup-driver authority**; it cannot resurrect media or personal state.

## Concrete result projections required by F

These are the actual component fields, not an owner/index audit. Parent owns final normalized header/result names and mapping.

* Start result: `preparation_id`, **new root `command_incarnation` (16-byte value)**, lifecycle state/revision, original execution ID, original deadline, original result session ID, legacy media session ID/generation, snapshot ID, exact stop execution ID, cleanup-pending flag and cleanup cursor. Birth700 returns pending revision1 and the root incarnation, while its retained birth owner's incarnation/sequence remain separate. Claim/seal/accept/retire/cleanup preserve the original stable IDs.
* Job result: `job_id`, **root `command_incarnation`**, job state/revision, execution ID, generation, cleaned generation, lease owner/generation/expiry, attempts, nullable next-attempt-ms, bytes/size, original version ID/digest/state/revision, exact stop execution ID, cleanup-pending flag/cursor. Birth740 returns queued revision1/generation0 and its new root incarnation; subsequent root command sequences begin at1.
* Both result families bind result version1, command ID, original owner incarnation/sequence/operation ID, complete request digest and original applied-ms into an explicit bounded result codec. A replay reads only immutable scalar result fields and verifies identity/request/kind/version/result digest. It never reconstructs a result from current source, lease, job or queue. Parent still runs W's current disclosure and canonical receipt classification **before** calling the load component.
* These effects have no `Applied`/ACK/commit result. W must save effects, exact result, actual debt, required bounded events and session/queue consequences together and use its sole terminal/finish path. Errors release zero typed output.

## Exact owner/storage requirements for parent mapping

No security/WC/personal owner DDL is proposed or changed here. No generic polymorphic receipt owner exists.

1. Native root projections used by SQL: `playback_start_preparations` and `prepared_media_jobs` need `command_incarnation BLOB(16)`, `command_last_sequence`, `command_acknowledged_sequence`, `command_receipt_rows`, `command_receipt_variable_bytes`, plus the parent's selected due/floor/history fields. Birth inserts supply the new incarnation; remaining counters default0. The parent owns retained history/floors and their lifetime even if domain rows retire. Counter advance checks exact old incarnation/last/ACK/row/variable-byte values. Actual variable bytes come from `INSERT RETURNING octet_length(...)` over **every persisted TEXT/BLOB**; fixed integer bytes are not claimed as variable debt.
2. Separate retained public-birth owners for start preparation and prepared job need their **own** ID/incarnation/last/ACK/debt, existing current root/install/member/profile/family lifetime copies and original public idempotency lookup. Parent creates/validates those rows under its approved lifetime mapping. The component takes two distinct types (`mpStartPrepBirthOwner`, `mpTranscodeBirthOwner`) and never touches security/WC/personal roots or creates a generic owner. Birth results also retain the new actual root incarnation so replay never allocates another one.
3. Draft receipt SQL deliberately names concrete projections `playback_start_preparation_command_history`, `prepared_media_job_command_history`, `playback_start_preparation_birth_results`, `prepared_media_job_birth_results`, and the corresponding two birth-owner relations. Each scalar fixture row currently contains header+typed result. **Map these statements to the parent's separate per-root receipt headers and typed result relations before publication**, preserving the codec fields above. Debit the actual combined retained rows/bytes across that split (the current one-row projection increments rows by1); use parent's due updates, historical floors, ACK and bounded reclaim. This file neither adopts DDL nor duplicates that audit.
4. Start physical custody projection `playback_start_preparation_execution`: operation PK/FK; unique nonempty execution ID; physical-exited, cleaned, exit digest, bounded result digest/staged-row/staged-byte counts, cleanup cursor. Retirement keeps this row until exact supervisor exit and actual external cleanup. Each cursor update CASes execution+old cursor; cleaned implies exited. The completion commitment is inert external evidence and needs the parent's verified preparer adapter before execution.
5. Job input projection `prepared_media_job_inputs`: job PK/FK; original actor's server/authority/account/**actor profile**/device/family/grant/session hash/epoch/token generation/original horizon (recipe profile is distinct); complete start-source projection (library/source/asset/object/inventory revision/root/incarnation/remote/configuration/policy/authority/library generation/configuration generation/profile revision/size/mtime); immutable transformation digest/part index. Existing canonical job columns retain immutable recipe profile/target/selection/source revision. `principal_json` is an empty compatibility column, never decoded or used as authority by this component.
6. Job physical custody projection `prepared_media_job_custody`: job PK/FK, exact generation/execution ID, physical-exited, cleaned, exit digest and cleanup cursor. Allow multiple empty initial execution IDs; if execution uniqueness is enforced use a **nonempty partial unique** key. A new execution can replace only the exited+cleaned prior generation. Publication never marks it exited/cleaned. Version deletion does not release the published object while any session pin remains.

External observations are private typed **data**, not forged proof capabilities: the parent must validate real supervisor exit, completed external work and immutable artifact verification through its closed adapter; a digest/string/boolean/expired lease alone proves none of those. Object deletion must additionally retain the external artifact gate and global shared-object references (other versions/downloads), not infer byte release from a retired version row. No IO/helper/network call occurs in these writer components.

## Validation and remaining integration

Implemented tests cover bounds and complete request/result commitments, wrong-purpose/error/cancel zero-output finish, original stable IDs, stale execution, queue drift, cancellation after revocation/expiry, exact lease owner/generation, no expiry takeover, partial cleanup before retry, source mismatch on publication, pin-fenced retirement, immutable original replay after head drift, digest tampering and independent birth/root sequences/debt. Native tests use a small explicitly unqualified synthetic frame and exact MP descriptors; they never borrow a shared existing command ID. They use canonical domain table fragments and projected missing fields/children; minimal scope tables are fixtures, not an authorization or FK oracle.

Locally verified: Go1.26.10 compilation of the writer test package and tiny `^TestMPPrepDraft` preparation-only tests and the existing shared finish AST guard, `GOMAXPROCS=2`, `-p 1`; no native fixture, heavy suite, live provider or capture run locally. Native fixtures, race, crash/uncertain-COMMIT/ACK/reclaim integration and HN actual-byte/plan Capture remain **unexecuted runner/integration targets**. Parent owns final `commands_mp.go` descriptors (RD2 draft `25e238949de08732c7d537c4b76629863d0de673`), closed result-codec adoption, immutable constructor/buffer admission, fresh opaque disclosure + effective-source-policy checks, birth/root receipt mapping/floors/debt/due, bounded events, session/source-pin composition and external-supervisor bridges. No passing native or qualification claim is made.

After parent review/cherry-pick and authorized publication, dispatch the exact committed source on standard runners:

```sh
gh workflow run lane-focused.yml --repo PorticoMediaServer/portico --ref main \
  -f source_ref="$(git rev-parse HEAD)" -f module=server -f runner=ubuntu-24.04 \
  -f packages='./internal/store/writer' \
  -f run='^TestMP(PrepDraft|StartPrepNative|TranscodeNative)' \
  -f race=true -f shards=auto
```

Toolchain is the workflow's pinned Go1.26.10. This helper has no push/PR authorization and does not dispatch an unpublished source SHA.
