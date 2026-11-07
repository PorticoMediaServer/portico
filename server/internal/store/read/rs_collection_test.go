package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func newRSCollectionFixture(t *testing.T, live bool) (*browseFixture, *CollectionReader) {
	t.Helper()
	f := composeReadFixture(t, newRSCollectionSource(t), append(browseDefinitions(), CollectionDefinitions()...), readfixture.LiveConfig(time.Now), !live, rsCollectionHash(t))
	r, err := NewCollectionReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func TestRSCollectionTraversal(t *testing.T) {
	f, r := newRSCollectionFixture(t, false)
	in := CollectionInput{View: f.input, Collection: 1101}
	in.View.Limit = 7
	var got []int64
	for n := 0; n < 100; n++ {
		p, cost, err := r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil {
			t.Fatal(err)
		}
		if p.RawRows > MaxRaw || p.RawPositions > p.RawRows || len(p.Items) > 7 || cost.LookupStatements > 9 || cost.PublicationStatements != 6 {
			t.Fatalf("unbounded window %+v %+v", p, cost)
		}
		for _, item := range p.Items {
			if item.Kind != 1 {
				t.Fatal("wrong member kind")
			}
			got = append(got, item.ID)
		}
		if p.Exhausted {
			break
		}
		if p.Next == nil || n == 99 {
			t.Fatal("missing/nonterminating raw cursor")
		}
		in.After = p.Next
	}
	var want []int64
	for id := int64(1); id <= 201; id++ {
		if id%9 != 0 {
			want = append(want, id)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// A completely hidden raw window advances without pretending exhaustion.
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id<=201`)
	in.After = nil
	p, _, err := r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || len(p.Items) != 0 || p.Exhausted || p.Next == nil || p.RawPositions != MaxRaw {
		t.Fatalf("hidden raw tail %+v %v", p, err)
	}
	// Collection incarnation/revision and query identity bind the continuation.
	in.After = p.Next
	f.exec(t, `UPDATE catalog_entities SET collection_revision=collection_revision+1 WHERE id=1101`)
	p, _, err = r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(p, CollectionPage{}) {
		t.Fatal("stale source escaped", err)
	}
	in.Collection = 1102
	p, _, err = r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err == nil || !reflect.DeepEqual(p, CollectionPage{}) {
		t.Fatal("cursor retargeted", err)
	}
}

func TestRSCollectionPublicationAndLIVE(t *testing.T) {
	f, r := newRSCollectionFixture(t, true)
	in := CollectionInput{View: f.input, Collection: 1101}
	q, request, output := liveRequests("rs-collection")
	p, _, err := r.Members(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || !reflect.DeepEqual(p, CollectionPage{}) {
		t.Fatal("unadmitted read", err)
	}
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		p, _, e := r.Members(ctx, request, time.Second, out, in)
		if e != nil || len(p.Items) != 40 {
			t.Fatalf("public collection %+v %v", p, e)
		}
		if f.authority.ForegroundUsage().Live != 1 {
			t.Fatal("lost consumer custody")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
	// A fresh final denial wins and releases no private values.
	r.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	q, request, output = liveRequests("rs-collection-revoked")
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		p, _, e := r.Members(ctx, request, time.Second, out, in)
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, CollectionPage{}) {
			t.Fatal("revoked draft leaked", e)
		}
		return e
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestRSCollectionSourceDriftAndCancellation(t *testing.T) {
	f, r := newRSCollectionFixture(t, false)
	in := CollectionInput{View: f.input, Collection: 1101}
	r.snapshots = &rsCollectionDriftSnapshots{Snapshots: f.snapshots, drift: func() {
		f.exec(t, `UPDATE catalog_entities SET collection_revision=collection_revision+1 WHERE id=1101`)
	}}
	p, cost, err := r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrStale) || cost.Attempts != 2 || !reflect.DeepEqual(p, CollectionPage{}) {
		t.Fatal("source drift leaked", cost.Attempts, err)
	}
	r.snapshots = f.snapshots
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p, _, err = r.members(ctx, foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(p, CollectionPage{}) {
		t.Fatal("cancelled draft leaked", err)
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	p, _, err = r.members(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(p, CollectionPage{}) {
		t.Fatal("unproved container visibility", err)
	}
	var nilReader *CollectionReader
	_, _, err = nilReader.Members(t.Context(), governor.Request{}, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) {
		t.Fatal("nil reader", err)
	}
}

// Each attempt has initial, membership and final windows. Drift after every
// membership window prevents even the one allowed whole-operation retry.
type rsCollectionDriftSnapshots struct {
	*governor.Snapshots
	calls int
	drift func()
}

func (s *rsCollectionDriftSnapshots) Read(ctx context.Context, request governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.calls++
	result, err := s.Snapshots.Read(ctx, request, budget, read)
	if s.calls%3 == 2 {
		s.drift()
	}
	return result, err
}
