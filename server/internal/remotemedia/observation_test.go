package remotemedia

import (
	"errors"
	"testing"
)

func TestObservationNeverSubstitutesServedETagForProviderEvidence(t *testing.T) {
	o := FileObservation{SourceID: "source", Incarnation: "1", Object: "film.mkv", Size: 10, ModifiedKnown: true, ServeETag: `"served-mtime-size"`}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if o.ProviderETag != "" || len(o.ProviderHashes) != 0 {
		t.Fatal("invented provider evidence")
	}
	next := o
	next.ProviderETag = `"new-provider-observation"`
	if !errors.Is(o.Check(next), ErrObservationChanged) {
		t.Fatal("validator availability change did not invalidate")
	}
	next = o
	next.ModifiedKnown = false
	if !errors.Is(o.Check(next), ErrObservationChanged) {
		t.Fatal("missing metadata accepted")
	}
	// Equal observable facts do not detect a provider that changed bytes while
	// reusing all metadata. This type deliberately provides no immutable Version.
	if err := o.Check(o); err != nil {
		t.Fatal(err)
	}
}

func TestObservationCopiesAndComparesEveryProviderHash(t *testing.T) {
	o := FileObservation{SourceID: "source", Incarnation: "1", Object: "file", Size: 10, ModifiedKnown: true, ServeETag: `"served"`, ProviderHashes: []ProviderHash{{Kind: "sha256", Value: "s"}, {Kind: "md5", Value: "m"}}}
	captured, err := o.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if captured.ProviderHashes[0].Kind != "md5" || o.ProviderHashes[0].Kind != "sha256" {
		t.Fatal("mutated caller or lost canonical order")
	}
	if !captured.Equal(o) {
		t.Fatal("hash set order changed identity")
	}
	o.ProviderHashes[0].Value = "changed"
	if !errors.Is(captured.Check(o), ErrObservationChanged) {
		t.Fatal("lost a provider hash change")
	}
	o = captured
	o.ProviderHashes = append(append([]ProviderHash(nil), captured.ProviderHashes...), captured.ProviderHashes[0])
	if o.Validate() == nil {
		t.Fatal("duplicate hash kind accepted")
	}
}

func TestRangePreconditionDoesNotInventMissingOriginMetadata(t *testing.T) {
	o := FileObservation{SourceID: "source", Incarnation: "1", Object: "file", Size: 10}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	if o.ValidateForRange() == nil {
		t.Fatal("unknown metadata admitted ranges")
	}
	o.ServeETag = `"served-precondition"`
	if o.ValidateForRange() != nil || o.ModifiedKnown || o.ProviderETag != "" || len(o.ProviderHashes) != 0 {
		t.Fatal("missing origin time became a provider cut or fabricated identity")
	}
	o.ModifiedKnown = true
	o.ServeETag = `W/"weak"`
	if o.Validate() != nil || o.ValidateForRange() == nil {
		t.Fatal("weak served tag was either lost or promoted")
	}
}
