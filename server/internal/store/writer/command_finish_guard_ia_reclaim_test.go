package writer

// IA-10 is its own release family: two owner-specific ACK/reclaim outcomes, the
// retained owner/ledger/unit history values and every scalar helper release a
// caller lifetime through the shared finish rule. The two purposes stay separate
// entries; nothing here is a shared ACK dispatcher.
func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ia_reclaim",
		resultTypes: []string{
			"iaConfigurationReclaimOutcome", "iaRetirementReclaimOutcome",
			"iaConfigurationReclaimOwner", "iaRetirementReclaimOwner",
			"iaConfigurationReclaimLedger", "iaRetirementReclaimUnit", "iaReclaimSaved",
		},
		names: []string{
			"iaConfigurationReclaimProof", "loadIAConfigurationReclaimOwner", "applyIAConfigurationReclaim",
			"iaConfigurationReclaimDropSource", "iaConfigurationReclaimDropHeader", "iaConfigurationReclaimWriteSlot",
			"loadIARetirementReclaimOwner", "applyIARetirementReclaim", "iaRetirementReclaimReceipt",
			"iaRetirementReclaimDrop", "iaRetirementReclaimWriteSlot", "iaReclaimTime",
		},
		bypasses: []string{
			`func reclaimIAConfiguration(ctx context.Context)(out iaConfigurationReclaimOutcome,err error){return out,nil}`,
			`func reclaimIARetirement(ctx context.Context)(out iaRetirementReclaimOutcome,err error){return out,nil}`,
			`func iaConfigurationReclaimDropSource(ctx context.Context)(bytes int64,err error){return 0,nil}`,
			`func iaReclaimTime(ctx context.Context)(value int64,err error){return 0,nil}`,
		},
	})
}
