package writer

const (
	MGSaveRule   CommandID = 824
	MGDeleteRule CommandID = 825
)

func mgRuleDescriptors() []Descriptor {
	return []Descriptor{{MGSaveRule, "mg.rule.save", Normal}, {MGDeleteRule, "mg.rule.delete", Normal}}
}

// Config is bounded descriptive canonical input, never permission or matching
// authority. Parent validates canonical public config, anchor/new-evidence and
// adapter availability, resolves resurrection by the unique native identity,
// and supplies opaque current grants before invoking these private effects.
type mgRuleFacts struct {
	id, owner, account, profile, source, series, title, channel, config string
	revision, enabled, deleted, updated                                 int64
}
type mgRuleRequest struct {
	owner          mgRecordingOwner
	before         mgRuleFacts // zero for a new ID; exact native snapshot otherwise
	after          mgRuleFacts
	sourceRevision int64
	generation     string
}

// Parent owns public receipts/lifetimes, dependency and owner-revision effects,
// future keep/cancel decisions, events, admission, COMMIT and candidate EOF.
// Every effect error requires whole-frame rollback. No physical capture,
// allocation, guide pin or reservation is changed by logical rule disable.
