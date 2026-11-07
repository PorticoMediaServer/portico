package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "uo-operations",
		resultTypes: []string{"uoNoticeEffect", "uoNoticeAckEffect", "uoNoticeReclaimEffect", "uoNoticeBulkState", "uoJobLease", "uoOperationResult"},
		names:       []string{"reserveUONoticeOrdinals", "uoNoticeEvent", "uoNoticeVictims", "enqueueUOWebhook", "reclaimUOWebhook", "persistUOOperationResult"},
		bypasses:    []string{`func enqueueUOWebhook(ctx context.Context)(out string,err error){return "saved",nil}`},
	})
}
