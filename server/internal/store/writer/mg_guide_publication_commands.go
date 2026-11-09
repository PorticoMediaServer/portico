package writer

// MG guide seal and publication purposes 804-805. A descriptor is metadata only:
// it neither authorizes nor qualifies execution. The accepting frame, original
// saved result, event and COMMIT belong to the parent command frame; these
// private effects run on the caller's admitted turn.
const (
	MGGuideSeal    CommandID = 804
	MGGuidePublish CommandID = 805
)

func mgGuidePublicationDescriptors() []Descriptor {
	return []Descriptor{
		{MGGuideSeal, "mg.guide.seal", Normal},
		{MGGuidePublish, "mg.guide.publish", Normal},
	}
}
