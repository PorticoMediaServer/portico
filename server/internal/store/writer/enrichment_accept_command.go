package writer

// Frozen ORIGINAL acceptance input. The original BEGIN remains a distinct
// historical command: its identity/request/result are never acceptance aliases.
// W's source verifier binds the FINAL pointer, identity/digest and EnrichAccept
// turn while independently reading those immutable original seal references.
type enrichAcceptCommand struct {
	identity      CommandIdentity
	digest        RequestDigest
	originalBegin *effectiveStageBeginCommand
	request       enrichAcceptRequest
	valid         bool
}

type enrichAcceptRequest struct {
	variant       string
	beginIdentity CommandIdentity
	beginRequest  RequestDigest
	beginResult   EffectiveStageBeginResult
	owner         effectiveBeginOwner
	entity        effectiveBeginEntity
	producer      effectiveBeginProducer
	image         enrichAcceptImage
	rows          []effectiveCreditTuple
}

// Complete prospective image intent and observed current revision. Actual
// native rows/count/chain/source/owner/context still require verification; this
// is neither a checkpoint capability nor caller-selected admission policy.
type enrichAcceptImage struct {
	id, revision             int64
	header                   effectiveBeginHeader
	stagedCount, stagedBytes int64
	seed, chain              [32]byte
}
