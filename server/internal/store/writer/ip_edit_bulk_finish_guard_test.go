package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name: "ip_edit_bulk",
		resultTypes: []string{
			"ipEditBulkCommandResult", "ipEditBulkCommand", "ipEditBulkLookup",
			"ipEditBulkApplied", "ipEditBulkResult",
		},
		bypasses: []string{
			`func applyIPEditBulkBegin(ctx context.Context)(out ipEditBulkCommandResult,err error){return out,nil}`,
			`func ipEditBulkReadSaved(ctx context.Context)(out ipEditBulkApplied,err error){return out,nil}`,
			`func ipEditBulkCheckEntity(ctx context.Context)(err error){return nil}`,
		},
	})
}
