package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestFacetRuntimeSnapshotRetryPreservesDurableBuildAndReadyBasis(t *testing.T) {
	for _, failure := range []error{context.DeadlineExceeded, dbwork.ErrReadPressure, dbwork.ErrWriterCapacity} {
		t.Run(failure.Error(), func(t *testing.T) {
			r := &FacetRuntime{}
			d := &facetRuntimeDemand{build: 17, ready: 11, gap: 2 * time.Second, changed: make(chan struct{})}
			next := time.Now().Add(time.Minute)
			d.next = next
			if !facetRuntimeTemporary(errors.Join(errors.New("snapshot"), failure)) {
				t.Fatal("wrapped temporary failure became fatal")
			}
			if wait := r.maintenanceFailure(context.Background(), d, d.build, failure); wait != time.Second {
				t.Fatal("missing retry pause", wait)
			}
			if d.build != 17 || d.ready != 11 || d.retiring != 0 || d.err != nil || d.gap != 2*time.Second || d.next != next {
				t.Fatalf("temporary snapshot failure discarded progress: %+v", d)
			}
			select {
			case <-d.changed:
				t.Fatal("temporary failure published terminal change")
			default:
			}
		})
	}
	for _, failure := range []error{ErrFacetBaseInput, ErrFacetBaseCapacity, errors.New("malformed catalogue")} {
		if facetRuntimeTemporary(failure) {
			t.Fatal("terminal failure was hidden", failure)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &FacetRuntime{}
	d := &facetRuntimeDemand{build: 17, changed: make(chan struct{})}
	r.maintenanceFailure(ctx, d, d.build, context.Canceled)
	if d.build != 17 || d.err != nil {
		t.Fatal("shutdown changed durable state", d)
	}
}
