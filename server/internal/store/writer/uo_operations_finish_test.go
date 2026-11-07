package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "uo-operations",
		resultTypes: []string{"uoNoticeEffect", "uoNoticeAckEffect", "uoNoticeReclaimEffect", "uoNoticeBulkState", "uoJobLease", "uoOperationResult", "uoRootLookup", "uoRootReclaimResult", "uoRootStoredSlot"},
		names:       []string{"reserveUONoticeOrdinals", "uoNoticeEvent", "uoNoticeVictims", "enqueueUOWebhook", "reclaimUOWebhook", "persistUORootOperation", "readUORootPayload", "probeUORootChild"},
		prefixes:    []string{"acknowledgeUORoot"},
		bypasses:    []string{`func enqueueUOWebhook(ctx context.Context)(out string,err error){return "saved",nil}`},
	})
}
