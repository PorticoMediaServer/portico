package writer

const (
	MGAcquireAllocation CommandID = 814
	MGRenewAllocation   CommandID = 815
)

func mgTunerDescriptors() []Descriptor {
	return []Descriptor{{MGAcquireAllocation, "mg.tuner.acquire", Normal}, {MGRenewAllocation, "mg.tuner.renew", Normal}}
}

// Frozen tuples are owned data, never current authority or physical capabilities.
// Parent owns current actor/source/rating/follower custody, reservations,
// preemption selection, admission, original receipts, debt/events and COMMIT.
// Retire 816 and reconcile 817 remain held. Every later failure rolls back the
// whole parent turn; expiry/withdrawal never retires physical debt here.
type mgTunerAllocation struct {
	id, source, channel, resource, kind, authority, account, profile, token string
	generation                                                              int64
	state                                                                   string
	lease, created, preempt                                                 int64
}
type mgTunerRequest struct {
	allocation mgTunerAllocation
	guide      string
	// Absolute parent-selected protected boundary, data rechecked at this frame's clock.
	protectedUntil int64
}

// held is a saturated prefix/lower bound, not a total. An effective0 leaves
// it unprobed because there is no configured hard capacity to enforce.
type mgTunerCapacity struct{ effective, held int64 }

func (a mgTunerAllocation) valid() bool {
	for _, v := range []string{a.id, a.source, a.channel, a.resource, a.account, a.profile, a.token} {
		if !mgSourceText(v, true, 256) {
			return false
		}
	}
	return a.authority == "local" && (a.kind == "live" || a.kind == "recording") && a.generation > 0 && (a.state == "active" || a.state == "preempting" || a.state == "released") && a.created >= 0 && a.lease > 0 && a.preempt >= 0
}
func freezeMGTunerRequest(r mgTunerRequest) (mgTunerRequest, error) {
	if !r.allocation.valid() || !mgSourceText(r.guide, true, 256) || r.protectedUntil < 0 {
		return mgTunerRequest{}, ErrInvalid
	}
	return r, nil
}
func (a mgTunerAllocation) values() []any {
	return []any{a.id, a.source, a.channel, a.resource, a.kind, a.authority, a.account, a.profile, a.token, a.generation, a.state, a.lease, a.created, a.preempt}
}
