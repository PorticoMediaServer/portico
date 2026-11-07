package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "uo-operations",
		resultTypes: []string{"uoNoticeEffect", "uoNoticeAckEffect", "uoNoticeReclaimEffect", "uoNoticeBulkState", "uoJobLease", "uoOperationResult", "uoRootLookup", "uoRootReclaimResult", "uoRootStoredSlot", "uoRootFlagsResult"},
		names:       []string{"reserveUONoticeOrdinals", "uoNoticeEvent", "uoNoticeVictims", "enqueueUOWebhook", "reclaimUOWebhook", "persistUORootOperation", "readUORootPayload", "probeUORootChild"},
		prefixes:    []string{"persistUORoot", "acknowledgeUORoot", "applyUO", "readUOPersonal", "advanceUOPersonal", "bumpUOPersonal", "uoPreferenceCurrent", "uoSettingsCurrent", "acceptUOConfigurationProjection"},
		bypasses:    []string{`func enqueueUOWebhook(ctx context.Context)(out string,err error){return "saved",nil}`},
	})
}
