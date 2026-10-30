package mediaanalysis

import (
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"testing"
)

func TestConditionalSourceEvidenceNeverInventsObservation(t *testing.T) {
	ref := playback.SourceReference{Kind: playback.StrongSourceReference, ID: "conditional-version"}
	good := playback.InputSourceEvidence{Reference: ref}
	if !compatibleSource(good, nil, good, nil) || !validChunkInterval(ref.Kind, nil, nil) {
		t.Fatal("valid conditional representation rejected")
	}
	bad := good
	bad.ObservationInterval = &playback.ObservationInterval{First: 1, Last: 2}
	if compatibleSource(bad, nil, good, nil) || validChunkInterval(ref.Kind, bad.ObservationInterval, nil) {
		t.Fatal("fabricated observed interval accepted")
	}
	bad = good
	bad.Reference.ID = "rotated"
	if compatibleSource(good, nil, bad, nil) {
		t.Fatal("changed representation accepted")
	}
}

func TestDescriptorDependencyCannotDisappearOrChangeDigest(t *testing.T) {
	ref := playback.SourceReference{Kind: playback.ObservedSourceReference, ID: "retained-descriptor"}
	old := playback.PreparedInputDependency{ID: "descriptor", Role: "access_configuration", Reference: playback.InputDependencyReference{Kind: ref.Kind, ID: ref.ID}, Digest: "digest", Provenance: &playback.InputSourceEvidence{Reference: ref, ObservationInterval: &playback.ObservationInterval{First: 1, Last: 1}}, Observation: &playback.ObservedInputSnapshot{AcquisitionID: ref.ID, Sequence: 1, Continuity: "intact", Observation: playback.SourceObservation{RootBindingID: "root", ObjectBindingID: "object"}}}
	rows := []playback.PreparedInputDependency{old}
	if !compatibleDependencies(rows, rows, rows) {
		t.Fatal("retained dependency rejected")
	}
	if compatibleDependencies(rows, rows, nil) {
		t.Fatal("lost descriptor accepted")
	}
	changed := old
	changed.Digest = "changed"
	if compatibleDependencies(rows, rows, []playback.PreparedInputDependency{changed}) {
		t.Fatal("changed descriptor accepted")
	}
	if compatibleDependencies(rows, rows, append(rows, old)) {
		t.Fatal("duplicate descriptor accepted")
	}
}
