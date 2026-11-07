package writer

import (
	"crypto/sha256"
	"errors"
	"unicode/utf8"
)

// ResultDigest commits to an immutable typed result, including its ORIGINAL
// request binding. It is neither a RequestDigest nor publication/manifest proof.
type ResultDigest [32]byte

const canonicalResultCodec = "portico-writer-result-sha256-v1\x00"
const maxCanonicalResultBytes = 32 << 20
const effectiveBeginFixedBytes = 238 // complete v1 wire excluding the two TEXT byte payloads

// effectiveBeginResult is the complete native scalar BEGIN result. Its fixed
// fields remain explicit so malformed historical shapes cannot be relabeled as
// this variant. Decode from the immutable receipt, never current mutable heads.
type effectiveBeginResult struct {
	ProviderOperationID     string
	EntityID                int64
	ObservedCatalogRevision int64
	AppliedMS               int64
	AfterItem               string
	NextOrdinal             int64
	PublicationID           *string
	ImageID                 int64
	ImageRevision           int64
	ResultCount             int64
	MemberCohortLength      int64
}

func effectiveBeginResultDigest(identity CommandIdentity, request RequestDigest, result effectiveBeginResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, result)
}

// canonicalResultDigest accepts only explicitly compiled result schemas. A new
// domain cannot register a codec or make an arbitrary output a canonical result.
func canonicalResultDigest(identity CommandIdentity, request RequestDigest, result any) (ResultDigest, error) {
	if err := preflightCanonicalResult(identity, request, result); err != nil {
		return ResultDigest{}, err
	}
	e := requestEncoder{h: sha256.New()}
	if err := writeCanonicalResult(&e, identity, request, result); err != nil {
		return ResultDigest{}, err
	}
	var digest ResultDigest
	copy(digest[:], e.h.Sum(nil))
	return digest, nil
}

func preflightCanonicalResult(identity CommandIdentity, request RequestDigest, result any) error {
	if identity.Sequence <= 0 {
		return invalid("result_command_sequence")
	}
	switch v := result.(type) {
	case ipArtworkDiscoverySavedResult:
		return preflightIPArtworkDiscoveryResult(v)
	case ipPolicyResult:
		return preflightIPPolicyResult(v)
	case ipRefreshSavedResult:
		return preflightIPRefreshResult(v)
	case ipArtworkMaterialResult:
		return preflightIPArtworkMaterialResult(v)
	case ipReceiptReclaimResult:
		return preflightIPReceiptReclaimResult(v)
	case ipTextBundleSavedResult:
		return preflightIPTextBundleResult(v)
	case ipEditBulkSavedResult:
		return preflightIPEditBulkResult(v)
	case ipTextMutationSavedResult:
		return preflightIPTextMutationResult(v)
	case ipTextLockResult:
		return preflightIPTextLockResult(v)
	case evidenceSealResult:
		return preflightEnrichmentSealResult(EvidenceSeal, v.facts)
	case creditSourceSealResult:
		return preflightEnrichmentSealResult(CreditSourceSeal, v.facts)
	case evidenceBeginResult:
		return preflightEvidenceBeginResult(v)
	case effectiveBeginResult:
		return preflightEffectiveBeginResult(v)
	case inventoryAcceptResult:
		return preflightInventoryAcceptResult(v)
	case BasicResult:
		return preflightBasicResult(v)
	case inventoryCreateResult:
		return preflightScanRunLifecycleResult(InventoryCreate, identity, request, v.facts)
	case inventoryClaimResult:
		return preflightScanRunLifecycleResult(InventoryClaim, identity, request, v.facts)
	default:
		return invalid("canonical_result_schema")
	}
}

func preflightEffectiveBeginResult(v effectiveBeginResult) error {
	if v.ProviderOperationID == "" || v.EntityID <= 0 ||
		v.ObservedCatalogRevision <= 0 || v.AppliedMS < 0 || v.ImageID <= 0 || v.ImageRevision <= 0 ||
		v.NextOrdinal != 0 || v.PublicationID != nil || v.ResultCount != 0 || v.MemberCohortLength != 0 {
		return invalid("effective_begin_result")
	}
	// Check the COMPLETE prospective size before scanning/copying/hashing either
	// TEXT payload. Subtraction avoids addition overflow. This result ceiling is
	// independent of future request-codec preparation policy changes.
	remaining := maxCanonicalResultBytes - effectiveBeginFixedBytes
	if len(v.ProviderOperationID) > remaining || len(v.AfterItem) > remaining-len(v.ProviderOperationID) ||
		!utf8.ValidString(v.ProviderOperationID) || !utf8.ValidString(v.AfterItem) {
		return invalid("canonical_result_preflight")
	}
	return nil
}

func encodeCanonicalResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, result any) error {
	if err := preflightCanonicalResult(identity, request, result); err != nil {
		return err
	}
	return writeCanonicalResult(e, identity, request, result)
}

func writeCanonicalResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, result any) error {
	switch v := result.(type) {
	case ipArtworkDiscoverySavedResult:
		return writeIPArtworkDiscoveryResult(e, identity, request, v)
	case ipPolicyResult:
		return writeIPPolicyResult(e, identity, request, v)
	case ipRefreshSavedResult:
		return writeIPRefreshResult(e, identity, request, v)
	case ipArtworkMaterialResult:
		return writeIPArtworkMaterialResult(e, identity, request, v)
	case ipReceiptReclaimResult:
		return writeIPReceiptReclaimResult(e, identity, request, v)
	case ipTextBundleSavedResult:
		return writeIPTextBundleResult(e, identity, request, v)
	case ipEditBulkSavedResult:
		return writeIPEditBulkResult(e, identity, request, v)
	case ipTextMutationSavedResult:
		return writeIPTextMutationResult(e, identity, request, v)
	case ipTextLockResult:
		return writeIPTextLockResult(e, identity, request, v)
	case evidenceSealResult:
		return writeEnrichmentSealResult(e, EvidenceSeal, identity, request, v.facts)
	case creditSourceSealResult:
		return writeEnrichmentSealResult(e, CreditSourceSeal, identity, request, v.facts)
	case evidenceBeginResult:
		return writeEvidenceBeginResult(e, identity, request, v)
	case effectiveBeginResult:
		return writeEffectiveBeginResult(e, identity, request, v)
	case inventoryAcceptResult:
		return writeInventoryAcceptResult(e, identity, request, v)
	case BasicResult:
		return writeBasicResult(e, identity, request, v)
	case inventoryCreateResult:
		return writeScanRunLifecycleResult(e, InventoryCreate, identity, request, v.facts)
	case inventoryClaimResult:
		return writeScanRunLifecycleResult(e, InventoryClaim, identity, request, v.facts)
	default:
		return invalid("canonical_result_schema")
	}
}

func writeEffectiveBeginResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v effectiveBeginResult) error {
	// Reuse only the bounded raw-byte/integer/UTF-8 LP primitives. The request
	// codec/reflection/type descriptors are never invoked on generated outputs.
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1}) // semantic result version, independent of CommandID ordinals
	e.text("effective_stage")
	e.text("begin")
	e.text("applied")
	e.text(v.ProviderOperationID)
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.number(uint64(v.EntityID))
	e.number(uint64(v.ObservedCatalogRevision))
	e.number(uint64(v.AppliedMS))
	e.text(v.AfterItem)
	e.number(uint64(v.NextOrdinal))
	e.bytes([]byte{0}) // required NULL publication
	e.number(uint64(v.ImageID))
	e.number(uint64(v.ImageRevision))
	e.number(uint64(v.ResultCount))
	e.number(uint64(v.MemberCohortLength))
	if e.err != nil {
		return errors.Join(invalid("canonical_result"), e.err)
	}
	return nil
}
