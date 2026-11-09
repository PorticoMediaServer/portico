package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "mg_reconcile",
		resultTypes: []string{"mgReconcileAcceptance", "mgReconcileWorkFacts", "mgScheduledReconcileFacts"},
		bypasses: []string{
			`func acceptMGReconcileRule(ctx context.Context)(out mgReconcileAcceptance,err error){return out,nil}`,
			`func acceptMGScheduledReconcile(ctx context.Context)(out mgReconcileAcceptance,err error){return out,nil}`,
			`func mgReconcileRuleWork(ctx context.Context)(out mgReconcileWorkFacts,err error){return out,nil}`,
			`func mgScheduledReconcile(ctx context.Context)(out mgScheduledReconcileFacts,err error){return out,nil}`,
		},
	})
}
