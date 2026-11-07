package writer

import (
	"math"
	"reflect"
)

type basicAcceptOperation uint8

const basicAcceptFirstOrdinaryLeaf basicAcceptOperation = 1

type basicAcceptRequest struct {
	original   basicOriginalScope
	purpose    BasicPurpose
	operation  basicAcceptOperation
	activation basicActivationEffects
	entityID   int64
	duration   float64
	height     int64
	analysis   basicObjectAnalysis
	nfo        *basicNFOSet
	artwork    []basicArtworkInput
	oldScalars []basicFirstOldScalar
}
type basicAcceptCommand struct {
	identity CommandIdentity
	request  basicAcceptRequest
	digest   RequestDigest
	valid    bool
}

// Complete retained source/local inputs precede validation maps, clones, JSON
// work and the ONE common encoder. This is a prospective peak, never a grant.
func basicAcceptRequiredBytes(in basicAcceptRequest) (uint64, error) {
	remaining := uint64(maxCanonicalRequestBytes)
	if err := basicAcceptPayloadBytes(reflect.ValueOf(in), &remaining); err != nil {
		return 0, err
	}
	payload := uint64(maxCanonicalRequestBytes) - remaining
	// Includes before+after NFO, prepared artwork, native binds/readback,
	// complete result wire and parsing/clone scratch. Native reserve is separate.
	return 16384 + 64*payload + 1024*uint64(len(in.artwork)), nil
}
func basicAcceptPayloadBytes(v reflect.Value, remaining *uint64) error {
	switch v.Kind() {
	case reflect.Pointer:
		if *remaining < 1 {
			return ErrCap
		}
		*remaining--
		if !v.IsNil() {
			return basicAcceptPayloadBytes(v.Elem(), remaining)
		}
		return nil
	case reflect.Struct:
		for n := 0; n < v.NumField(); n++ {
			if err := basicAcceptPayloadBytes(v.Field(n), remaining); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice, reflect.Array:
		if *remaining < 8 {
			return ErrCap
		}
		*remaining -= 8
		for n := 0; n < v.Len(); n++ {
			if err := basicAcceptPayloadBytes(v.Index(n), remaining); err != nil {
				return err
			}
		}
		return nil
	default:
		return basicCopyPayloadBytes(v, remaining)
	}
}
func newBasicAcceptCommand(identity CommandIdentity, in basicAcceptRequest) (*basicAcceptCommand, error) {
	if _, err := basicAcceptRequiredBytes(in); err != nil {
		return nil, err
	}
	s := in.activation.source
	if err := basicOriginalPreflight(identity, in.original, s); err != nil {
		return nil, err
	}
	if in.purpose != BasicPurposeAccept || in.operation != basicAcceptFirstOrdinaryLeaf || in.entityID <= 0 || in.duration <= 0 || math.IsNaN(in.duration) || math.IsInf(in.duration, 0) || in.height < 0 || in.activation.changeRevision <= 0 || in.activation.changeRevision > math.MaxInt64-2 || in.activation.familyMask != 511 || in.activation.shapeID <= 0 || s.expectedEpoch == math.MaxInt64 || in.activation.invalidationRevision <= 0 || in.activation.fileSizeRevision <= 0 || in.activation.ownerObject.Valid || in.activation.ownerRevision.Valid || in.activation.ownerObject.String != "" || in.activation.ownerRevision.String != "" || !in.analysis.valid || in.analysis.source != s || in.analysis.libraryKey != in.original.scope.libraryKey {
		return nil, ErrInvalid
	}
	if _, err := validateBasicFirstScalarOld(in.oldScalars); err != nil {
		return nil, err
	}
	// Revalidate and own every local input. They are inert observations, with
	// no preparation capability, mutable proof or source lookup in the request.
	analysis, err := prepareBasicObjectAnalysis(s, in.analysis.libraryKey, in.analysis.algorithm, in.analysis.policyJSON, in.analysis.completedJSON, in.analysis.subtitleTextPending)
	if err != nil {
		return nil, err
	}
	in.analysis = *analysis
	if in.nfo != nil {
		if !in.nfo.valid || in.nfo.source != s || in.nfo.library != in.original.scope.libraryKey {
			return nil, ErrInvalid
		}
		in.nfo, err = prepareBasicNFOSet(s, in.nfo.library, in.nfo.before, in.nfo.after, in.nfo.beforeStatus, in.nfo.afterStatus, true)
		if err != nil {
			return nil, err
		}
	}
	// A completed local_metadata operation requires its complete evidence set,
	// including observed empty. An absent set cannot masquerade as known absence.
	if basicAnalysisHasOperation(in.analysis.completedJSON, "local_metadata") && in.nfo == nil {
		return nil, ErrInvalid
	}
	owned := make([]basicArtworkInput, len(in.artwork))
	for n, a := range in.artwork {
		if !a.valid || a.source != s || a.entityID != in.entityID {
			return nil, ErrInvalid
		}
		for j := 0; j < n; j++ {
			if owned[j].role == a.role {
				return nil, ErrInvalid
			}
		}
		p, e := prepareBasicArtwork(a)
		if e != nil {
			return nil, e
		}
		owned[n] = *p
	}
	if in.artwork == nil {
		owned = nil
	}
	in.artwork = owned
	in.oldScalars, err = cloneBasicFirstScalarOld(in.oldScalars)
	if err != nil {
		return nil, err
	}
	in.original = cloneBasicOriginal(in.original)
	in.activation.source = cloneBasicSource(s)
	digest, err := canonicalRequestDigest(BasicAccept, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &basicAcceptCommand{identity, in, digest, true}, nil
}
func (c *basicAcceptCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid || c.request.purpose != BasicPurposeAccept {
		return BasicReceiptRequest{}, ErrInvalid
	}
	return BasicReceiptRequest{Purpose: BasicPurposeAccept, Scan: ScanReceiptRequest{RunID: c.request.original.scope.runID, LibraryID: c.request.original.scope.libraryID, LibraryKey: c.request.original.scope.libraryKey, Receipt: ReceiptReference{Identity: c.identity, RequestDigest: c.digest}}}, nil
}
