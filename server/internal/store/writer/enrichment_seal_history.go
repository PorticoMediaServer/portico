package writer

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	sealHistoryOwner   stmt.ID = "enrichment.seal.history_owner"
	sealHistoryHeader  stmt.ID = "enrichment.seal.history_header"
	sealHistoryPayload stmt.ID = "enrichment.seal.history_payload"
)

// The fixed header is schema-bounded (BLOB16/32 and closed native kind). It is
// classified BEFORE any retained TEXT proof payload is read. No live entity
// join is used to reconstruct historical results or block retained ownership.
func enrichmentSealHistoryDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		{ID: sealHistoryOwner, Owner: "writer.enrichment", Kind: stmt.Read,
			SQL:    `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence FROM metadata_provider_operations WHERE id=? AND entity_id=?`,
			Params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "entity", Type: stmt.Integer}},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_1"}}},
		{ID: sealHistoryHeader, Owner: "writer.enrichment", Kind: stmt.Read,
			SQL:    `SELECT operation_id,request_digest,command_kind,result_count,result_digest,entity_id FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}},
		{ID: sealHistoryPayload, Owner: "writer.enrichment", Kind: stmt.Read,
			SQL:    `SELECT provider_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,entity_id,after_item,after_ordinal,publication_id,effective_image_id,effective_image_revision,result_count,result_digest,seal_version,seal_evidence_id,seal_subject_id,seal_parser_version,seal_provider,seal_subject_kind,seal_provider_id,seal_language,seal_region,seal_request_shape,seal_observation_digest,seal_owner_generation,seal_build_id,seal_build_generation,seal_content_revision,seal_algorithm,seal_commitment,seal_verified_rows,seal_canonical_bytes,seal_retained_bytes,upstream_evidence_owner,upstream_evidence_incarnation,upstream_evidence_sequence,source_dependency_owner,source_dependency_incarnation,source_dependency_sequence,source_dependency_publication_id,source_dependency_provider FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}},
	}
	for i := range defs {
		defs[i].CallSites = []string{"writer/enrichment_seal_history.go:readEnrichmentSealHistory"}
		bindings := []any{"missing", make([]byte, 16), int64(1)}
		if defs[i].ID == sealHistoryOwner {
			bindings = []any{"missing", int64(1)}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "missing_exact_key", Bindings: bindings}}
	}
	return defs
}

type enrichmentSealHistory struct {
	decision receiptDecision
	saved    savedSealReceipt
}

// Public-history semantics only. W must check CURRENT disclosure before this
// read and at release. Its private pinned-proof path is deliberately separate:
// a retained ACKed proof can NEVER resurrect an Applied/public result here.
// The caller owns a funded finite snapshot recipe and qualified compiled IDs.
func readEnrichmentSealHistory(ctx context.Context, scope *governor.ReadScope, purpose CommandID, request EnrichmentReceiptRequest) (out enrichmentSealHistory, err error) {
	if ctx == nil || scope == nil || (purpose != EvidenceSeal && purpose != CreditSourceSeal) || request.ProviderOperationID == "" || request.LibraryID <= 0 || request.EntityID <= 0 || request.Receipt.Identity.Sequence <= 0 {
		return out, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, scope.Context(), out, err) }()
	ownerValues, err := scope.Query(sealHistoryOwner, request.ProviderOperationID, request.EntityID)
	if err != nil {
		return out, err
	}
	var owner receiptOwnerFacts
	if len(ownerValues) > 1 {
		return out, ErrResolveRequired
	}
	if len(ownerValues) == 1 {
		if len(ownerValues[0]) != 3 {
			return out, ErrResolveRequired
		}
		n := savedSealNativeRow{values: ownerValues[0]}
		copy(owner.incarnation[:], n.blob(0, 16))
		owner.lastSequence, owner.acknowledgedSequence = n.integer(1), n.integer(2)
		if n.err != nil {
			return out, n.err
		}
		owner.exists = true
	}
	headerValues, err := scope.Query(sealHistoryHeader, request.ProviderOperationID, request.Receipt.Identity.Incarnation[:], request.Receipt.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var found receiptFacts
	if len(headerValues) > 1 {
		return out, ErrResolveRequired
	}
	if len(headerValues) == 1 {
		if len(headerValues[0]) != 6 {
			return out, ErrResolveRequired
		}
		n := savedSealNativeRow{values: headerValues[0]}
		found.identity.command = request.Receipt.Identity
		copy(found.identity.command.OperationID[:], n.blob(0, 16))
		copy(found.identity.digest[:], n.blob(1, 32))
		switch n.text(2) {
		case "evidence_seal":
			found.kind = EvidenceSeal
		case "credit_source_seal":
			found.kind = CreditSourceSeal
		default:
			return out, ErrConflict
		}
		found.resultCount = n.integer(3)
		copy(found.resultDigest[:], n.blob(4, 32))
		if n.integer(5) != request.EntityID {
			return out, ErrConflict
		}
		if n.err != nil {
			return out, n.err
		}
		found.exists = true
	}
	expected := receiptIdentity{request.Receipt.Identity, request.Receipt.RequestDigest}
	decision, err := classifyReceipt(expected, purpose, owner, found)
	if err != nil {
		return out, err
	}
	out.decision = decision
	if decision != receiptReplay {
		return out, nil
	}
	// The same immutable snapshot supplies BOTH native octet counts and copy.
	// Every variable column is mapped, including nullable foreign-dependency
	// fields which the saved decoder must reject rather than silently omit.
	row, err := scope.QueryExactBytes(sealReceiptLengths, sealHistoryPayload, []int{0, 1, 3, 4, 5, 9, 11, 15, 17, 19, 20, 21, 22, 23, 24, 25, 26, 28, 31, 32, 36, 37, 39, 40, 42, 43}, request.ProviderOperationID, request.Receipt.Identity.Incarnation[:], request.Receipt.Identity.Sequence)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	saved, err := decodeSavedSealReceipt(row)
	if err != nil {
		return out, err
	}
	if saved.identity != found.identity || saved.kind != found.kind || [32]byte(saved.resultDigest) != found.resultDigest || saved.facts.ProviderOperationID != request.ProviderOperationID || saved.facts.EntityID != request.EntityID || saved.facts.ResultCount != found.resultCount {
		return out, ErrConflict
	}
	out.saved = saved
	return out, nil
}
