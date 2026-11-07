package writer

// LOCAL DRAFT HANDOFF (P2-IP artwork223..231, base b0e108cb4).
//
// Implemented private entry: ipArtworkMaterialApply. Job223/224 claim/renew;
// material225 prepare/installed/publish; role226/227 select/clear/unlock;
// upload228 atomic installed-material+locked-selection acceptance (offline);
// GC229/230 source and blob mark/token/finalize plus staging-only retirement
// after published files have transferred custody; source231 exact revision
// reencode with no descendant enumeration. Same choice, source reuse, identical
// reencode and saved replay produce zero source/history effects. Matched changes
// preserve the separate locked row. Changed matched selections independently
// match the complete native image job, published immutable intent and CURRENT
// ready discovery stage/candidate source, provider, language and policy fences. Zero/NULL and raw slot no-next are explicit.
// The retained owner is artwork_subject_jobs; no image/role/child owner is added.
//
// PARENT'S TWO REQUIRED result_digest.go CASES (not edited here):
// in preflightCanonicalResult's switch:
//   case ipArtworkMaterialResult:
//       return preflightIPArtworkMaterialResult(v)
// in writeCanonicalResult's switch:
//   case ipArtworkMaterialResult:
//       return writeIPArtworkMaterialResult(e, identity, request, v)
// Constructors/verification MUST continue calling canonicalResultDigest, not
// writeIPArtworkMaterialResult directly. The latter is its closed writer helper.
// All new request digests use canonicalRequestDigest(kind,1,q.Identity,q), with
// the complete original typed root including every inactive witness/identity.
// No new manifest domain, request hash or result encoder is introduced.
//
// Exact independent Python vector (struct.pack('>Q'), UTF-8 U64 length-prefix,
// SHA256) for writeIPArtworkMaterialResult: identity inc={1},seq2,op={3}; request
// {5}; result kind229,owner11,revision4,applied123,count1,action2,lease0/0;
// six texts empty/empty/empty/empty/empty/(64 'a'); selection0/storage8/history0;
// token={9}. Wire349 bytes; SHA256:
// fbf7d7465d1eb921b8fad84c47ae6ccce594066448584ec3b4ad93092629ee1c.
// W's requestEncoder supplies every production primitive and closed result
// preflight bounds all result fields before hashing. Native TEXT copies occur
// only after exact point octet_length preflight; other native values compare
// original bounded input in SQL and copy fixed scalars. InputBytes is actual
// original text length, NOT physical buffer/governor admission.
//
// DDL GAP1: EXPLICIT LOCKED EMPTY. Do not erase the lock to approximate empty.
// Exact CREATE-fold change requested (INT chooses adoption):
// artwork_selections.digest:
//   digest TEXT REFERENCES artwork_sources(digest),
//   CHECK(digest IS NOT NULL OR choice='locked'),
// artwork_command_receipts: replace its select-only nonnull CHECK with:
//   CHECK(command_kind<>'select' OR selected_digest IS NOT NULL OR choice='locked')
// Row presence+choice='locked'+digest=NULL represents locked empty; absent row
// is unlocked. No extra sentinel source/blob or per-art owner is appropriate.
// Remaining functions after adoption: allow the typed Present/empty digest
// locked value; use IS and nullable digest binds in SelectionExact/Create/Update;
// skip SourceReady/history source pin when that value is empty; adjust closed
// select result preflight. Existing native test explicitly refuses the shape
// without effects until the DDL can represent it. It does not claim acceptance.
//
// DDL GAP2 / REMAINING ABANDONED CANONICAL FILE CLEANUP:
// Prepared/installed intents may be the sole custody of canonical digest paths.
// A staging-only delete MUST NOT drop those manifests or unlink a shared file.
// This draft intentionally refuses their GC and retains all charged slots/bytes.
// Current GC intent path requires a fully published manifest, ready matching
// blobs and (when slot3 exists) an exact source original_bytes hold. It removes
// ONLY private intent/slot staging paths; canonical paths stay with their rows.
// To implement abandoned canonical unlink without a second owner, request:
// artwork_material_intents CREATE fold:
//   retirement_mask INTEGER NOT NULL DEFAULT 0
//     CHECK(typeof(retirement_mask)='integer' AND retirement_mask BETWEEN 0 AND 15),
// artwork_material_slots CREATE fold:
//   retirement_storage_revision INTEGER
//     CHECK(retirement_storage_revision IS NULL OR
//       (typeof(retirement_storage_revision)='integer' AND retirement_storage_revision>0)),
//   CHECK(slot<>3 OR retirement_storage_revision IS NULL)
// The exact existing intent token/revision fences raw-original unlink; each
// encoded slot additionally retains its marked artwork_blobs.storage_revision.
// A four-bit native mask distinguishes canonical files owned for removal from
// shared paths merely released by this intent. These are lifetime facts, not a
// generic payload. Original scalar slot metadata remains immutable.
// Remaining typed functions to build after that hunk:
//  1. ipArtAbandonedMark: independently verify all <=4 raw slots/no-next; probe
//     source/history/receipt/other-intent/backup holds; mark only unreferenced
//     canonical blobs deleting with token/exact generation, preserve shared
//     paths, persist per-slot generations and intent retirement mask together.
//  2. ipArtAbandonedFinish: verify identical intent token/revision/mask and
//     each stored blob generation after physical completion; retain/retry any
//     failed unlink; delete children/marked blobs only after exact hold recheck.
//  3. Original source publication/preparation must refuse a retiring intent's
//     slot3 source_digest in the same transaction. No digest grants authority.
// These missing functions cannot be replaced with the current staging-only GC.
// Independent implemented source/blob GC, selection, uploads, receipts and
// reencode remain valid against the existing combined unadopted D/E draft.
//
// EXPLICIT INTEGRATION GATES (no enable/adoption claim):
// - Current permission and disclosure, including ORIGINAL replay, are W-owned.
// - Actual governor/physical memory (including bounded audit JSON), physical
//   disk/file custody, current clock, total repeated recipe+terminal costs,
//   safe COMMIT/uncertain resolve, receipt retention/ACK and HN Capture remain
//   outer gates. This component opens no transaction, admits nothing and never
//   exposes public Applied. Work forecasts are bookkeeping, not measurement.
// - The physical owner must verify every exact installed file before installed
//   transition, and hold token/path/generation custody until unlink completion.
//   Cancellation retains installed/staged facts; it does not refund reservations.
// - Backup admission must refuse outstanding unlink marks BEFORE copying;
//   checking backup_blob_holds only at mark/finalize cannot stop a backup that
//   begins between mark and physical unlink. The backup entry is not owned here.
// - Parent composes finite library/domain revisions and durable event hints for
//   changed roles, and its W/PB source/search visibility helper where needed.
//   Subject artwork revision and exact retained owner source revision are
//   bumped here; no library x user/album x song/book x part fanout occurs.
// - Intent ID equals native image-job ID. One job owns one immutable material
//   generation; retries cannot replace that manifest. A replacement image job
//   needs a distinct native job ID, under the SAME retained subject-job owner.
// - Image parsing, MIME acceptance and source/blob digest production are the
//   existing external pipeline's work. No image codec is introduced here.
//
// Exact local selectors (Go1.26.10, GOMAXPROCS=2, -p1 -parallel2):
// compile: -run '^$'
// tiny draft components:
// -run '^TestIPArtworkMaterial(TinySmoke|RollbackAndFences|LockedEmptyRefusesWithoutEffects|Plans|CanonicalBinding|GCTokensHoldsAndReferences|ReencodeSourceRevision|UploadAcceptance|ProtectedRowsError|NoopSourceReuse)$'
// PORTICO_IP_DDL_DRAFT points at the original combined IP-DDL-DRAFT.sql.
// Accepting tests require the parent's two canonical result switch cases; the
// local run uses ONLY those two cases in a temporary Go -overlay. Without them,
// accepting tests explicitly skip; input/vector/plans/refusal remain runnable.
// Never mistake that overlay or the DDL environment for schema adoption.
// Tests use tiny native fixtures/virtual protocol clock, not qualification.
// Unrun: full/race/Capture/1M, external file IO/unlink and process-crash custody,
// actual admission/physical memory/uncertain COMMIT, library/event integration,
// adopted locked-empty acceptance, abandoned canonical unlink, production or
// remote/live providers. Parent owns GitHub lane-focused CI and publication.
//
// FINAL LOCAL EVIDENCE (ten selected parents PASS, nine tiny fixture clones):
// Go1.26.10; SQLite3.53.4; virtual protocol clock; canonical schema
// da7841e26dd347f65554466139dd8eae0f181c9250715dc26d462e0e33b3f1c4.
// Combined IP DDL: 8bcf4b72a62fd44313ce941999b8ea07efd9514e5d699c25c7505c97d6f081d0.
// Two-case result_digest.go overlay source:
// 65ea2039a530e94a8d03a099a6d36a18ac50f89357c4e9baa89bf1bfbe9a13df.
// Fixture-source aggregate (sorted repo paths; path+NUL+bytes+NUL each):
// 118f775a6937e562bdbf7318b31d3cabc367c5b0b9c5c201a40ec3c54bdb7737.
// Final verbose smoke stdout SHA256:
// c3b1911f8d4a00b27b23c3d4f2b24bb5ae63fd824bbc8171baf63df294a13fcf.
// Canonical fixture bootstrap DB9535488 bytes SHA256 (before IP delta):
// 763a1b7037a28176c4f01a97af940dc79ed602b88309190bf9d6378c611eb4af.
// No external files, production/remote DBs, live providers or captures used.
