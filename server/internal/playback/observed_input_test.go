package playback

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

// This driver uses only ten in-memory bytes. It cannot reopen a file, resolve a
// locator, publish bytes or establish any filesystem/provider guarantees.
type observedMemoryDriver struct {
	dependencyError error
	observation     SourceObservation
	deps            []PreparedInputDependency
	data            []byte
	observe         func(context.Context) (SourceObservation, error)
	read            func(context.Context, int64, int64) ([]byte, error)
	closes, reads   int
}

func memoryDriver() *observedMemoryDriver {
	return &observedMemoryDriver{observation: SourceObservation{RootBindingID: "actual-root", ObjectBindingID: "actual-object", Size: KnownInt64{true, 10}, ModifiedNS: KnownInt64{true, 0}}, data: []byte("0123456789")}
}

func workerInput() SourcePreparationInput {
	return SourcePreparationInput{Fence: SourcePreparationFence{PlaybackID: "playback", PresentationID: "presentation", OwnershipRevision: 1, DesiredRevision: 2, Generation: 3, PolicyRevision: 4}, Selection: SourcePreparationSelection{Kind: "local_file", ItemID: "item", AssetID: "asset", LibraryID: "library", RootID: "root", Path: "protected-selected-path", InventorySize: 10}}
}
func (d *observedMemoryDriver) Observe(ctx context.Context) (SourceObservation, error) {
	if d.observe != nil {
		return d.observe(ctx)
	}
	return d.observation, ctx.Err()
}
func (d *observedMemoryDriver) Dependencies(ctx context.Context) ([]PreparedInputDependency, error) {
	if d.dependencyError != nil {
		return nil, d.dependencyError
	}
	return d.deps, ctx.Err()
}
func (d *observedMemoryDriver) ReadExtent(ctx context.Context, o, n int64) ([]byte, error) {
	d.reads++
	if d.read != nil {
		return d.read(ctx, o, n)
	}
	if o+n > int64(len(d.data)) {
		return nil, io.ErrUnexpectedEOF
	}
	return d.data[o : o+n], ctx.Err()
}
func (d *observedMemoryDriver) Close() error { d.closes++; return nil }
func observedFixture(t *testing.T, d *observedMemoryDriver) *PreparedInput {
	t.Helper()
	p, err := newObservedInput(context.Background(), workerInput(), "finite_random", d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}
func observedDependency() PreparedInputDependency {
	ref := SourceReference{ObservedSourceReference, "config-acquisition"}
	return PreparedInputDependency{ID: "config", Role: "access_configuration", Reference: InputDependencyReference{ref.Kind, ref.ID}, Provenance: &InputSourceEvidence{Reference: ref, ObservationInterval: &ObservationInterval{1, 2}}, Observation: &ObservedInputSnapshot{AcquisitionID: ref.ID, Sequence: 2, Continuity: "intact", Observation: memoryDriver().observation}}
}
func TestObservedInputFreshIdentityAndCopies(t *testing.T) {
	d := memoryDriver()
	d.deps = []PreparedInputDependency{observedDependency()}
	p := observedFixture(t, d)
	q := observedFixture(t, memoryDriver())
	m := p.Metadata()
	if m.Reference.Kind != ObservedSourceReference || m.Reference.ID == q.Metadata().Reference.ID || m.Reference.ID == "" || m.FactsStatus != "unknown" {
		t.Fatal("acquisition identity/facts invalid")
	}
	if v, ok := p.StrongVersion(); ok || v.ID() != "" {
		t.Fatal("observed input forged strong version")
	}
	if m.InitialObservation.Sequence != 1 || *m.InitialEvidence.ObservationInterval != (ObservationInterval{1, 1}) || !m.Length.Known || m.Length.Value != 10 {
		t.Fatal("initial evidence invalid")
	}
	m.InitialObservation.Observation.ObjectBindingID = "mutated"
	m.InitialEvidence.ObservationInterval.Last = 99
	m.Dependencies[0].Observation.Sequence = 99
	m.Dependencies[0].Provenance.ObservationInterval.Last = 99
	d.deps[0].Observation.Sequence = 3
	d.deps[0].Provenance.ObservationInterval.Last = 3
	fresh := p.Metadata()
	if fresh.InitialObservation.Observation.ObjectBindingID != "actual-object" || fresh.InitialEvidence.ObservationInterval.Last != 1 || fresh.Dependencies[0].Observation.Sequence != 2 || fresh.Dependencies[0].Provenance.ObservationInterval.Last != 2 {
		t.Fatal("metadata aliases callers or adapter")
	}
	extent, err := p.ReadExtent(context.Background(), 2, 4)
	if err != nil || string(extent.Bytes) != "2345" || *extent.Evidence.ObservationInterval != (ObservationInterval{2, 3}) || extent.Evidence.Reference != fresh.Reference {
		t.Fatalf("extent interval/bytes %v %v", extent, err)
	}
	extent.Bytes[0] = 'x'
	if d.data[2] != '2' {
		t.Fatal("extent aliases adapter buffer")
	}
	validation, err := p.Validate(context.Background())
	if err != nil || validation.Observation.Sequence != 4 || validation.Evidence.Reference != fresh.Reference {
		t.Fatal("acquisition identity or monotonic sequence lost")
	}
	validation.Dependencies[0].Observation.Sequence = 888
	if p.Metadata().Dependencies[0].Observation.Sequence != 2 {
		t.Fatal("initial observation mutated by current validation")
	}
}
func TestObservedInputStrictPinsAndInvalidEvidence(t *testing.T) {
	for _, kind := range []string{"media pin", "descriptor pin", "bad binding", "bad presence", "unowned artifact"} {
		t.Run(kind, func(t *testing.T) {
			d := memoryDriver()
			in := workerInput()
			switch kind {
			case "media pin":
				in.Selection.ExpectedSourceVersionID = "strict"
			case "descriptor pin":
				in.Selection.ExpectedDescriptorVersionID = "strict"
				d.deps = []PreparedInputDependency{observedDependency()}
			case "bad binding":
				d.observation.RootBindingID = ""
			case "bad presence":
				d.observation.Size = KnownInt64{false, 10}
			case "unowned artifact":
				dep := observedDependency()
				dep.Reference.Kind = SealedArtifactReference
				d.deps = []PreparedInputDependency{dep}
			}
			p, err := newObservedInput(context.Background(), in, "finite_random", d)
			if p != nil || err == nil || d.closes != 1 {
				t.Fatal("invalid input accepted or leaked")
			}
			if kind == "media pin" || kind == "descriptor pin" {
				if !errors.Is(err, mediasource.ErrSourceChanged) {
					t.Fatal("strict pin error misclassified")
				}
			}
		})
	}
}
func TestObservedInputDetectedChangeFencesBytesAndStaysLost(t *testing.T) {
	for _, kind := range []string{"size", "object", "root", "time", "token", "reconnect", "dependency"} {
		t.Run(kind, func(t *testing.T) {
			d := memoryDriver()
			d.observation.ChangeToken = "r1"
			d.deps = []PreparedInputDependency{observedDependency()}
			p := observedFixture(t, d)
			d.read = func(context.Context, int64, int64) ([]byte, error) {
				switch kind {
				case "size":
					d.observation.Size.Value = 9
				case "object":
					d.observation.ObjectBindingID = "replacement"
				case "root":
					d.observation.RootBindingID = "replacement"
				case "time":
					d.observation.ModifiedNS.Value = 1
				case "token":
					d.observation.ChangeToken = "r2"
				case "dependency":
					d.deps[0].Observation.Observation.ObjectBindingID = "different config"
				case "reconnect":
					return nil, ErrObservedContinuityLost
				}
				return []byte("01"), nil
			}
			extent, err := p.ReadExtent(context.Background(), 0, 2)
			if !errors.Is(err, ErrObservedContinuityLost) || len(extent.Bytes) != 0 {
				t.Fatalf("changed bytes escaped: %v %v", extent, err)
			}
			d.observation = memoryDriver().observation
			d.deps = nil
			if _, err = p.Validate(context.Background()); !errors.Is(err, ErrObservedContinuityLost) {
				t.Fatal("lost continuity revived")
			}
			if _, err = p.ReadExtent(context.Background(), 0, 2); !errors.Is(err, ErrObservedContinuityLost) || d.reads != 1 {
				t.Fatal("lost acquisition reused")
			}
		})
	}
}
func TestObservedInputUnknownDoesNotEraseKnownEvidence(t *testing.T) {
	for _, dependency := range []bool{false, true} {
		d := memoryDriver()
		d.deps = []PreparedInputDependency{observedDependency()}
		p := observedFixture(t, d)
		if dependency {
			d.deps[0].Observation.Observation.ModifiedNS = KnownInt64{}
		} else {
			d.observation.ModifiedNS = KnownInt64{}
		}
		if _, err := p.Validate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if dependency {
			d.deps[0].Observation.Observation.ModifiedNS = KnownInt64{true, 1}
		} else {
			d.observation.ModifiedNS = KnownInt64{true, 1}
		}
		if _, err := p.Validate(context.Background()); !errors.Is(err, ErrObservedContinuityLost) {
			t.Fatal("unknown observation erased previous known revision")
		}
	}
}
func TestObservedInputUnavailableAndReadBounds(t *testing.T) {
	d := memoryDriver()
	p := observedFixture(t, d)
	unavailable := errors.New("temporarily unavailable")
	d.observe = func(context.Context) (SourceObservation, error) { return SourceObservation{}, unavailable }
	if _, err := p.Validate(context.Background()); !errors.Is(err, unavailable) {
		t.Fatal(err)
	}
	d.observe = nil
	if _, err := p.Validate(context.Background()); err != nil {
		t.Fatal("ordinary unavailability falsely ended continuity")
	}
	for _, bounds := range [][2]int64{{-1, 1}, {0, 0}, {0, maxObservedExtent + 1}, {10, 1}} {
		if out, err := p.ReadExtent(context.Background(), bounds[0], bounds[1]); err == nil || out.Bytes != nil {
			t.Fatal("invalid extent accepted")
		}
	}
	if d.reads != 0 {
		t.Fatal("bounds initiated source read")
	}
	d.read = func(context.Context, int64, int64) ([]byte, error) { return []byte("0"), nil }
	if _, err := p.ReadExtent(context.Background(), 0, 2); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("short read accepted")
	}
}
func TestObservedInputCloseCancelsActiveAndQueuedWork(t *testing.T) {
	d := memoryDriver()
	p := observedFixture(t, d)
	entered := make(chan struct{})
	done := make(chan error, 1)
	queued := make(chan error, 1)
	d.read = func(ctx context.Context, _ int64, _ int64) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() { _, err := p.ReadExtent(context.Background(), 0, 1); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("read never entered")
	}
	go func() { _, err := p.Validate(context.Background()); queued <- err }()
	p.Close()
	p.Close()
	for _, ch := range []chan error{done, queued} {
		select {
		case err := <-ch:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("close failed to cancel acquisition work")
		}
	}
	if d.closes != 1 {
		t.Fatal("driver not closed exactly once")
	}
}

func TestObservedInputObservationSurvivesDependencyFailure(t *testing.T) {
	for _, initiallyKnown := range []bool{true, false} {
		d := memoryDriver()
		if !initiallyKnown {
			d.observation.ModifiedNS = KnownInt64{}
		}
		p := observedFixture(t, d)
		unavailable := errors.New("configuration temporarily unavailable")
		d.observation.ModifiedNS = KnownInt64{true, 1}
		d.dependencyError = unavailable
		_, err := p.Validate(context.Background())
		if initiallyKnown {
			if !errors.Is(err, ErrObservedContinuityLost) {
				t.Fatal("known conflict discarded behind dependency error")
			}
		} else if !errors.Is(err, unavailable) {
			t.Fatal("new knowledge should survive incomplete validation", err)
		}
		d.dependencyError = nil
		d.observation.ModifiedNS = KnownInt64{true, 0}
		if _, err := p.Validate(context.Background()); !errors.Is(err, ErrObservedContinuityLost) {
			t.Fatal("restored metadata revived acquisition or erased learned evidence")
		}
		if out, err := p.ReadExtent(context.Background(), 0, 1); !errors.Is(err, ErrObservedContinuityLost) || len(out.Bytes) != 0 || d.reads != 0 {
			t.Fatal("lost acquisition yielded bytes")
		}
	}
}
