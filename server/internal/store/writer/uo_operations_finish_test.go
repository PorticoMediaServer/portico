package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "uo-operations",
		resultTypes: []string{"uoNoticeEffect", "uoNoticeAckEffect", "uoNoticeReclaimEffect", "uoNoticeBulkState", "uoJobLease", "uoOperationResult", "uoRootLookup"},
		names:       []string{"reserveUONoticeOrdinals", "uoNoticeEvent", "uoNoticeVictims", "enqueueUOWebhook", "reclaimUOWebhook", "persistUORootOperation", "readUORootPayload", "acknowledgeUORootOperation"},
		bypasses:    []string{`func enqueueUOWebhook(ctx context.Context)(out string,err error){return "saved",nil}`},
	})
}
