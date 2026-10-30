package telemetry

import "sync/atomic"

// SummaryCacheEvent is a fixed, non-identifying process counter in the existing
// telemetry package. It adds no wire fields or route. Verification harnesses can
// sample these counters alongside database cost; policies/library/user IDs are
// never metric labels. The vocabulary is bounded independently of traffic.
type SummaryCacheEvent string

const (
	SummaryHitEntry          SummaryCacheEvent = "hit:entry"
	SummaryHitFlight         SummaryCacheEvent = "hit:flight"
	SummaryMissCold          SummaryCacheEvent = "miss:cold"
	SummaryBypassSnapshot    SummaryCacheEvent = "bypass:no_snapshot"
	SummaryBypassIdentity    SummaryCacheEvent = "bypass:unproved_identity"
	SummaryBypassRetaining   SummaryCacheEvent = "bypass:retaining"
	SummaryBypassDirty       SummaryCacheEvent = "bypass:restricted_dirty"
	SummaryBypassFailure     SummaryCacheEvent = "bypass:restricted_failure"
	SummaryBypassRebuild     SummaryCacheEvent = "bypass:restricted_rebuild"
	SummaryBypassCleanup     SummaryCacheEvent = "bypass:restricted_cleanup"
	SummaryBypassFirstBuild  SummaryCacheEvent = "bypass:restricted_first_build"
	SummaryBypassReading     SummaryCacheEvent = "bypass:reading_unready"
	SummaryBypassKeySize     SummaryCacheEvent = "bypass:key_size"
	SummaryBypassValueSize   SummaryCacheEvent = "bypass:value_size"
	SummaryBypassFlightLimit SummaryCacheEvent = "bypass:flight_limit"
)

const SummaryBypassRestrictedMembership SummaryCacheEvent = "bypass:restricted_membership_unproved"

const SummaryBypassCancelled SummaryCacheEvent = "bypass:cancelled"

var summaryEvents = [...]SummaryCacheEvent{SummaryBypassCancelled, SummaryHitEntry, SummaryHitFlight, SummaryMissCold, SummaryBypassSnapshot, SummaryBypassIdentity, SummaryBypassRetaining, SummaryBypassDirty, SummaryBypassRestrictedMembership, SummaryBypassFailure, SummaryBypassRebuild, SummaryBypassCleanup, SummaryBypassFirstBuild, SummaryBypassReading, SummaryBypassKeySize, SummaryBypassValueSize, SummaryBypassFlightLimit}
var summaryCounters [len(summaryEvents)]atomic.Uint64

func CountLibrarySummaryCache(event SummaryCacheEvent) {
	for i, e := range summaryEvents {
		if e == event {
			summaryCounters[i].Add(1)
			return
		}
	}
}

// LibrarySummaryCacheCounters returns an independent fixed-size observation.
func LibrarySummaryCacheCounters() map[SummaryCacheEvent]uint64 {
	out := make(map[SummaryCacheEvent]uint64, len(summaryEvents))
	for i, e := range summaryEvents {
		out[e] = summaryCounters[i].Load()
	}
	return out
}
