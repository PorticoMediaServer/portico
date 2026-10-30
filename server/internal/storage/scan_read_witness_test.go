package storage

import (
	"context"
	"errors"
	"testing"
)

// The guard and the observation it takes are one entry: installing a newer
// guard must retire the older observation, so no inherited observation can
// speak for authority that has since been replaced.
func TestScanReadGuardReplacesTheObservationItSupersedes(t *testing.T) {
	denied := errors.New("denied")
	client := New("")
	witnessed := 0
	ctx := WithScanReadWitness(context.Background(), client,
		func(context.Context, string) (ScanReadWitness, error) {
			witnessed++
			return ScanReadWitness{SourceID: "source", RelativePath: "poster.png", Snapshot: Snapshot{Size: 1}}, nil
		})
	outer, ok, err := ObserveScanRead(ctx, "/root/poster.png")
	if err != nil || !ok || outer.Client != client || outer.RelativePath != "poster.png" {
		t.Fatal("the installed observation did not answer", outer, ok, err)
	}
	nested := WithScanReadGuard(ctx, func(context.Context, string) error { return denied })
	if err = CheckScanRead(nested, "/root/poster.png"); !errors.Is(err, denied) {
		t.Fatal("the replacing guard was not consulted", err)
	}
	if stale, ok, err := ObserveScanRead(nested, "/root/poster.png"); ok || err != nil || stale != (ScanReadObservation{}) {
		t.Fatal("a superseded observation survived the guard that replaced it", stale, ok, err)
	}
	if witnessed != 1 {
		t.Fatal("the superseded observation was consulted after replacement", witnessed)
	}
}

// A guard with no observation keeps exactly its previous behaviour: it is
// consulted at admission and it is never evidence about a candidate.
func TestScanReadGuardAloneKeepsItsErrorOnlyBehaviour(t *testing.T) {
	denied := errors.New("revoked")
	calls := 0
	ctx := WithScanReadGuard(context.Background(), func(context.Context, string) error { calls++; return denied })
	if err := CheckScanRead(ctx, "/root/poster.png"); !errors.Is(err, denied) || calls != 1 {
		t.Fatal("guard-only context did not answer at admission", err, calls)
	}
	if obs, ok, err := ObserveScanRead(ctx, "/root/poster.png"); ok || err != nil || obs != (ScanReadObservation{}) || calls != 1 {
		t.Fatal("a guard-only context invented an observation", obs, ok, err, calls)
	}
	if err := CheckScanRead(context.Background(), "/root/poster.png"); err != nil {
		t.Fatal("an uninstalled context refused admission", err)
	}
	if obs, ok, err := ObserveScanRead(context.Background(), "/root/poster.png"); ok || err != nil || obs != (ScanReadObservation{}) {
		t.Fatal("an uninstalled context invented an observation", obs, ok, err)
	}
}

// An observation that fails is never usable, and cancellation is answered
// before any observation is attempted.
func TestScanReadObservationErrorIsNeverUsable(t *testing.T) {
	revoked := errors.New("policy revoked")
	ctx := WithScanReadWitness(context.Background(), New(""),
		func(context.Context, string) (ScanReadWitness, error) {
			return ScanReadWitness{}, revoked
		})
	obs, ok, err := ObserveScanRead(ctx, "/root/poster.png")
	if !ok || !errors.Is(err, revoked) || obs != (ScanReadObservation{}) {
		t.Fatal("a failed observation was returned as usable", obs, ok, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	attempted := 0
	ctx = WithScanReadWitness(cancelled, New(""),
		func(context.Context, string) (ScanReadWitness, error) {
			attempted++
			return ScanReadWitness{SourceID: "source"}, nil
		})
	if obs, ok, err := ObserveScanRead(ctx, "/root/poster.png"); ok || !errors.Is(err, context.Canceled) || obs != (ScanReadObservation{}) {
		t.Fatal("cancellation did not answer before the observation", obs, ok, err)
	}
	if attempted != 0 {
		t.Fatal("cancellation was not checked before the observation", attempted)
	}
}

// The returned observation stays bound to the exact producer that took it.
func TestScanReadObservationKeepsItsProducerIdentity(t *testing.T) {
	first, second := New(""), New("")
	ctx := WithScanReadWitness(context.Background(), first,
		func(context.Context, string) (ScanReadWitness, error) {
			return ScanReadWitness{SourceID: "source", Root: "/root", RelativePath: "poster.png"}, nil
		})
	obs, ok, err := ObserveScanRead(ctx, "/root/poster.png")
	if err != nil || !ok || obs.Client != first || obs.Client == second {
		t.Fatal("the observation lost its producer identity", obs, ok, err)
	}
}

// Admission and observation are the same authority: one installed witness
// answers CheckScanRead and ObserveScanRead, and a transition from allow to deny
// is seen by both without reinstalling anything.
func TestScanReadGuardAndObservationShareOneWitness(t *testing.T) {
	denied := errors.New("policy revoked")
	allow := true
	paths := []string{}
	ctx := WithScanReadWitness(context.Background(), New(""),
		func(_ context.Context, path string) (ScanReadWitness, error) {
			paths = append(paths, path)
			if !allow {
				return ScanReadWitness{}, denied
			}
			return ScanReadWitness{SourceID: "source", RelativePath: "poster.png", Snapshot: Snapshot{Size: 3}}, nil
		})
	if err := CheckScanRead(ctx, "/root/poster.png"); err != nil {
		t.Fatal("an allowed witness refused admission", err)
	}
	if obs, ok, err := ObserveScanRead(ctx, "/root/poster.png"); err != nil || !ok || obs.RelativePath != "poster.png" || obs.Snapshot.Size != 3 {
		t.Fatal("an allowed witness did not answer the observation", obs, ok, err)
	}
	allow = false
	if err := CheckScanRead(ctx, "/root/poster.png"); !errors.Is(err, denied) {
		t.Fatal("a denied witness still admitted", err)
	}
	if obs, ok, err := ObserveScanRead(ctx, "/root/poster.png"); !ok || !errors.Is(err, denied) || obs != (ScanReadObservation{}) {
		t.Fatal("a denied witness was returned as usable", obs, ok, err)
	}
	if len(paths) != 4 {
		t.Fatal("admission and observation did not consult the one witness", len(paths))
	}
	for _, path := range paths {
		if path != "/root/poster.png" {
			t.Fatal("the witness was consulted for another path", path)
		}
	}
}
