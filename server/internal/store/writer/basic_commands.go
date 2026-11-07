package writer

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// Original native values only. Current disclosure, preparation custody and W's
// grant are transient outer inputs and never become request authority here.
type basicOriginalScope struct {
	scope  inventoryScope
	source inventorySource
	run    inventoryRun
}

type basicStageOperation uint8
type basicSealOperation uint8

const basicStageBeginInput basicStageOperation = 1
const basicSealStreamInput basicSealOperation = 1

// FIRST constructors cover only these implemented closed operations. Additional
// payloads require their own validated variant before the dispatcher enables
// them. No interface, callback, caller kind or runtime capability is stored.
type basicStageRequest struct {
	original  basicOriginalScope
	purpose   BasicPurpose
	operation basicStageOperation
	begin     basicBeginEffects
}
type basicSealRequest struct {
	original       basicOriginalScope
	purpose        BasicPurpose
	operation      basicSealOperation
	source         basicStreamFactStage
	changeRevision int64
}
type basicStageCommand struct {
	identity CommandIdentity
	request  basicStageRequest
	digest   RequestDigest
	valid    bool
}
type basicSealCommand struct {
	identity CommandIdentity
	request  basicSealRequest
	digest   RequestDigest
	valid    bool
}

func newBasicStageCommand(identity CommandIdentity, in basicStageRequest) (*basicStageCommand, error) {
	if err := basicOriginalPreflight(identity, in.original, in.begin.source); err != nil {
		return nil, err
	}
	if in.purpose != BasicPurposeStage || in.operation != basicStageBeginInput || in.begin.source.changeID != 0 || in.begin.source.inputRevision != 0 {
		return nil, invalid("basic_stage_operation")
	}
	if err := validateBasicOrdinaryCounts(in.begin.source.libraryID, in.begin.shape, in.begin.counts); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(BasicStage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	in.original = cloneBasicOriginal(in.original)
	in.begin.source = cloneBasicSource(in.begin.source)
	in.begin.shape.CUEOrdinals = cloneBasicRequestSlice(in.begin.shape.CUEOrdinals)
	in.begin.shape.EpisodeIDs = cloneBasicRequestSlice(in.begin.shape.EpisodeIDs)
	in.begin.shape.VersionAssetIDs = cloneBasicRequestSlice(in.begin.shape.VersionAssetIDs)
	in.begin.shape.Streams = cloneBasicRequestSlice(in.begin.shape.Streams)
	in.begin.shape.AuthoredParentIDs = cloneBasicRequestSlice(in.begin.shape.AuthoredParentIDs)
	return &basicStageCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func newBasicSealCommand(identity CommandIdentity, in basicSealRequest) (*basicSealCommand, error) {
	if err := basicOriginalPreflight(identity, in.original, in.source); err != nil {
		return nil, err
	}
	if in.purpose != BasicPurposeSeal || in.operation != basicSealStreamInput || in.source.changeID <= 0 || in.source.inputRevision <= 0 || in.changeRevision <= 0 {
		return nil, invalid("basic_seal_operation")
	}
	digest, err := canonicalRequestDigest(BasicSeal, 1, identity, in)
	if err != nil {
		return nil, err
	}
	in.original = cloneBasicOriginal(in.original)
	in.source = cloneBasicSource(in.source)
	return &basicSealCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func basicOriginalPreflight(identity CommandIdentity, original basicOriginalScope, s basicStreamFactStage) error {
	// Complete native variable payload before UTF-8, clones or shared hashing.
	remaining := maxCanonicalRequestBytes
	if len(original.source.resolvedRoot) > remaining {
		return ErrCap
	}
	remaining -= len(original.source.resolvedRoot)
	texts := [...]string{original.scope.runID, original.scope.libraryKey, original.source.id, original.source.incarnation, original.source.rootIdentity,
		original.run.phase, original.run.scanMode, s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint}
	for _, text := range texts {
		if len(text) > remaining {
			return ErrCap
		}
		remaining -= len(text)
	}
	if bytes.IndexByte(original.source.resolvedRoot, 0) >= 0 {
		return invalid("basic_native_root")
	}
	if !utf8.Valid(original.source.resolvedRoot) {
		return &PolicyError{Field: "basic_native_root", Cause: ErrFilenameUnrepresentable}
	}
	for _, text := range texts {
		if text == "" || !utf8.ValidString(text) {
			return invalid("basic_original_text")
		}
	}
	r, source, scope := original.run, original.source, original.scope
	if identity.Sequence <= 0 || scope.libraryID <= 0 || source.generation <= 0 || len(source.resolvedRoot) == 0 || source.resolvedRoot[0] != '/' ||
		r.revision <= 0 || r.policyRevision <= 0 || r.leaseOwner <= 0 || r.leaseGeneration <= 0 || r.leaseExpiresMS <= 0 ||
		r.lastSequence != identity.Sequence-1 || r.acknowledgedSequence != r.lastSequence || !inventoryScanMode(r.scanMode) ||
		s.runID != scope.runID || s.libraryID != scope.libraryID || s.sourceID != source.id || s.incarnation != source.incarnation ||
		s.sourceGeneration != source.generation || s.policyRevision != r.policyRevision || s.assetID <= 0 || s.expectedEpoch <= 0 ||
		s.factRevision <= 0 || s.size < 0 || s.detailVersion < 0 || s.witnessFingerprint == ([32]byte{}) {
		return invalid("basic_original_scope")
	}
	// Phase eligibility, lease expiry, native equality and physical proof are
	// FRESH gates. A pure constructor cannot attest any of those observations.
	return nil
}

func cloneBasicOriginal(in basicOriginalScope) basicOriginalScope {
	in.scope.runID = strings.Clone(in.scope.runID)
	in.scope.libraryKey = strings.Clone(in.scope.libraryKey)
	in.source.id = strings.Clone(in.source.id)
	in.source.incarnation = strings.Clone(in.source.incarnation)
	in.source.rootIdentity = strings.Clone(in.source.rootIdentity)
	in.source.resolvedRoot = bytes.Clone(in.source.resolvedRoot)
	in.run.phase = strings.Clone(in.run.phase)
	in.run.scanMode = strings.Clone(in.run.scanMode)
	return in
}

func cloneBasicRequestSlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in)) // retain nil-vs-empty wire identity, no caller backing array
	copy(out, in)
	return out
}

func (c *basicStageCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid {
		return BasicReceiptRequest{}, invalid("basic_stage_command")
	}
	return basicOriginalReference(c.request.original.scope, BasicPurposeStage, c.identity, c.digest), nil
}
func (c *basicSealCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid {
		return BasicReceiptRequest{}, invalid("basic_seal_command")
	}
	return basicOriginalReference(c.request.original.scope, BasicPurposeSeal, c.identity, c.digest), nil
}
func basicOriginalReference(scope inventoryScope, purpose BasicPurpose, identity CommandIdentity, digest RequestDigest) BasicReceiptRequest {
	return BasicReceiptRequest{Purpose: purpose, Scan: ScanReceiptRequest{RunID: scope.runID, LibraryID: scope.libraryID, LibraryKey: scope.libraryKey,
		Receipt: ReceiptReference{Identity: identity, RequestDigest: digest}}}
}
