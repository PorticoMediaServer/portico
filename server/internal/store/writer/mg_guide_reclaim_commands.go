package writer

// MG guide retirement purposes 806, 807 and 852. A descriptor is metadata only:
// it neither authorizes nor qualifies execution. MG-26 owns the receipt/ACK and
// retirement-floor adapter; these private effects run on a caller's NORMAL turn.
const (
	MGGuideDiscard        CommandID = 806
	MGGuideReclaim        CommandID = 807
	MGGuideAdvanceCleanup CommandID = 852
)

func mgGuideReclaimDescriptors() []Descriptor {
	return []Descriptor{
		{MGGuideDiscard, "mg.guide.discard", Normal},
		{MGGuideReclaim, "mg.guide.reclaim", Normal},
		{MGGuideAdvanceCleanup, "mg.guide.cleanup", Normal},
	}
}

// Purpose and durability bind a private retirement effect to the closed command
// it serves. A policy-verified NORMAL turn is the only frame admitted here; a
// FULL, foreign or closed command can never borrow guide retirement mutations.
func mgGuideReclaimPurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch purpose {
	case MGGuideDiscard, MGGuideReclaim, MGGuideAdvanceCleanup:
		return nil
	}
	return ErrInvalid
}
