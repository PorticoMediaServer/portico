package playback

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"testing"
)

func pinnedFixture(t *testing.T) (SourcePreparationInput, *strongInputSource) {
	t.Helper()
	in := workerInput()
	v, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag, Scope: "provider", Object: "object", Revision: "\"v1\"", Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancel(context.Background())
	s := &strongInputSource{life: life, cancel: cancel, path: in.Selection.Path, metadata: strongInputMetadata{Fence: in.Fence, AssetID: in.Selection.AssetID, ItemID: in.Selection.ItemID, LibraryID: in.Selection.LibraryID, RootID: in.Selection.RootID, Version: v, Access: "finite_random", FactsStatus: "unknown", Size: 10, InventorySize: 10}}
	t.Cleanup(func() { s.Close() })
	return in, s
}
func TestPreparedInputStrongConstructionAndFailure(t *testing.T) {
	in, s := pinnedFixture(t)
	p, err := newStrongPreparedInput(in, s)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := p.StrongVersion()
	m := p.Metadata()
	if !ok || v.ID() != s.metadata.Version.ID() || m.Reference.Kind != StrongSourceReference || m.InitialEvidence.ObservationInterval != nil || m.InitialObservation != nil {
		t.Fatal("strong identity altered")
	}
	rejectedInput, rejected := pinnedFixture(t)
	rejectedInput.Selection.ExpectedSourceVersionID = "other"
	if result, err := newStrongPreparedInput(rejectedInput, rejected); result != nil || !errors.Is(err, mediasource.ErrSourceChanged) || rejected.life.Err() == nil {
		t.Fatal("strict mismatch accepted or acquired backend leaked")
	}
	preparer := NewStrongInputPreparer(nil, nil)
	if result, err := preparer.PrepareInput(context.Background(), workerInput()); result != nil || !errors.Is(err, mediasource.ErrIdentityRequired) {
		t.Fatal("unconfigured strong source fell back or accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := preparer.PrepareInput(canceled, workerInput()); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled acquisition accepted")
	}
}
func TestPreparedInputDependencyDiscriminators(t *testing.T) {
	for _, kind := range []string{"version on observed", "interval on strong", "wrong acquisition", "sequence mismatch", "lost", "wrong provenance", "duplicate", "unknown role", "invalid digest", "unowned artifact"} {
		t.Run(kind, func(t *testing.T) {
			d := observedDependency()
			_, s := pinnedFixture(t)
			switch kind {
			case "version on observed":
				d.Version = s.metadata.Version
			case "interval on strong":
				d.Reference = InputDependencyReference{StrongSourceReference, s.metadata.Version.ID()}
				d.Version = s.metadata.Version
				d.Provenance.Reference = SourceReference{d.Reference.Kind, d.Reference.ID}
				d.Observation = nil
			case "wrong acquisition":
				d.Observation.AcquisitionID = "other"
			case "sequence mismatch":
				d.Observation.Sequence = 1
			case "lost":
				d.Observation.Continuity = "lost"
			case "wrong provenance":
				d.Provenance.Reference.ID = "other"
			case "unknown role":
				d.Role = "invented"
			case "invalid digest":
				d.Digest = "bad"
			case "unowned artifact":
				d.Reference.Kind = SealedArtifactReference
			}
			deps := []PreparedInputDependency{d}
			if kind == "duplicate" {
				deps = append(deps, d)
			}
			if err := validateInputDependencies(deps); !errors.Is(err, ErrPreparedInput) {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	// Zero timestamps and negative pre-epoch timestamps are known values; absence
	// is distinct and may not smuggle a nonzero observation.
	o := memoryDriver().observation
	o.ModifiedNS = KnownInt64{true, -1}
	if !validSourceObservation(o) {
		t.Fatal("valid pre-epoch timestamp rejected")
	}
	o.ModifiedNS.Known = false
	if validSourceObservation(o) {
		t.Fatal("unknown field retained hidden value")
	}
}
