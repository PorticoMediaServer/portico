# MP queryable session, physical custody and canonical HLS draft

Current clarified ruling: **UO-001 owns user-initiated birth/lifetime/public idempotency. Seven actual media worker streams keep their independent MP native incarnation/last/ACK/header/result quota, including cleanup after issuer revocation.** W owns the sole adopting codec with payload <=65,536 bytes per closed purpose. No five retained MP public-birth tables or borrowed UO sequence. Parent owns exact F and UO integration. UO draft SHA256 `a049c20e0c1763a3508d384688953a64e26197f7e3d21f143cddc30d21e4fa41` was read and verified. Canonical HLS mapping was committed first at `73f7585066b0a145a6b966d6b3a2af0c238fe1ca`. The subsequent adapter maps the parent's now-concrete session/physical worker receipt tables. No second codec is adopted.

Source base: local `1be57f969abd46535ba6679832676670aea5f6f7`; parent integrated that draft at `cdb385657a01d0b3415fffec81b379b020dfab11`. Changes stay in owned `mp_session_domain*`, `mp_source_pins*`, `mp_hls_domain*`. No canonical DDL, registry, legacy, product wiring, live provider, push, PR or subagent work. No family/native acceptance claim.

## Completed canonical HLS mapping

Every executable reference to the former separate window custody table is removed. The **only mutable window row is `playback_hls_windows`**. Its first INSERT writes the existing native status/highest/start time and all twelve `mp_` facts. A same-key restart uses one fenced UPDATE; cancel sets failed status and retires writes in the same UPDATE. Window capture does not join a second window row.

| Draft semantic fact | Canonical native field |
|---|---|
| source occurrence generation | `mp_physical_generation` |
| exact producer execution | `mp_execution_id` |
| original physical source request | `mp_source_request_id` |
| writes retired | `mp_writes_retired` |
| actual accepted producer exit | `mp_exit_confirmed` |
| monotonic same-key execution generation | `mp_execution_generation` |
| window CAS revision | `mp_revision` |
| selected end index / immutable plan | `mp_end_index` / `mp_plan_digest` |
| completed physical cleanup | `mp_cleaned` |
| original exit commitment / cleanup cursor | `mp_exit_digest` / `mp_cleanup_cursor` |

Reclaim captures **expected old execution ID**, revision and execution generation in its immutable input. It additionally repeats the actual physical generation and source request, requires actual exit+cleanup and complete/failed status, and refuses while old progress/artifact children remain. It allocates a different execution and increments the generation; it never reloads a successor tuple to make an old producer's request pass. Publish, cancel, exit and control points keep the exact execution/generation/source-request fences. Publication also repeats physical generation; delayed old publication/exit cannot settle a successor. An expired lease or failed status is not physical exit.

Queryable child generation keys must reference native `(session_id,start_index,mp_execution_generation)` with its UNIQUE key and **NO ACTION** update/delete FKs. Current-source generation, source request and execution remain separate facts. Physical final cleanup and reservation release now query canonical window `mp_cleaned`; no shadow mutable custody authority survives.

## Queryable MP facts retained

These facts are read by actual admission, source selection, delivery, retirement, cleanup or worker recovery paths. They are not proposed solely to hold response payloads.

* Physical root: resource incarnation, revision, exact occurrence execution/source request, generation, native retired/exited generation floors, retirement request, exit supervisor incarnation/time/request, cleanup cursor/completion/commitment, grant-retired and delivery-ready facts.719 repeats the original retirement request, supervisor incarnation and exit time; later cleanup must match the accepted tuple. Cancellation/expiry closes grants but never releases pins/reservations/reader custody. Root facts, not ACKed receipt children, retain live stop debt.
* Logical root: resource incarnation; logical/presentation generation, desired/current transport state, sequence, position, lease/end facts, presentation engine and original scoped URL. Ready consumes native verified-delivery readiness; pause/resume/seek use exact CAS and sequence. Lease renewal respects grant and actor horizons. Admin/account operators remain real separately authorized actors, not fabricated target principals.
* `mp_media_inputs`: physical-root point/FK; original actor/family/device binding; complete immutable selected source and original queue witness; part and original public item ID. Native public ID/item/asset/library/part association is checked. Queue CAS gates installation/reselection; a later queue edit does not invalidate every owned byte read.
* `mp_session_replacements`: queryable current requested/installed transition, exact old media/logical generations/execution, proposed new media/execution/incarnation and complete immutable target-input commitment.714 creates no new physical custody until old native exit and cleanup. The target-input commitment is a queryable media-intent fact, not a receipt/payload codec.
* `mp_start_execution.custody_media_session_id`: one-time exact prepared execution transfer during703. Parent transfers the actual handles/permits; it does not allocate overlapping preparation/media custody.
* `mp_remote_delivery_inputs`, canonical subtitle remote evidence, `mp_direct_delivery`: actual version/descriptor/evidence/endpoint policy and size bindings used by relay/native/prepared acquisition. Prepared direct output uses an actual immutable version pin/digest/size, not original inventory size. No raw origin URL/credential is published.
* `mp_media_read_pins`: actual reader execution, occurrence generation, source version/artifact digest and byte range. Actual reader exit releases the pin. Source/resource release requires physical exit **and no remaining native body reader pins**.
* Native source/prepared/subtitle/disc/private receiver pins: exact selected immutable facts and real ownership. Prepared pin selection also checks the job's original `mp_job_inputs` source witness. Subtitle personal ownership is derived from the real actor tuple. Receiver owner is the actual predecessor **physical** session/generation. A version/artifact with readers cannot retire.
* `mp_hls_plans` / `mp_hls_renditions`: immutable plan/source-keyframe or converted-grid commitment, source size/mtime, backend/strategy, declared count1..32, bounded typed rendition staging/contiguous ordinals and private-init allowlist. Actual timeline/probe/encode verification remains outside SQL.
* `mp_hls_window_renditions`: contiguous per-rendition progress, bounded <=32-row seed with native affected-row count; exact declared-set count/minimum forms the publication barrier. No arbitrary highest-produced jump.
* `mp_hls_artifacts`: immutable name/digest/size/timing/decode-from, exact source occurrence/window/execution/plan and publication/deletion state. Private init is a separate zero-timing/index=-1 shape. It cannot be overwritten under a stable URL, acquire readers while deleting or disappear before actual deletion/gate checks.

Parent F includes actual TEXT/BLOB bytes for every added queryable field in its generated variable-byte expressions. Neither role caps nor field maxima are substitutes for point `octet_length` or actual native debt. Child indexes/plan/FK Capture and qualification remain unexecuted integration work.

## Origin/incarnation integration with UO and native MP workers

`mp_command_incarnation` remains the independent actual media worker stream incarnation. User-origin bindings (`mp_birth_id`, `mp_start_birth_id` spellings from the prior draft) require mapping to UO's actual user lifetime/idempotency graph; they do not authorize retained MP public-birth tables. Parent owns that mapping. The follow-up adds only session/physical worker header payload INSERT/lookup and native counter/debt CAS against F; it does not change other MP families. Native worker counters, ACK/floors and quota remain separate from UO sequences and must remain usable for cleanup after issuer revocation.

703 creation consumes exact accepted preparation IDs and original media generation. Independent710 installation and closed760/762 composition use the actual parent lifetime mapping; no nested operation borrows710.760/762 can stage physical retirement while keeping the predecessor's logical state for the parent's selected-next CAS. After native exit/cleanup they end old logical authority and install the new one.762 adopts the exact already-buffered private physical root and transfers its private binding; it never clones that resource. Original resource/execution/generation/retirement/exit data remain available as bounded result data.

`composeMPSessionDelivery` runs a fixed typed recipe under exact703/710/714/760/762, with private leaf paths for at most one selected prepared/subtitle/disc/receiver/remote binding and direct or HLS-plan/reservation facts. It does not invoke standalone-purpose guards under a parent turn or mutate a descriptor. Larger rendition/window work remains separately bounded. Parent owns staged scheduling, real authority/lifetime proof, quota/resource grants, event composition, shared request/result persistence, retries and unknown-COMMIT resolution.

## Bounded result DATA contract for native MP workers and UO/W

`mp_session_domain_results.go` validates the closed typed DTOs and totals selected raw text octets plus known scalar/array input widths. It **does not encode/hash a result, return encoded byte length, reserve memory, persist anything, create an owner or advance any receipt counter**. The former three planning result codecs in these core files are removed; worker payload bytes must come from W’s sole purpose-bound codec. W must select the closed role for the actual parent purpose, preflight its real versioned encoded payload, enforce the actual cap, save the original result atomically with effects and use its sole finish/commit/ACK path. These are proposed caps, pending UO/W adoption:

| Typed role | Proposed encoded cap | Exact original data that must survive replay |
|---|---:|---|
| `mpSessionEffect` | 64 KiB | session/media IDs and resource incarnations; public/internal item, asset/part/queue/entry; logical/physical/presentation revisions and generations; state/mode/engine/original URL; grant/lease horizon, position/start/sequence; end time/reason; exact stop execution/generation, cleanup cursor/pending/changed; native physical retire/exit tuple; optional exact old-media projection including its item/asset/incarnation/execution/source request/generation and full retire/exit tuple |
| `mpSourcePinEffect` | 64 KiB | media/resource incarnation/execution/state/mode/revision/generation and actual retire/exit tuple; closed pin kind; exact resource/version/source/incarnation/revision/config generation/digest/size; issued grant hash/token/expiry; receiver parent/expiry; remote origin version; acquisition/release/pending facts |
| `mpHLSEffect` | 64 KiB | media/resource incarnation/execution/asset/mode/state/revision/generation and native retire/exit tuple; exact window execution ID/generation/revision/start/end/plan/backend/status; actual reservation; contiguous produced/served positions; artifact kind/name/rendition/digest/size/index/start/end/decode time; original reader pin/strong source version; cleanup/exit/pending/throttle/ready/release facts |
| `mpSessionDeliveryProjection` | 64 KiB | fixed optional prepared/subtitle/disc/receiver/remote role slots, delivery/reservation results, final physical revision. No generic command/response list. Parent's base installation result must use this final physical revision. |

703/760/762 nested session/delivery data belongs to the **actual shared parent operation**. It is not an independently ACKed710 command or a borrowed UO sequence. UO/W must preserve the original bounded payload after source, lease, queue and window drift. These role data need no MP receipt-only child tables; retain typed relational children only where a real media query/custody operation needs them. W owns payload version/role identity and canonical bytes/digests. Native MP worker adapters must bind the actual MP owner/incarnation/sequence/operation and retain MP replay/floor/reclaim and actual row/byte accounting; user-initiated parent operation bindings belong to UO. Current disclosure remains an outer authority obligation. Secrets stay out of generic events.

Grant rotation724 still requires the parent's same-turn logical/private URL rebind. Physical/schema support alone does not establish actual IO, rendering, upstream policy, supervisor acceptance, authoritative scopes or family completion. Current NORMAL media durability/security policy remains unchanged.

## Validation and runner target

Locally passed: Go1.26.10 compile and tiny pure native-SQL arity/catalog, complete canonical-field mapping, input/purpose, zero-output finish, native exit-data validation and actual text-octet contract checks (`GOMAXPROCS=2 -p 1`). Domain/shared finish AST guards are included. No result codec or worker-header adoption is established by those checks.

Runner-only target `TestMPHLSDomainNativeCanonicalWindowExecutionFence` is prepared but **not run locally**: canonical base window plus explicit queryable F fields, one native row, NO ACTION generation child, child-blocked reclaim, exact old execution reclaim, and delayed old producer/exit refusal. This is synthetic SQL fence evidence, not F schema/FK/plan/authority/real-exit qualification or an accepting facade. Every effect frame uses its actual732/733/735/736/739 descriptor; no descriptor is mutated or existing ID borrowed.

After parent review/integration and authorized publication:

```sh
gh workflow run lane-focused.yml --repo PorticoMediaServer/portico --ref main \
  -f source_ref="$(git rev-parse HEAD)" -f module=server -f runner=ubuntu-24.04 \
  -f packages='./internal/store/writer' \
  -f run='^TestMP(SessionDomain(Draft|Native)|HLSDomain(Draft|Native)|SourcePinsDraft)' \
  -f race=true -f shards=auto
```

Native integration, EXPLAIN/octet Capture, real IO/supervisor bridges, current UO root authority, source-policy/URL origin binding, durable recovery and workload qualification remain parent integration work. Registry names720–739 were reconciled by the parent to the earlier concrete allocation; this change does not edit that registry.

## Preserved interrupted work

The unfinished standalone session receipt graph file was stopped at the lead ruling, removed from the Go build and **not committed/adopted**. It is preserved at `/home/justin/projects/Portico/.private/mp-core-uo-fold-2026-11-07-1be57f969/mp_session_receipts.go`, SHA256 `092baa42e802fa5e300c54df9d6ba46804428ed73b7d7dc7fa704af647654d98`. Modified planning-codec snapshots and a hash manifest are preserved in that same private directory. This is retained source work, not disposable scratch; it is deliberately excluded from task cleanup. Earlier already-committed prep/transcode work is outside this helper's current file ownership; parent maps its actual native worker streams independently from UO user origins.

## Current mapping handoff

Canonical window text preflight `mp.hls.window_octets` measures actual execution/source-request/plan/exit text octets under the exact selected row fences before payload copying. The parent must admit those measured bytes; the helper does not mint memory credit. All own standalone/composed media effects remain drafted. Exact standalone worker payload INSERT/lookup/counter/debt mapping now exists in `mp_session_receipts.go` and `mp_physical_receipts.go`; W codec/authority/admission/ACK/reclaim integration remains outstanding. No native SQL test has run locally.

## Follow-up worker storage mapping

F source snapshot SHA256 `c295971123cce995fdc641d5057ff2744548279f8be4bd8b2d56fda8e5d67a03` (parent-owned file, read only). Concrete parents are `mp_session_command_receipts` (711–719; actual root `playback_v1_sessions`) and `mp_physical_command_receipts` (720–739; actual root `playback_sessions`). Both map exactly: `owner_id`, `command_incarnation`, `command_sequence`, `operation_id`, `command_kind`, `request_digest`, `result_version=1`, `result_count`, `result_digest`, `result_payload` (1..65536), `applied_ms`, generated `variable_bytes`. No new field request for this adapter.

The shared DATA envelope is `mpWMediaPayload`, never a serializer. W must supply the original closed-purpose version/count/digest/bytes after its codec preflight. Actual native variable-byte debt is owner text octets +16 incarnation +16 operation +32 request +32 digest + actual payload octets. The native generated RETURNING value must equal that total. Caller supplies captured actual worker incarnation/revision/last/ACK/rows/debt and real admitted max rows/bytes/due. CAS advances `mp_command_last_sequence`, `mp_receipt_rows`, `mp_receipt_variable_bytes`, `mp_owner_revision`, and initializes `mp_receipt_reclaim_due_ms`; it repeats all captured counters and allows live/draining cleanup. ACK, retired/exited floors and current live stop debt remain untouched. A failed CAS must roll back the INSERT through W's single transaction finish.

Point-only `mpSessionWorkerPayloadBytes`/`mpPhysicalWorkerPayloadBytes` measures native debt before admission. Lookup repeats that bounded point and requires admitted variable bytes before copying the original saved payload. It repeats actual incarnation/sequence and checks operation/request/purpose/version/count/digest shape. W must still verify the purpose-bound canonical digest and decode into the closed typed result; this adapter never reconstructs replay from current media state. Nested703/760/762 and user birth710 are explicitly rejected by standalone session worker storage and stay in the actual parent's result.

Tiny admission/catalog/arity checks passed; runner-only `TestMPSessionDomainNativeWorkerOriginalPayload` is compiled, unrun locally. It exercises both actual-root projections with original-byte replay after mutable root drift, exact native debt, unchanged ACK and refused copy without admission. Its synthetic payload/DDL does not qualify F, W codec, authority, actual memory admission, rollback/unknown COMMIT, ACK/floor/reclaim or IO. Adapter definitions still need parent registration and descriptor steps; W closed-purpose encode/decode/admission bridges and actual root initialization remain integration debt. No family completion is claimed.
