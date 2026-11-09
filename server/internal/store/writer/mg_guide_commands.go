package writer

// MG guide command purposes 800-803. A descriptor is metadata only: it neither
// authorizes nor qualifies execution. The accepting frame, originals and COMMIT
// belong to the owning task; these private effects run on a caller's turn.
const (
	MGGuideBegin           CommandID = 800
	MGGuideStageChannels   CommandID = 801
	MGGuideStageProgrammes CommandID = 802
	MGGuideStageChoices    CommandID = 803
)

func mgGuideDescriptors() []Descriptor {
	return []Descriptor{
		{MGGuideBegin, "mg.guide.begin", Normal},
		{MGGuideStageChannels, "mg.guide.channels", Normal},
		{MGGuideStageProgrammes, "mg.guide.programmes", Normal},
		{MGGuideStageChoices, "mg.guide.choices", Normal},
	}
}

// Purpose and durability bind a private effect to the closed command it serves.
// A policy-verified NORMAL turn is the only frame admitted here; a FULL or
// foreign command can never borrow guide staging mutations.
func mgGuidePurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose || t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified || !t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch purpose {
	case MGGuideBegin, MGGuideStageChannels, MGGuideStageProgrammes, MGGuideStageChoices:
		return nil
	}
	return ErrInvalid
}

func mgGuideStagePurpose(t *commandTurn, family mgGuideFamily) bool {
	switch family {
	case mgGuideChannels:
		return mgGuidePurpose(t, MGGuideStageChannels) == nil
	case mgGuideProgrammes:
		return mgGuidePurpose(t, MGGuideStageProgrammes) == nil
	case mgGuideChoices:
		return mgGuidePurpose(t, MGGuideStageChoices) == nil
	}
	return false
}
