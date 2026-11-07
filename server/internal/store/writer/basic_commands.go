package writer

// This first executable slice admits only the existing one-fact Stage/Seal.
// Values 1 (BEGIN) and later operations are not aliases or callable variants.
// A later frozen request shape needs its own explicit request version.
type basicStageOperation uint8
type basicSealOperation uint8

const basicStageFactInput basicStageOperation = 2
const basicSealStreamInput basicSealOperation = 1

type basicStageRequest struct {
	original       basicOriginalScope
	purpose        BasicPurpose
	operation      basicStageOperation
	fact           basicStreamFactStage
	changeRevision int64
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

func basicStageSource(in basicStageRequest) (basicStreamFactStage, error) {
	if in.purpose != BasicPurposeStage || in.operation != basicStageFactInput || in.changeRevision <= 0 || in.fact.changeID <= 0 || in.fact.inputRevision <= 0 {
		return basicStreamFactStage{}, ErrInvalid
	}
	return in.fact, nil
}
func newBasicStageCommand(identity CommandIdentity, in basicStageRequest) (*basicStageCommand, error) {
	source, err := basicStageSource(in)
	if err != nil {
		return nil, err
	}
	if _, err = basicControlRequiredBytes(in.original, source); err != nil {
		return nil, err
	}
	if err = basicOriginalPreflight(identity, in.original, source); err != nil {
		return nil, err
	}
	in.original = cloneBasicOriginal(in.original)
	in.fact = cloneBasicSource(in.fact)
	digest, err := canonicalRequestDigest(BasicStage, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &basicStageCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}
func newBasicSealCommand(identity CommandIdentity, in basicSealRequest) (*basicSealCommand, error) {
	if in.purpose != BasicPurposeSeal || in.operation != basicSealStreamInput || in.changeRevision <= 0 || in.source.changeID <= 0 || in.source.inputRevision <= 0 {
		return nil, ErrInvalid
	}
	if _, err := basicControlRequiredBytes(in.original, in.source); err != nil {
		return nil, err
	}
	if err := basicOriginalPreflight(identity, in.original, in.source); err != nil {
		return nil, err
	}
	in.original = cloneBasicOriginal(in.original)
	in.source = cloneBasicSource(in.source)
	digest, err := canonicalRequestDigest(BasicSeal, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &basicSealCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}
func (c *basicStageCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid {
		return BasicReceiptRequest{}, ErrInvalid
	}
	return basicOriginalReference(c.request.original.scope, BasicPurposeStage, c.identity, c.digest), nil
}
func (c *basicSealCommand) receiptRequest() (BasicReceiptRequest, error) {
	if c == nil || !c.valid {
		return BasicReceiptRequest{}, ErrInvalid
	}
	return basicOriginalReference(c.request.original.scope, BasicPurposeSeal, c.identity, c.digest), nil
}
func basicOriginalReference(scope inventoryScope, purpose BasicPurpose, identity CommandIdentity, digest RequestDigest) BasicReceiptRequest {
	return BasicReceiptRequest{Purpose: purpose, Scan: ScanReceiptRequest{RunID: scope.runID, LibraryID: scope.libraryID, LibraryKey: scope.libraryKey, Receipt: ReceiptReference{Identity: identity, RequestDigest: digest}}}
}
