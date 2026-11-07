package writer

const (
	MGSaveRule   CommandID = 824
	MGDeleteRule CommandID = 825
)

// Today's DVR decoder accepts 384 KiB. Canonical RuleConfig permits 2,000
// allowed plus 2,000 blocked 64-byte IDs and forty bounded keywords, including
// JSON escaping. The transport bound covers that accepted range, not a new
// 64 KiB restriction on the stored configuration.
const mgRuleMaxConfigBytes = 384 << 10

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

// Exact variable TEXT bytes for validated facts, for the parent's input/result
// admission. SQLite/container overhead and receipt bytes are separate charges.
// This counts the actual complete configuration, never columns times maxima.
func (v mgRuleFacts) variableBytes() int64 {
	var n int64
	for _, s := range []string{v.id, v.owner, v.account, v.profile, v.source, v.series, v.title, v.channel, v.config} {
		n += int64(len(s))
	}
	return n
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
