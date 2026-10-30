package playback

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"strings"
	"unicode/utf8"
)

const (
	StrongSourceReference   = "strong_version"
	ObservedSourceReference = "observed_acquisition"
	SealedArtifactReference = "sealed_artifact"
)

var ErrPreparedInput = errors.New("invalid prepared input evidence")

type SourceReference struct{ Kind, ID string }
type ObservationInterval struct{ First, Last int64 }
type InputSourceEvidence struct {
	Reference           SourceReference
	ObservationInterval *ObservationInterval
}
type KnownInt64 struct {
	Known bool
	Value int64
}
type SourceObservation struct {
	RootBindingID, ObjectBindingID string
	Size, ModifiedNS               KnownInt64
	ChangeToken                    string
}
type ObservedInputSnapshot struct {
	AcquisitionID string
	Sequence      int64
	Continuity    string
	Observation   SourceObservation
}
type InputDependencyReference struct{ Kind, ID string }
type PreparedInputDependency struct {
	ID, Role    string
	Reference   InputDependencyReference
	Version     mediasource.Version
	Provenance  *InputSourceEvidence
	Observation *ObservedInputSnapshot
	Digest      string
}
type PreparedInputMetadata struct {
	Input              SourcePreparationInput
	Reference          SourceReference
	Access             string
	Length             KnownInt64
	FactsStatus        string
	InitialEvidence    InputSourceEvidence
	InitialObservation *ObservedInputSnapshot
	Dependencies       []PreparedInputDependency
}
type InputValidation struct {
	Evidence     InputSourceEvidence
	Observation  *ObservedInputSnapshot
	Dependencies []PreparedInputDependency
}
type InputPreparer interface {
	PrepareInput(context.Context, SourcePreparationInput) (*PreparedInput, error)
}

// PreparedInput has one private variant. Observed references cannot be converted
// to Version; neither metadata nor a reference ID grants media authority.
type PreparedInput struct {
	metadata PreparedInputMetadata
	strong   *strongInputSource
	observed *observedAcquisition
}

func (p *PreparedInput) Metadata() PreparedInputMetadata {
	m := p.metadata
	m.InitialEvidence = copyInputEvidence(m.InitialEvidence)
	m.InitialObservation = copyObservation(m.InitialObservation)
	m.Dependencies = copyInputDependencies(m.Dependencies)
	return m
}
func (p *PreparedInput) StrongVersion() (mediasource.Version, bool) {
	if p.strong == nil {
		return mediasource.Version{}, false
	}
	return p.strong.Metadata().Version, true
}
func (p *PreparedInput) Close() error {
	if p.strong != nil {
		return p.strong.Close()
	}
	if p.observed != nil {
		return p.observed.close()
	}
	return ErrPreparedInput
}
func (p *PreparedInput) Validate(ctx context.Context) (InputValidation, error) {
	if p.observed != nil {
		return p.observed.validate(ctx)
	}
	if p.strong == nil {
		return InputValidation{}, ErrPreparedInput
	}
	s := p.strong
	if err := s.ValidateDescriptorDependency(ctx); err != nil {
		return InputValidation{}, err
	}
	if s.remote == nil && s.backend == nil {
		file, err := s.OpenDescriptor(ctx)
		if err != nil {
			return InputValidation{}, err
		}
		if err := file.Close(); err != nil {
			return InputValidation{}, err
		}
	} else {
		// Source condition is checked as well as its separate configuration dependency.
		body, err := s.OpenRange(ctx, 0, 1)
		if err != nil {
			return InputValidation{}, err
		}
		_, err = io.Copy(io.Discard, body)
		closeErr := body.Close()
		if err != nil {
			return InputValidation{}, err
		}
		if closeErr != nil {
			return InputValidation{}, closeErr
		}
	}
	deps := copyInputDependencies(p.metadata.Dependencies)
	if s.dependencies != nil {
		var err error
		deps, err = s.dependencies(ctx)
		if err != nil {
			return InputValidation{}, err
		}
	}
	return InputValidation{Evidence: copyInputEvidence(p.metadata.InitialEvidence), Dependencies: deps}, nil
}

// newStrongPreparedInput takes ownership of the acquired backend on every path.
// It is a private construction step, not a second preparation interface.
func newStrongPreparedInput(in SourcePreparationInput, s *strongInputSource) (result *PreparedInput, err error) {
	defer func() {
		if err != nil && s != nil {
			s.Close()
		}
	}()
	if s == nil || !validSourcePreparation(in) {
		return nil, ErrPreparedInput
	}
	m := s.Metadata()
	if m.Version.ID() == "" || m.Access != "finite_random" || m.Size != m.Version.Evidence().Size || m.FactsStatus != "unknown" || m.Fence != in.Fence || m.AssetID != in.Selection.AssetID || m.ItemID != in.Selection.ItemID || m.LibraryID != in.Selection.LibraryID || m.RootID != in.Selection.RootID || m.InventorySize != in.Selection.InventorySize || m.InventoryModifiedNS != in.Selection.InventoryModifiedNS || m.NetworkPolicyRevision != in.Selection.NetworkPolicyRevision || s.path != in.Selection.Path {
		return nil, ErrPreparedInput
	}
	ref := SourceReference{StrongSourceReference, m.Version.ID()}
	meta := PreparedInputMetadata{Input: in, Reference: ref, Access: m.Access, Length: KnownInt64{true, m.Version.Evidence().Size}, FactsStatus: "unknown", InitialEvidence: InputSourceEvidence{Reference: ref}}
	meta.Dependencies = copyInputDependencies(s.initialDependencies)
	if m.DescriptorVersion.ID() != "" {
		dr := SourceReference{StrongSourceReference, m.DescriptorVersion.ID()}
		meta.Dependencies = []PreparedInputDependency{{ID: "access:" + m.DescriptorVersion.ID(), Role: "access_configuration", Reference: InputDependencyReference{dr.Kind, dr.ID}, Version: m.DescriptorVersion, Provenance: &InputSourceEvidence{Reference: dr}, Digest: m.DescriptorDigest}}
	}
	if err := validateInputDependencies(meta.Dependencies); err != nil {
		return nil, err
	}
	if err := validateInputPins(in, ref, meta.Dependencies); err != nil {
		return nil, err
	}
	return &PreparedInput{metadata: meta, strong: s}, nil
}
func validateInputPins(in SourcePreparationInput, ref SourceReference, deps []PreparedInputDependency) error {
	if pin := in.Selection.ExpectedSourceVersionID; pin != "" && (ref.Kind != StrongSourceReference || ref.ID != pin) {
		return mediasource.ErrSourceChanged
	}
	if pin := in.Selection.ExpectedDescriptorVersionID; pin != "" {
		for _, d := range deps {
			if d.Role == "access_configuration" && d.Reference.Kind == StrongSourceReference && d.Version.ID() == pin {
				return nil
			}
		}
		return mediasource.ErrSourceChanged
	}
	return nil
}
func validInputText(s string) bool {
	return s != "" && len(s) <= 4096 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}
func validSourceReference(r SourceReference) bool {
	return (r.Kind == StrongSourceReference || r.Kind == ObservedSourceReference) && validInputText(r.ID)
}
func validInputEvidence(e InputSourceEvidence) bool {
	if !validSourceReference(e.Reference) {
		return false
	}
	if e.Reference.Kind == StrongSourceReference {
		return e.ObservationInterval == nil
	}
	return e.ObservationInterval != nil && e.ObservationInterval.First > 0 && e.ObservationInterval.Last >= e.ObservationInterval.First
}
func validSourceObservation(o SourceObservation) bool {
	return validInputText(o.RootBindingID) && validInputText(o.ObjectBindingID) && (!o.Size.Known || o.Size.Value >= 0) && (o.Size.Known || o.Size.Value == 0) && (o.ModifiedNS.Known || o.ModifiedNS.Value == 0) && (o.ChangeToken == "" || validInputText(o.ChangeToken))
}
func validateInputDependencies(deps []PreparedInputDependency) error {
	if len(deps) > 128 {
		return ErrPreparedInput
	}
	seen := map[string]bool{}
	config := false
	for _, d := range deps {
		if !validInputText(d.ID) || !validInputText(d.Role) || seen[d.ID] || d.Provenance == nil || !validInputEvidence(*d.Provenance) {
			return ErrPreparedInput
		}
		seen[d.ID] = true
		switch d.Role {
		case "subtitle_sidecar", "font", "style", "bitmap", "access_configuration", "other":
		default:
			return ErrPreparedInput
		}
		if d.Digest != "" {
			if len(d.Digest) != 64 || strings.ToLower(d.Digest) != d.Digest {
				return ErrPreparedInput
			}
			if _, err := hex.DecodeString(d.Digest); err != nil {
				return ErrPreparedInput
			}
		}
		if d.Role == "access_configuration" {
			if config {
				return ErrPreparedInput
			}
			config = true
		}
		ref := SourceReference{d.Reference.Kind, d.Reference.ID}
		if d.Provenance.Reference != ref {
			return ErrPreparedInput
		}
		switch d.Reference.Kind {
		case StrongSourceReference:
			if d.Version.ID() == "" || d.Version.ID() != ref.ID || d.Observation != nil {
				return ErrPreparedInput
			}
		case ObservedSourceReference:
			o := d.Observation
			if d.Version.ID() != "" || o == nil || o.AcquisitionID != ref.ID || o.Sequence != d.Provenance.ObservationInterval.Last || o.Continuity != "intact" || !validSourceObservation(o.Observation) {
				return ErrPreparedInput
			}
		default:
			// IDs cannot prove sealed ownership. A future artifact-store integration must
			// resolve independent immutable ownership/provenance before enabling this arm.
			return ErrPreparedInput
		}
	}
	return nil
}
func copyInputEvidence(e InputSourceEvidence) InputSourceEvidence {
	if e.ObservationInterval != nil {
		v := *e.ObservationInterval
		e.ObservationInterval = &v
	}
	return e
}
func copyObservation(o *ObservedInputSnapshot) *ObservedInputSnapshot {
	if o == nil {
		return nil
	}
	v := *o
	return &v
}
func copyInputDependencies(ds []PreparedInputDependency) []PreparedInputDependency {
	out := append([]PreparedInputDependency(nil), ds...)
	for i := range out {
		if out[i].Provenance != nil {
			v := copyInputEvidence(*out[i].Provenance)
			out[i].Provenance = &v
		}
		out[i].Observation = copyObservation(out[i].Observation)
	}
	return out
}
