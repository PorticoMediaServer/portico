package writer

// MG-12 owns two NORMAL accepting descriptors: bounded rule-work custody (826)
// and bounded scheduled-intent reconciliation for a retired rule (827). Neither
// is an acquisition, capture, allocation or physical-release command.
func mgReconcileDescriptors() []Descriptor {
	return []Descriptor{
		{MGRuleWorkReconcile, "mg.rule-work.reconcile", Normal},
		{MGScheduledReconcile, "mg.scheduled.reconcile", Normal},
	}
}
