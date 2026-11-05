package scan

// LocalWitness records a COMPLETE local NFO/art/subtitle observation. Observed
// distinguishes known absence from an unobserved directory. Revision is an
// opaque adapter witness, not an inferred mtime or a digest of an omitted set.
type LocalWitness struct {
	Observed bool
	Revision string
}

// BasicInput is the input vector of an already committed Basic publication or
// of the next proposed probe. PolicyRevision and ProbeRecipe cover settings and
// parser changes independently of the opaque media revision. Each library has
// its own inputs even when two roots refer to identical physical bytes.
type BasicInput struct {
	File                    FileWitness
	Available               bool
	PolicyRevision          int64
	ProbeRecipe             string
	NFO, Artwork, Subtitles LocalWitness
}

func (b BasicInput) reusable() bool {
	return b.Available && b.File.valid() && b.PolicyRevision > 0 && b.ProbeRecipe != "" &&
		b.NFO.Observed && b.Artwork.Observed && b.Subtitles.Observed
}

type BasicPlan uint8

const (
	ProbeRequired BasicPlan = iota
	ReuseBasic
	SourceUnavailable
)

// PlanBasic avoids another probe only for an exact, complete, already accepted
// input vector. A pending receipt is NOT accepted input. This is a scheduling
// hint: the private writer still rechecks source/lease/authority at acceptance.
// Unavailable entries remain retained and must not be probed or deleted here.
func PlanBasic(current BasicInput, accepted *BasicInput) BasicPlan {
	if current.File.valid() && !current.Available {
		return SourceUnavailable
	}
	if accepted != nil && current.reusable() && accepted.reusable() && current == *accepted {
		return ReuseBasic
	}
	return ProbeRequired
}
