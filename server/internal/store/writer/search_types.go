package writer

import "math"

// These are writer-private source effects, constructed only after the source
// command has allocated and read back a genuinely new retained subject/entity.
// They are not caller-provided file metadata or a separate publication command.
type newOrdinaryEntitySource struct {
	SubjectID, LibraryID, Kind int64
	MetadataIncarnation        [16]byte
	EntityRevision             int64
}

type searchInputChange struct {
	NewEntities  []newOrdinaryEntitySource
	AcceptedAtMS int64
}

// Initial InventoryAccept admits one ordinary movie. Increasing this is a
// recipe/qualification change, not permission to truncate an input cohort.
const (
	maxNewSearchInputEntities        = 1
	firstSearchInputGeneration int64 = 1
	firstSearchInputPhase            = "capture"
)

func validateNewSearchInput(change *searchInputChange) error {
	if change == nil || change.AcceptedAtMS < 0 {
		return invalid("search_input")
	}
	// Bound the caller-owned slice before inspecting its values.
	if err := count("search_input_entities", len(change.NewEntities), maxNewSearchInputEntities); err != nil {
		return err
	}
	for _, entity := range change.NewEntities {
		if entity.SubjectID <= 0 || entity.LibraryID <= 0 || entity.Kind != 1 || entity.EntityRevision != 1 {
			return invalid("new_movie_search_input")
		}
	}
	return nil
}

// The handler computes every expected->next native counter before binding SQL.
// Empty source cohorts do not call this: they must not advance any input clock.
func nextSearchInputCounter(expected, delta int64) (int64, error) {
	if expected < 0 || delta <= 0 || expected > math.MaxInt64-delta {
		return 0, invalid("search_input_counter")
	}
	return expected + delta, nil
}
