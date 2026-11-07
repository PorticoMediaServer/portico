package writer

// Person slice handoff (unadopted D overlay; no accepting product wiring).
//
// Implemented private bodies/components:
// 203: complete typed owner birth + historical result resolution; expired exact
//      owner claim checks retry due and increments ONLY lease generation.
// 204: exact active lease renewal; 205: retry/finish with original saved result.
// 210/211/212: ONE build ever per operation/incarnation, frozen generation,
//      exact unique-owner-key recovery after BEGIN ACK/reclamation; original
//      BEGIN stays Retired. Generic immutable person header, aliases/external IDs,
//      independent native source verification/no-next, exact person seal FK.
// 216/217: sealed source accept/delta (217 receipt CHECK remains a D gate), exact person token/revision, provider namespace,
//      originating nonretired library locale+metadata revision, policy/consent,
//      explicit primary source role/incarnation/revision/head, typed field locks,
//      current source CAS and person field origins. Result revision means actual
//      accepted PERSON revision for216/217, operation revision for other variants.
//      A composed search intent MUST enter reviewed W/PB durability before commit.
// 218: typed obsolete-publication/build retirement and bounded child removal;
//      These are components, not accepting receipts or custody refunds.
//      Root owns fixed-slot251; this helper supplies NO251 implementation.
//
// Mandatory parent hunks (never edit shared files from this helper):
// 1. result_digest.go, preflightCanonicalResult:
//      case ipPersonSavedResult: return preflightIPPersonResult(v)
//    result_digest.go, writeCanonicalResult:
//      case ipPersonSavedResult: return writeIPPersonResult(e,identity,request,v)
//    There is no fallback hash. Before these cases, tests verify that a missing
//    codec refuses after mutation and W rollback restores the native database.
// 2. ip_receipt_ack.go: admit IPProviderClaim in the ipReceiptPerson sourceKind
//    condition. In its person command-kind CASE use:
//      WHEN 'begin' THEN CASE WHEN evidence_id IS NULL THEN 203 ELSE 210 END
//    Renew/finish/evidence/accept retain their existing mappings. This slice
//    intentionally uses the D-admitted begin shape for birth/claim (no evidence).
//
// Exact D request hunk, not applied to the draft or canonical schema:
// - metadata_person_receipts command_kind CHECK: add 'delta' for distinct217.
// - Add typed reclaim-only result columns to that CREATE definition:
//      reclaimed_rows INTEGER CHECK(reclaimed_rows IS NULL OR
//        (typeof(reclaimed_rows)='integer' AND reclaimed_rows>=0)),
//      reclaimed_bytes INTEGER CHECK(reclaimed_bytes IS NULL OR
//        (typeof(reclaimed_bytes)='integer' AND reclaimed_bytes>=0)),
//      after_family INTEGER CHECK(after_family IS NULL OR
//        (typeof(after_family)='integer' AND after_family BETWEEN 0 AND 2)),
//      after_alias_ordinal INTEGER CHECK(after_alias_ordinal IS NULL OR
//        (typeof(after_alias_ordinal)='integer' AND after_alias_ordinal>=-1)),
//      after_external_provider TEXT,
//      after_external_kind TEXT CHECK(after_external_kind IS NULL OR
//        after_external_kind='person'),
//      after_external_id TEXT,
//      terminal INTEGER CHECK(terminal IS NULL OR
//        (typeof(terminal)='integer' AND terminal IN(0,1))),
//    Include typeof(text) guards and all-or-none external cursor CHECKs; require
//    reclaimed_rows/reclaimed_bytes/after_family/terminal for kind='reclaim',
//    forbid them outside reclaim, and use existing accepted_revision for the
//    exact resulting build revision. Ordinal is -1 before any raw alias. External
//    cursor is the actual last deleted tuple(provider,kind,id), not row count.
//    No FK to a transient build/publication is added to an immutable result.
//
// Remaining typed bodies/checklist:
// - 217 is drafted through the full typed body with nonempty exact captured
//   accepted-head fences and a distinct saved result kind. Its last INSERT is
//   refused by the current D CHECK until INT adds delta; W must roll back.
// - 213–215 are genuinely complete publication sets, not aliases for this scalar
//   person source. Credits/other families need distinct bounded native manifests.
// - Final header/build retirement requires all generic child no-next,
//   current/historical person and generic evidence references, EN build/proof,
//   provider-subject accepted-evidence, accounting and downstream source custody.
//   A failed FK is not an indexed bounded retention proof. Do not erase a body
//   under a receipt or return terminal while charged evidence remains. The
//   operation owner remains retained/terminal after build deletion; never re-birth
//   a second build on its ID/incarnation. New observation uses a NEW owner.
// - W composes current disclosure BEFORE any historical result/retired return,
//   mutation permission, immutable input capture, owner birth admission, trusted
//   clock, real governor + physical memory, finite statement Work/Capture, PB
//   intent, ACK, maintenance results, custody reconciliation and safe COMMIT.
//   Source manifests and local copy ledgers provide NONE of those authorities.
//
// Qualification targets (NORMAL): ip_person_birth/claim/renew/retry/finish,
// ip_person_evidence_begin/append/seal, ip_person_accept/delta, old publication /
// abandoned body / receipt retirement. Cohorts: fresh/replay/retired/conflict,
// zero/max bounded children, actual-byte failure, stale person/locale/policy/
// consent/lease/source role/head, locked-empty, rollback, interrupted retention.
// Tiny native fixtures are functional evidence only. Full/race/Capture/1M,
// W/GV physical memory, current permission, PB durability and COMMIT uncertainty
// controls are UNRUN here, publication-only gates owned by parent/integration.

type ipPersonDeltaDraft struct {
	original                       ipPersonRequest
	expectedAcceptedPersonRevision int64
}
type ipPersonPublicationPageDraft struct {
	page, rows, bytes int64
	commitment        [32]byte
}
type ipPersonPublicationSetDraft struct {
	identity   CommandIdentity
	owner      ipPersonOwner
	setID      string
	pages      []ipPersonPublicationPageDraft
	commitment [32]byte
}
type ipPersonFinalRetireDraft struct {
	original                                      ipPersonRetireRequest
	expectedRemainingRows, expectedRemainingBytes int64
}
