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
	v, err := preflightCanonicalResult(identity, result)
	if err != nil {
		return ResultDigest{}, err
	}
	e := requestEncoder{h: sha256.New()}
	if err := writeEffectiveBeginResult(&e, identity, request, v); err != nil {
		return ResultDigest{}, err
	}
	var digest ResultDigest
	copy(digest[:], e.h.Sum(nil))
	return digest, nil
}

func preflightCanonicalResult(identity CommandIdentity, result any) (effectiveBeginResult, error) {
	v, ok := result.(effectiveBeginResult)
	if !ok || identity.Sequence <= 0 || v.ProviderOperationID == "" || v.EntityID <= 0 ||
		v.ObservedCatalogRevision <= 0 || v.AppliedMS < 0 || v.ImageID <= 0 || v.ImageRevision <= 0 ||
		v.NextOrdinal != 0 || v.PublicationID != nil || v.ResultCount != 0 || v.MemberCohortLength != 0 {
		return effectiveBeginResult{}, invalid("effective_begin_result")
	}
	// Check the COMPLETE prospective size before scanning/copying/hashing either
	// TEXT payload. Subtraction avoids addition overflow. This result ceiling is
	// independent of future request-codec preparation policy changes.
	remaining := maxCanonicalResultBytes - effectiveBeginFixedBytes
	if len(v.ProviderOperationID) > remaining || len(v.AfterItem) > remaining-len(v.ProviderOperationID) ||
		!utf8.ValidString(v.ProviderOperationID) || !utf8.ValidString(v.AfterItem) {
		return effectiveBeginResult{}, invalid("canonical_result_preflight")
	}
	return v, nil
}

func encodeCanonicalResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, result any) error {
	v, err := preflightCanonicalResult(identity, result)
	if err != nil {
		return err
	}
	return writeEffectiveBeginResult(e, identity, request, v)
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
