package writer

import "reflect"

type basicStageOperation uint8
type basicSealOperation uint8

const (
	basicStageBeginInput basicStageOperation = iota + 1
	basicStageFactInput
)
const basicSealStreamInput basicSealOperation = 1

// FIRST constructors cover only these implemented closed operations. Additional
// payloads require their own validated variant before the dispatcher enables
// them. No interface, callback, caller kind or runtime capability is stored.
type basicStageRequest struct {
	original       basicOriginalScope
	purpose        BasicPurpose
	operation      basicStageOperation
	begin          basicBeginEffects
	fact           basicStreamFactStage
	changeRevision int64 // existing change only; allocated BEGIN revision is native
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
	source, err := basicStageSource(in)
	if err != nil {
		return nil, err
	}
	if err = basicOriginalPreflight(identity, in.original, source); err != nil {
		return nil, err
	}
	switch in.operation {
	case basicStageBeginInput:
		if err = validateBasicOrdinaryCounts(source.libraryID, in.begin.shape, in.begin.counts); err != nil {
			return nil, err
		}
	case basicStageFactInput:
		if source.changeID <= 0 || source.inputRevision <= 0 || in.changeRevision <= 0 {
			return nil, invalid("basic_stage_fact")
		}
	}
	digest, err := canonicalRequestDigest(BasicStage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	in.original = cloneBasicOriginal(in.original)
	if in.operation == basicStageBeginInput {
		in.begin.source = cloneBasicSource(in.begin.source)
		in.begin.shape.CUEOrdinals = cloneBasicRequestSlice(in.begin.shape.CUEOrdinals)
		in.begin.shape.EpisodeIDs = cloneBasicRequestSlice(in.begin.shape.EpisodeIDs)
		in.begin.shape.VersionAssetIDs = cloneBasicRequestSlice(in.begin.shape.VersionAssetIDs)
		in.begin.shape.Streams = cloneBasicRequestSlice(in.begin.shape.Streams)
		in.begin.shape.AuthoredParentIDs = cloneBasicRequestSlice(in.begin.shape.AuthoredParentIDs)
	} else {
		in.fact = cloneBasicSource(in.fact)
	}
	return &basicStageCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

func basicStageSource(in basicStageRequest) (basicStreamFactStage, error) {
	if in.purpose != BasicPurposeStage {
		return basicStreamFactStage{}, ErrInvalid
	}
	switch in.operation {
	case basicStageBeginInput:
		if in.fact != (basicStreamFactStage{}) || in.changeRevision != 0 || in.begin.source.changeID != 0 || in.begin.source.inputRevision != 0 {
			return basicStreamFactStage{}, ErrInvalid
		}
		return in.begin.source, nil
	case basicStageFactInput:
		if !reflect.ValueOf(in.begin).IsZero() {
			return basicStreamFactStage{}, ErrInvalid
		}
		return in.fact, nil
	}
	return basicStreamFactStage{}, invalid("basic_stage_operation")
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
