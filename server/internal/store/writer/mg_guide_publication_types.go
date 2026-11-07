package writer

const (
	MGGuideBegin           CommandID = 800
	MGGuideStageChannels   CommandID = 801
	MGGuideStageProgrammes CommandID = 802
	MGGuideStageChoices    CommandID = 803
	MGGuideSeal            CommandID = 804
	MGGuidePublish         CommandID = 805
	MGGuideDiscard         CommandID = 806
	MGGuideReclaim         CommandID = 807
)

func mgGuideDescriptors() []Descriptor {
	return []Descriptor{
		{MGGuideBegin, "mg.guide.begin", Normal}, {MGGuideStageChannels, "mg.guide.channels", Normal}, {MGGuideStageProgrammes, "mg.guide.programmes", Normal}, {MGGuideStageChoices, "mg.guide.choices", Normal},
		{MGGuideSeal, "mg.guide.seal", Normal}, {MGGuidePublish, "mg.guide.publish", Normal}, {MGGuideDiscard, "mg.guide.discard", Normal}, {MGGuideReclaim, "mg.guide.reclaim", Normal},
	}
}

// Source facts on the same admitted private writer turn, never disclosure,
// receipt resolution, a physical pin or final COMMIT/Applied evidence. MG-001's
// adopted SQL carrier and complete driver/receipt profiles are still pending.
type mgGuideNative struct {
	request, input, name, createdAt, status string
	tuner                                   int64
	progress                                mgGuideProgress
	incarnation, createOperation            [16]byte
	birthDigest                             [32]byte
	acknowledged                            int64
	resultRevision                          int64
	resultState, publishedAt                string
}
type mgGuideNativeResult struct {
	operation mgGuideNative
	head      mgGuideHead
	appliedMS int64
}
