package writer

const (
	MGAcquireAllocation CommandID = 814
	MGRenewAllocation   CommandID = 815
)

func mgTunerDescriptors() []Descriptor {
	return []Descriptor{{MGAcquireAllocation, "mg.tuner.acquire", Normal}, {MGRenewAllocation, "mg.tuner.renew", Normal}}
}

const (
	// The allocation lease is the existing 20s public holding window. A shorter
	// parent-selected protected boundary may only narrow it, never extend it.
	mgTunerLeaseMS int64 = 20000
	// Owner configuration (MGConfigureTuner) already caps the published effective
	// tuner count at 256. This body refuses anything wider.
	mgTunerMaxCount int64 = 256
)

// A frozen tuple is owned data, never current authority or a physical
// capability. The parent owns current actor/source custody, the reservation
// selection, preemption choice, admission, the original receipt, events/debt and
// COMMIT truth. This component never opens a connection, provider or device, and
// it never releases another allocation: expire/withdraw/reconcile are other
// closed tasks.
type mgTunerAllocation struct {
	id, source, channel, resource, kind, authority, account, profile, token string
	generation                                                              int64
	state                                                                   string
	lease, created, preempt                                                 int64
}

// guide is the current immutable generation the parent selected. protectedUntil
// is the parent-selected absolute boundary: for a live allocation it is the
// enabled recording-reservation start that the owner's live/recording conflict
// policy protects (0 means the policy grants none), and it is rechecked against
// the native reservation window below. Recording preemption boundaries are
// parent retirement custody, carried in preempt, never a reservation grant.
type mgTunerRequest struct {
	allocation     mgTunerAllocation
	guide          string
	protectedUntil int64
}

// held is a saturated bounded prefix: it stops counting at the effective
// capacity, so held==effective means "at least effective", never a total.
// effective 0 is the established unknown/unlimited value and leaves held
// unprobed because there is no configured hard capacity to enforce.
type mgTunerCapacity struct{ effective, held int64 }

func (a mgTunerAllocation) valid() bool {
	for _, v := range []string{a.id, a.source, a.channel, a.resource, a.account, a.profile, a.token} {
		if !mgSourceText(v, true, 256) {
			return false
		}
	}
	return a.authority == "local" && (a.kind == "live" || a.kind == "recording") && a.generation > 0 && (a.state == "active" || a.state == "preempting" || a.state == "released") && a.created >= 0 && a.lease > 0 && a.preempt >= 0
}

// Public shape rules, not authority. Live viewing is only ever given a deadline
// that a protected reservation actually establishes, so preempt and the
// reservation boundary must be the same value; recordings carry the parent's
// retirement boundary alone.
func freezeMGTunerRequest(r mgTunerRequest) (mgTunerRequest, error) {
	if !r.allocation.valid() || !mgSourceText(r.guide, true, 256) || r.protectedUntil < 0 || r.protectedUntil > 0 && r.protectedUntil < r.allocation.lease {
		return mgTunerRequest{}, ErrInvalid
	}
	switch r.allocation.kind {
	case "live":
		if r.allocation.preempt != r.protectedUntil {
			return mgTunerRequest{}, ErrInvalid
		}
	case "recording":
		if r.protectedUntil != 0 {
			return mgTunerRequest{}, ErrInvalid
		}
	}
	return r, nil
}

func (a mgTunerAllocation) values() []any {
	return []any{a.id, a.source, a.channel, a.resource, a.kind, a.authority, a.account, a.profile, a.token, a.generation, a.state, a.lease, a.created, a.preempt}
}
