package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_edit_bulk_apply",
		resultTypes: []string{
			"ipEditBulkPageCommandResult", "ipEditBulkPageCommand", "ipEditBulkPageLookup",
			"ipEditBulkPageApplied", "ipEditBulkPageResult", "ipEditBulkPageSavedResult",
			"ipEditBulkTargetOutcome", "ipEditBulkFinishCommandResult", "ipEditBulkFinishCommand",
			"ipEditBulkFinishLookup", "ipEditBulkFinishApplied", "ipEditBulkFinishResult",
			"ipEditBulkFinishSavedResult",
		},
		names: []string{"ipEditBulkFinishTargets", "ipEditBulkFinishNoNext"},
		bypasses: []string{
			`func ipEditBulkPageReadSaved(ctx context.Context)(out ipEditBulkPageApplied,err error){return out,nil}`,
			`func ipEditBulkPageLookupReceipt(ctx context.Context)(out ipEditBulkPageLookup,err error){return out,nil}`,
			`func ipEditBulkPageTarget(ctx context.Context)(out ipEditBulkTargetOutcome,err error){return out,nil}`,
			`func applyIPEditBulkPage(ctx context.Context)(out ipEditBulkPageCommandResult,err error){return out,nil}`,
			`func ipEditBulkFinishReadSaved(ctx context.Context)(out ipEditBulkFinishApplied,err error){return out,nil}`,
			`func ipEditBulkFinishLookupReceipt(ctx context.Context)(out ipEditBulkFinishLookup,err error){return out,nil}`,
			`func applyIPEditBulkFinish(ctx context.Context)(out ipEditBulkFinishCommandResult,err error){return out,nil}`,
			`func ipEditBulkFinishTargets(ctx context.Context)(err error){return nil}`,
			`func ipEditBulkFinishNoNext(ctx context.Context)(err error){return nil}`,
		},
	})
}
