package writer

// IA23's finish and cleanup helpers are same-turn releases: each installs the
// shared finish rule before its first return, so a recovery proof, a bounded
// retirement result or a saved fixed-slot result can never be disclosed after
// the caller's lifetime is gone.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ia_refresh_finish",
		names: []string{
			"runIARefreshFinish", "iaRefreshFinishTerminal",
			"iaRefreshFinishVerify", "iaRefreshFinishVerifyKey", "iaRefreshFinishRead",
			"iaRefreshFinishPairPage", "iaRefreshFinishIDPage",
			"readIARefreshFinishFacts", "readIARefreshSlot",
			"iaRefreshReclaimTurn", "iaRefreshReclaimQuantum",
			"iaRefreshReclaimReceipts", "iaRefreshReclaimReceiptDrop",
			"iaRefreshReclaimCharge", "iaRefreshReclaimDrop",
			"iaRefreshSettleBudget",
		},
		bypasses: []string{
			`func iaRefreshFinishVerify(ctx context.Context)(checked int64,err error){return 0,nil}`,
			`func iaRefreshReclaimCharge(ctx context.Context)(charge int64,exists bool,err error){return 0,false,nil}`,
		},
	})
}
