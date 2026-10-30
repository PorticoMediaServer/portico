package mediaanalysis

import (
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"reflect"
)

func compatibleSource(old playback.InputSourceEvidence, before *playback.ObservedInputSnapshot, now playback.InputSourceEvidence, after *playback.ObservedInputSnapshot) bool {
	if old.Reference != now.Reference || old.Reference.ID == "" {
		return false
	}
	if old.Reference.Kind == playback.StrongSourceReference {
		return before == nil && after == nil && old.ObservationInterval == nil && now.ObservationInterval == nil
	}
	i := old.ObservationInterval
	return old.Reference.Kind == playback.ObservedSourceReference && before != nil && after != nil && i != nil && before.AcquisitionID == old.Reference.ID && after.AcquisitionID == old.Reference.ID && before.Sequence > 0 && after.Sequence >= before.Sequence && i.First > 0 && i.Last == before.Sequence && i.First <= i.Last && before.Continuity == "intact" && after.Continuity == "intact" && reflect.DeepEqual(before.Observation, after.Observation)
}
func validChunkInterval(kind string, chunk, final *playback.ObservationInterval) bool {
	if kind == playback.StrongSourceReference {
		return chunk == nil && final == nil
	}
	return kind == playback.ObservedSourceReference && chunk != nil && final != nil && chunk.First >= final.First && chunk.First <= chunk.Last && chunk.Last <= final.Last
}
func compatibleDependencies(initial, final, current []playback.PreparedInputDependency) bool {
	if len(final) > 128 || len(initial) != len(final) || len(current) != len(final) {
		return false
	}
	index := func(rows []playback.PreparedInputDependency) map[string]playback.PreparedInputDependency {
		result := map[string]playback.PreparedInputDependency{}
		for _, d := range rows {
			if d.ID == "" || d.Provenance == nil || d.Provenance.Reference.Kind != d.Reference.Kind || d.Provenance.Reference.ID != d.Reference.ID {
				return nil
			}
			if _, ok := result[d.ID]; ok {
				return nil
			}
			result[d.ID] = d
		}
		return result
	}
	a, b, c := index(initial), index(final), index(current)
	if a == nil || b == nil || c == nil {
		return false
	}
	for id, old := range b {
		before, ok := a[id]
		if !ok {
			return false
		}
		now, ok := c[id]
		if !ok {
			return false
		}
		if old.Role != before.Role || old.Role != now.Role || old.Digest != before.Digest || old.Digest != now.Digest || old.Reference != before.Reference || old.Reference != now.Reference {
			return false
		}
		if !compatibleSource(*before.Provenance, before.Observation, *old.Provenance, old.Observation) || !compatibleSource(*old.Provenance, old.Observation, *now.Provenance, now.Observation) {
			return false
		}
		if old.Reference.Kind == playback.StrongSourceReference {
			if old.Version.ID() != old.Reference.ID || before.Version.ID() != old.Reference.ID || now.Version.ID() != old.Reference.ID {
				return false
			}
		} else if old.Version.ID() != "" || before.Version.ID() != "" || now.Version.ID() != "" {
			return false
		}
	}
	return true
}
