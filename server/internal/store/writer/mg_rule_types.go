package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/internal/mglimits"

const (
	MGSaveRule   CommandID = 824
	MGDeleteRule CommandID = 825
)

// Shared serialized-config validation ceiling; not a resource reservation.
// Consumers reserve actual bytes, as counted by variableBytes below.
const mgRuleMaxConfigBytes = mglimits.RuleConfigBytes

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
// events, admission, COMMIT and candidate EOF. Every effect error requires
// whole-frame rollback. No physical capture, allocation, guide pin or reservation
// is changed by logical rule disable. The accepting delete frame (MG-11), not
// this logical rule-row effect, owns the public future keep/cancel receipt and
// its same-transaction legacy intent detach.
