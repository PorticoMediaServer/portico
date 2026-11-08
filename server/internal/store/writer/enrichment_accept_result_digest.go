package writer

import (
	"errors"
	"unicode/utf8"
)

const enrichAcceptFixedBytes = 220 // complete v1 wire excluding the two TEXT byte payloads

// enrichAcceptResult is the complete native scalar ENRICH ACCEPT result. Its
// fixed fields remain explicit so malformed historical shapes cannot be
// relabeled as this variant. Decode from the immutable receipt, never current
// mutable heads.
type enrichAcceptResult struct {
	ProviderOperationID     string
	EntityID                int64
	AcceptedCatalogRevision int64
	AppliedMS               int64
	AfterItem               string
	ImageID                 int64
	ImageRevision           int64
	RetainedCount           int64
}

func enrichAcceptResultDigest(identity CommandIdentity, request RequestDigest, result enrichAcceptResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, result)
}

func preflightEnrichAcceptResult(v enrichAcceptResult) error {
	if v.ProviderOperationID == "" || v.EntityID <= 0 || v.AcceptedCatalogRevision <= 0 ||
		v.AppliedMS < 0 || v.ImageID <= 0 || v.ImageRevision <= 0 || v.RetainedCount < 0 {
		return invalid("enrich_accept_result")
	}
	// Check the COMPLETE prospective size before scanning/copying/hashing either
	// TEXT payload. Subtraction avoids addition overflow. This result ceiling is
	// independent of future request-codec preparation policy changes.
	remaining := maxCanonicalResultBytes - enrichAcceptFixedBytes
	if len(v.ProviderOperationID) > remaining || len(v.AfterItem) > remaining-len(v.ProviderOperationID) ||
		!utf8.ValidString(v.ProviderOperationID) || !utf8.ValidString(v.AfterItem) {
		return invalid("canonical_result_preflight")
	}
	return nil
}

func writeEnrichAcceptResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v enrichAcceptResult) error {
	// Reuse only the bounded raw-byte/integer/UTF-8 LP primitives. The request
	// codec/reflection/type descriptors are never invoked on generated outputs.
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1}) // semantic result version, independent of CommandID ordinals
	e.text("enrich_accept")
	e.text("accept")
	e.text("applied")
	e.text(v.ProviderOperationID)
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.number(uint64(v.EntityID))
	e.number(uint64(v.AcceptedCatalogRevision))
	e.number(uint64(v.AppliedMS))
	e.text(v.AfterItem)
	e.number(uint64(v.ImageID))
	e.number(uint64(v.ImageRevision))
	e.number(uint64(v.RetainedCount))
	if e.err != nil {
		return errors.Join(invalid("canonical_result"), e.err)
	}
	return nil
}
