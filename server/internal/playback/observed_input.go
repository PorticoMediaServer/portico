package playback

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"sync"
)

var ErrObservedContinuityLost = errors.New("observed source continuity lost")

const maxObservedExtent = 1 << 20 // bounded operation, not a source length limit

// This private driver retains one actual acquisition. It must never reopen a
// mutable locator or claim a reconnect is continuous merely from matching stats.
// No implementation backed by real filesystem/provider IO is added in this slice.
type observedInputDriver interface {
	Observe(context.Context) (SourceObservation, error)
	Dependencies(context.Context) ([]PreparedInputDependency, error)
	ReadExtent(context.Context, int64, int64) ([]byte, error)
	Close() error
}
type observedAcquisition struct {
	driver              observedInputDriver
	life                context.Context
	cancel              context.CancelFunc
	gate                chan struct{}
	mu                  sync.Mutex
	snapshot            ObservedInputSnapshot
	baseline            SourceObservation
	dependencyBaselines map[string]SourceObservation
	deps                []PreparedInputDependency
	terminal            error
	closeOnce           sync.Once
	closeErr            error
}
type ObservedExtent struct {
	Bytes    []byte
	Evidence InputSourceEvidence
}

// newObservedInput is an adapter-only construction boundary. It creates the fresh
// ID, never accepts an old acquisition ID and never attempts strict fallback.
func newObservedInput(ctx context.Context, in SourcePreparationInput, access string, driver observedInputDriver) (result *PreparedInput, err error) {
	if driver == nil {
		return nil, ErrPreparedInput
	}
	defer func() {
		if err != nil {
			driver.Close()
		}
	}()
	if !validSourcePreparation(in) || (access != "finite_random" && access != "finite_sequential" && access != "growing") {
		return nil, ErrPreparedInput
	}
	if in.Selection.ExpectedSourceVersionID != "" {
		return nil, mediasource.ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	observation, err := driver.Observe(ctx)
	if err != nil {
		return nil, err
	}
	if !validSourceObservation(observation) {
		return nil, ErrPreparedInput
	}
	deps, err := driver.Dependencies(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateInputDependencies(deps); err != nil {
		return nil, err
	}
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	id := base64.RawURLEncoding.EncodeToString(token[:])
	ref := SourceReference{ObservedSourceReference, id}
	if err := validateInputPins(in, ref, deps); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot := ObservedInputSnapshot{AcquisitionID: id, Sequence: 1, Continuity: "intact", Observation: observation}
	evidence := InputSourceEvidence{Reference: ref, ObservationInterval: &ObservationInterval{1, 1}}
	life, cancel := context.WithCancel(context.Background())
	acquired := &observedAcquisition{driver: driver, life: life, cancel: cancel, gate: make(chan struct{}, 1), snapshot: snapshot, deps: copyInputDependencies(deps), baseline: observation, dependencyBaselines: make(map[string]SourceObservation)}
	for _, d := range deps {
		if d.Observation != nil {
			acquired.dependencyBaselines[d.ID] = d.Observation.Observation
		}
	}
	meta := PreparedInputMetadata{Input: in, Reference: ref, Access: access, Length: observation.Size, FactsStatus: "unknown", InitialEvidence: evidence, InitialObservation: copyObservation(&snapshot), Dependencies: copyInputDependencies(deps)}
	return &PreparedInput{metadata: meta, observed: acquired}, nil
}
func (a *observedAcquisition) stateError() error { a.mu.Lock(); defer a.mu.Unlock(); return a.terminal }
func (a *observedAcquisition) lose(err error) error {
	a.mu.Lock()
	if a.terminal == nil {
		a.terminal = err
		a.snapshot.Continuity = "lost"
	}
	result := a.terminal
	a.mu.Unlock()
	a.cancel()
	return result
}
func (a *observedAcquisition) close() error {
	a.lose(context.Canceled)
	a.closeOnce.Do(func() { a.closeErr = a.driver.Close() })
	return a.closeErr // actual driver/supervisor still owns accounting until exit
}
func (a *observedAcquisition) enter(parent context.Context) (context.Context, func(), error) {
	if err := a.stateError(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(a.life, cancel)
	select {
	case a.gate <- struct{}{}:
		if err := a.stateError(); err != nil {
			<-a.gate
			stop()
			cancel()
			return nil, nil, err
		}
		if err := ctx.Err(); err != nil {
			<-a.gate
			stop()
			cancel()
			return nil, nil, err
		}
		return ctx, func() { <-a.gate; stop(); cancel() }, nil
	case <-ctx.Done():
		stop()
		cancel()
		return nil, nil, ctx.Err()
	}
}
func observationsConflict(before, after SourceObservation) bool {
	return before.RootBindingID != after.RootBindingID || before.ObjectBindingID != after.ObjectBindingID || (before.Size.Known && after.Size.Known && before.Size.Value != after.Size.Value) || (before.ModifiedNS.Known && after.ModifiedNS.Known && before.ModifiedNS.Value != after.ModifiedNS.Value) || (before.ChangeToken != "" && after.ChangeToken != "" && before.ChangeToken != after.ChangeToken)
}
func mergeObservedKnowledge(before, after SourceObservation) SourceObservation {
	if after.Size.Known {
		before.Size = after.Size
	}
	if after.ModifiedNS.Known {
		before.ModifiedNS = after.ModifiedNS
	}
	if after.ChangeToken != "" {
		before.ChangeToken = after.ChangeToken
	}
	return before
}
func dependenciesContinue(before, after []PreparedInputDependency) bool {
	if len(before) != len(after) {
		return false
	}
	old := map[string]PreparedInputDependency{}
	for _, d := range before {
		old[d.ID] = d
	}
	for _, d := range after {
		p, ok := old[d.ID]
		if !ok || p.Role != d.Role || p.Reference != d.Reference || p.Version.ID() != d.Version.ID() || p.Digest != d.Digest {
			return false
		}
		if d.Reference.Kind == ObservedSourceReference {
			if d.Provenance.ObservationInterval.Last < p.Provenance.ObservationInterval.Last || observationsConflict(p.Observation.Observation, d.Observation.Observation) {
				return false
			}
		}
	}
	return true
}

// refresh runs only while the gate is held. Observation sequencing is ours, not
// an arbitrary provider content revision or a renderer observation counter.
func (a *observedAcquisition) refresh(ctx context.Context) (InputValidation, error) {
	observation, err := a.driver.Observe(ctx)
	if err != nil {
		if errors.Is(err, ErrObservedContinuityLost) || errors.Is(err, mediasource.ErrSourceChanged) {
			return InputValidation{}, a.lose(err)
		}
		return InputValidation{}, err
	}
	// A successful observation is evidence even if an independent dependency
	// check later fails. Never discard a proven conflict or newly known value.
	a.mu.Lock()
	if a.terminal != nil {
		err := a.terminal
		a.mu.Unlock()
		return InputValidation{}, err
	}
	if !validSourceObservation(observation) {
		a.mu.Unlock()
		return InputValidation{}, a.lose(ErrPreparedInput)
	}
	if observationsConflict(a.baseline, observation) {
		a.mu.Unlock()
		return InputValidation{}, a.lose(ErrObservedContinuityLost)
	}
	a.baseline = mergeObservedKnowledge(a.baseline, observation)
	a.mu.Unlock()
	deps, err := a.driver.Dependencies(ctx)
	if err != nil {
		if errors.Is(err, ErrObservedContinuityLost) || errors.Is(err, mediasource.ErrSourceChanged) {
			return InputValidation{}, a.lose(err)
		}
		return InputValidation{}, err
	}
	if !validSourceObservation(observation) || validateInputDependencies(deps) != nil {
		return InputValidation{}, a.lose(ErrPreparedInput)
	}
	a.mu.Lock()
	if a.terminal != nil {
		err := a.terminal
		a.mu.Unlock()
		return InputValidation{}, err
	}
	if observationsConflict(a.baseline, observation) || !dependenciesContinue(a.deps, deps) || a.snapshot.Sequence == math.MaxInt64 {
		a.mu.Unlock()
		return InputValidation{}, a.lose(ErrObservedContinuityLost)
	}
	for _, d := range deps {
		if d.Observation != nil && observationsConflict(a.dependencyBaselines[d.ID], d.Observation.Observation) {
			a.mu.Unlock()
			return InputValidation{}, a.lose(ErrObservedContinuityLost)
		}
	}
	a.baseline = mergeObservedKnowledge(a.baseline, observation)
	for _, d := range deps {
		if d.Observation != nil {
			a.dependencyBaselines[d.ID] = mergeObservedKnowledge(a.dependencyBaselines[d.ID], d.Observation.Observation)
		}
	}
	a.snapshot.Sequence++
	a.snapshot.Observation = observation
	a.deps = copyInputDependencies(deps)
	snapshot := a.snapshot
	a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return InputValidation{}, err
	}
	return InputValidation{Evidence: InputSourceEvidence{Reference: SourceReference{ObservedSourceReference, snapshot.AcquisitionID}, ObservationInterval: &ObservationInterval{1, snapshot.Sequence}}, Observation: copyObservation(&snapshot), Dependencies: copyInputDependencies(deps)}, nil
}
func (a *observedAcquisition) validate(parent context.Context) (InputValidation, error) {
	ctx, release, err := a.enter(parent)
	if err != nil {
		return InputValidation{}, err
	}
	defer release()
	return a.refresh(ctx)
}

// ReadExtent is explicitly uncommitted producer input, never an HTTP media body.
// Immutable overlapping ranges and published HLS resources require a separate
// bounded retention/equivalence layer; this method does not enable that delivery.
func (p *PreparedInput) ReadExtent(parent context.Context, offset, length int64) (ObservedExtent, error) {
	if p.strong != nil {
		if length <= 0 || length > maxObservedExtent {
			return ObservedExtent{}, ErrPreparedInput
		}
		if _, err := p.Validate(parent); err != nil {
			return ObservedExtent{}, err
		}
		r, err := p.strong.OpenRange(parent, offset, length)
		if err != nil {
			return ObservedExtent{}, err
		}
		data, err := io.ReadAll(io.LimitReader(r, length+1))
		closeErr := r.Close()
		if err != nil {
			return ObservedExtent{}, err
		}
		if closeErr != nil {
			return ObservedExtent{}, closeErr
		}
		if int64(len(data)) != length {
			return ObservedExtent{}, io.ErrUnexpectedEOF
		}
		if _, err = p.Validate(parent); err != nil {
			return ObservedExtent{}, err
		}
		return ObservedExtent{Bytes: data, Evidence: copyInputEvidence(p.metadata.InitialEvidence)}, nil
	}
	if p.observed == nil || offset < 0 || length <= 0 || length > maxObservedExtent || offset > math.MaxInt64-length {
		return ObservedExtent{}, ErrPreparedInput
	}
	a := p.observed
	ctx, release, err := a.enter(parent)
	if err != nil {
		return ObservedExtent{}, err
	}
	defer release()
	before, err := a.refresh(ctx)
	if err != nil {
		return ObservedExtent{}, err
	}
	size := before.Observation.Observation.Size
	if size.Known && (offset > size.Value || length > size.Value-offset) {
		return ObservedExtent{}, io.ErrUnexpectedEOF
	}
	data, readErr := a.driver.ReadExtent(ctx, offset, length)
	if errors.Is(readErr, ErrObservedContinuityLost) || errors.Is(readErr, mediasource.ErrSourceChanged) {
		return ObservedExtent{}, a.lose(readErr)
	}
	after, checkErr := a.refresh(ctx)
	if checkErr != nil {
		return ObservedExtent{}, checkErr
	}
	if readErr != nil {
		if errors.Is(readErr, ErrObservedContinuityLost) || errors.Is(readErr, mediasource.ErrSourceChanged) {
			return ObservedExtent{}, a.lose(readErr)
		}
		return ObservedExtent{}, readErr
	}
	if int64(len(data)) != length {
		return ObservedExtent{}, io.ErrUnexpectedEOF
	}
	if err := ctx.Err(); err != nil {
		return ObservedExtent{}, err
	}
	after.Evidence.ObservationInterval.First = before.Observation.Sequence
	return ObservedExtent{Bytes: append([]byte(nil), data...), Evidence: copyInputEvidence(after.Evidence)}, nil
}
