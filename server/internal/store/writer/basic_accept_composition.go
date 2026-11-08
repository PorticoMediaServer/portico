package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Trusted profile bounds, not caller authority, source limits or resource
// amounts. Whole units outside this finite profile refuse before fresh effects;
// original history is still resolved first. No default production fit is set.
type basicAcceptBounds struct {
	nfo                          bool
	nfoBefore, nfoAfter, artwork int
}

func (b basicAcceptBounds) valid() bool {
	return b.nfoBefore >= 0 && b.nfoAfter >= 0 && b.artwork >= 0 && b.nfoBefore <= maxCanonicalRequestBytes/192 && b.nfoAfter <= maxCanonicalRequestBytes/192 && b.artwork <= maxCanonicalRequestBytes/128 && (b.nfo || (b.nfoBefore == 0 && b.nfoAfter == 0))
}
func checkBasicAcceptBounds(c *basicAcceptCommand, b basicAcceptBounds) error {
	if c == nil || !c.valid || !b.valid() {
		return ErrInvalid
	}
	in := c.request
	if in.nfo != nil && (!b.nfo || len(in.nfo.before) > b.nfoBefore || len(in.nfo.after) > b.nfoAfter) {
		return ErrCap
	}
	if len(in.artwork) > b.artwork {
		return ErrCap
	}
	return nil
}
func basicFinalMaxUses(b basicAcceptBounds, id stmt.ID) int {
	nfo := 0
	if b.nfo {
		nfo = 1
	}
	switch id {
	case inventoryDisclosureJob, inventoryDisclosureLibrary, scanReceiptOwner, scanReceiptAck, basicReceiptScope, basicReceiptIdentity, basicReceiptHeaderLengths, basicReceiptHeader, scanMemberKeys, scanMemberByteLengths, scanReceiptResults:
		return 1
	case basicControlRun, basicControlSource, basicControlActive, basicControlObject, basicControlLibrary, basicControlPolicy, basicControlJob, basicControlAsset, basicControlPostState, basicAcceptRunCAS, basicControlReceipt, scanResultInsert:
		return 1
	case basicStageHeader, basicStageAsset, basicStageWitness:
		return 1 + nfo + b.artwork
	case basicActivationHeader, basicActivationFact, basicActivationAssetCAS:
		return 1
	case basicActivationChangeCAS:
		return 2
	case basicAcceptOwners, basicSettleForward, basicSettleReverse, basicSettleCollection, basicPublishLeafProof, basicSettleShapeRead:
		return 2
	case basicBeginFirstEntityAssets, basicSettleProof, basicAcceptSuccessor, basicSettleBefore, basicSettleLinkCAS, basicSettleScalarCAS, basicSettleScalarBothNull, basicSettleScalarDurationNull, basicSettleScalarResolutionNull, basicSettleShapeCAS, basicSettleEntityCAS, basicSettleEntityNullCAS, basicPublishScalarOld, basicPublishLinkCAS, basicPublishAssetCAS, basicObjectAnalysisPolicy, basicObjectAnalysisCAS, basicFirstScalarOldKeys, basicStageBudget, basicStageBudgetCAS:
		return 1
	case basicFirstScalarOldText:
		return 3
	case basicFirstScalarOldInteger:
		return 2
	case basicFirstScalarOldReal:
		return 1
	case basicNFOLibrary, basicNFORows, basicNFOStatusRead, basicNFOStatusInsert, basicNFOStatusCAS:
		return nfo
	case basicNFODelete:
		return b.nfoBefore
	case basicNFOInsert:
		return b.nfoAfter
	case basicArtworkEntity, basicArtworkSubject, basicArtworkLink, basicArtworkSelection, basicArtworkJob, basicArtworkInsert:
		return b.artwork
	}
	return 0
}
func basicFinalDefinitions(b basicAcceptBounds) []stmt.Definition {
	if !b.valid() {
		return nil
	}
	groups := [][]stmt.Definition{inventoryDisclosureDefinitions(), basicReceiptDefinitions(), basicReceiptMemberDefinitions(), basicControlDefinitions(), basicStreamStageDefinitions(), basicActivationDefinitions(), basicSettleLeafDefinitions(), basicPublishLeafDefinitions(), basicObjectAnalysisDefinitions(), basicAcceptControlDefinitions(), basicFirstScalarOldDefinitions(), basicNFODefinitions(), basicArtworkDefinitions()}
	for _, d := range scanReceiptDefinitions() {
		switch d.ID {
		case scanReceiptOwner, scanReceiptAck, scanResultInsert:
			groups = append(groups, []stmt.Definition{d})
		}
	}
	var defs []stmt.Definition
	for _, group := range groups {
		for _, d := range group {
			if basicFinalMaxUses(b, d.ID) > 0 {
				defs = append(defs, d)
			}
		}
	}
	return defs
}

// Native row bounds for this finite profile, including overflow/successor rows.
func basicFinalRows(b basicAcceptBounds, id stmt.ID) uint64 {
	switch id {
	case basicNFORows:
		return uint64(b.nfoBefore) + 1
	case basicBeginFirstEntityAssets, basicStageWitness, basicAcceptOwners, basicSettleForward, basicSettleReverse, scanMemberKeys, scanReceiptResults:
		return 2
	case basicFirstScalarOldKeys:
		return 7
	default:
		return 1
	}
}
