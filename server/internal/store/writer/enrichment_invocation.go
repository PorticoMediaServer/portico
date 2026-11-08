package writer

// Closed domain data for W's ONE common commandInvocation union. No member is
// a callback, registry service, SQL handle, resource grant or provenance cap.
// W owns runEnrichment, current disclosure, the per-purpose qualified plans,
// shared physical terminal and public release. This file cannot execute it.
type enrichmentAction uint8

const (
	enrichmentApply enrichmentAction = iota + 1
	enrichmentResolve
	enrichmentAcknowledge
)

type enrichmentInvocation struct {
	action     enrichmentAction
	purpose    CommandID
	disclosure *EnrichmentDisclosure
	request    EnrichmentReceiptRequest // ORIGINAL identity/digest/native owner
	payload    *enrichmentFrozenPayload // nil for Resolve/ACK
}

// Only future complete typed preparation constructors may produce a frozen
// payload. An identity/digest or nonnil arm is never authority/proof. Fresh
// execution remains W-gated, independently of this data shape. No constructor
// or public accepting entry is introduced by this declaration.
type enrichmentFrozenPayload struct {
	scope          sealOwnerScope
	variant        enrichmentPayloadVariant
	evidenceBegin  *enrichmentEvidenceBeginPayload
	evidenceAppend *enrichmentEvidenceAppendPayload
	sourceBegin    *enrichmentSourceBeginPayload
	sourceAppend   *enrichmentSourceAppendPayload
	evidenceSeal   *enrichmentEvidenceSealPayload
	sourceSeal     *enrichmentSourceSealPayload
	accept         *enrichAcceptCommand
}

type enrichmentPayloadVariant uint8

const (
	enrichmentEvidenceBegin enrichmentPayloadVariant = iota + 1
	enrichmentEvidenceAppend
	enrichmentSourceBegin
	enrichmentSourceAppend
	enrichmentEvidenceSeal
	enrichmentSourceSeal
	enrichmentAccept
)

type enrichmentEvidenceBeginPayload struct {
	prefix  evidenceStagePrefix
	content EvidenceStagePayload
}
type enrichmentEvidenceAppendPayload struct {
	prefix  evidenceStagePrefix
	credits []EvidenceCreditInput
}
type enrichmentSourceBeginPayload struct {
	header                         normalizedSourceHeader
	root                           sourceSealRoot
	upstreamRevision, upstreamPins int64
}
type enrichmentSourceAppendPayload struct {
	prefix  sourceStagePrefix
	members []sourceStageMember
}
type enrichmentEvidenceSealPayload struct {
	root                                     evidenceSealRoot
	content                                  sealReceiptContent
	oldCharge, providerCharge, providerOther int64
}
type enrichmentSourceSealPayload struct {
	root                           sourceSealRoot
	content                        sealReceiptContent
	upstreamRevision, upstreamPins int64
}

// Native/body values only. There is deliberately no ReceiptOutcome/Applied.
// W's closed result helper and actual terminal determine externally visible
// outcomes; body prefixes/progress cannot synthesize a successful receipt.
type enrichmentNativeResult struct {
	evidenceBegin  *evidenceBeginResult
	evidencePrefix *evidenceStagePrefix
	sourceProgress *sourceStageProgress
	evidenceSeal   *evidenceSealResult
	sourceSeal     *creditSourceSealResult
	accepted       *enrichAcceptResult
}
