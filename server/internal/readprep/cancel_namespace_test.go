package readprep

import (
	"context"
	"errors"
	"testing"
)

func TestCancelNamespacePreservesOtherAdapterAndCallerDemand(t *testing.T) {
	s := storeFor(t, Config{})
	a := Key{Namespace: "actions", Basis: "basis", Canonical: "same"}
	b := Key{Namespace: "browse", Basis: "basis", Canonical: "same"}
	own := submit(t, s, "caller", a, simplePlan("action"))
	browse := submit(t, s, "caller", b, simplePlan("browse"))
	foreign := submit(t, s, "other", a, simplePlan("action"))
	for _, tc := range []struct {
		h      Handle
		family string
	}{{browse, "actions"}, {own, "browse"}, {foreign, "actions"}, {"absent", "actions"}} {
		if err := s.CancelNamespace(context.Background(), "caller", tc.h, tc.family); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		h      Handle
		caller Caller
	}{{own, "caller"}, {browse, "caller"}, {foreign, "other"}} {
		r := ready(t, s, tc.caller, tc.h)
		r.Release()
	}
	if err := s.CancelNamespace(context.Background(), "caller", own, "actions"); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Poll(context.Background(), "caller", own); !errors.Is(err, ErrStale) {
		r.Release()
		t.Fatal("released action survived", err)
	}
	r := ready(t, s, "caller", browse)
	r.Release()
	r = ready(t, s, "other", foreign)
	r.Release()
	if err := s.CancelNamespace(context.Background(), "caller", browse, ""); !errors.Is(err, ErrStale) {
		t.Fatal("empty namespace broadened cancellation", err)
	}
	r = ready(t, s, "caller", browse)
	r.Release()
}
