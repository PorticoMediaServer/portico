package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func TestFileInfoOpeningPreparationHasNoDetailAndAcceptsOnlyReadyEpoch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFileInfoLaneFixture(t)
	f.c.Exec(`UPDATE catalog_entities SET info_source_blockers=1 WHERE id=?`, f.item.ID)
	out, e := f.r.PrepareOpening(context.Background(), "family", f.item.Public, "detail", "none")
	if e != nil || out.Preparing == nil || out.Preparing.Detail != nil || out.Opening != nil {
		t.Fatal(out, e)
	}
	id := out.Preparing.QueryID
	if _, e = f.r.Poll(context.Background(), "foreign", id); !errors.Is(e, readprep.ErrStale) {
		t.Fatal("foreign preparation admitted", e)
	}
	if e = f.r.Cancel(context.Background(), "foreign", id); e != nil {
		t.Fatal(e)
	}
	out, e = f.r.Poll(context.Background(), "family", id)
	if e != nil || out.Preparing == nil || out.Preparing.Detail != nil {
		t.Fatal(out, e)
	}
	f.c.Exec(`UPDATE catalog_entities SET info_source_blockers=0 WHERE id=?`, f.item.ID)
	deadline := time.Now().Add(8 * time.Second)
	for out.Preparing != nil && time.Now().Before(deadline) {
		out.Release()
		time.Sleep(time.Millisecond)
		out, e = f.r.Poll(context.Background(), "family", id)
		if e != nil {
			t.Fatal(e)
		}
	}
	defer out.Release()
	if out.Opening == nil || out.Opening.Kind != "detail" || out.Opening.Related != "none" || out.Opening.Epoch < 1 {
		t.Fatal(out)
	}
	var opening string
	original := ContentRevision{Catalog: 71, Viewer: 8}
	e = dbwork.WithReadSnapshot(context.Background(), f.c.DB, func(ctx context.Context) error {
		var e error
		opening, e = f.r.AcceptPreparedOpening(ctx, "family", *out.Opening, original)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	proof, _, e := f.r.opening(context.Background(), "family", opening)
	if e != nil || proof.Revision != original {
		t.Fatal(proof, e)
	}
	f.c.Exec(`UPDATE catalog_entities SET info_desired_epoch=info_desired_epoch+1,info_applied_epoch=info_applied_epoch+1 WHERE id=?`, f.item.ID)
	e = dbwork.WithReadSnapshot(context.Background(), f.c.DB, func(ctx context.Context) error {
		_, e := f.r.AcceptPreparedOpening(ctx, "family", *out.Opening, original)
		return e
	})
	if !errors.Is(e, readprep.ErrStale) {
		t.Fatal("ready preparation adopted another epoch", e)
	}
}

func TestFileInfoOpeningPreparationConcurrentInterestsRetireIndependently(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFileInfoLaneFixture(t)
	f.c.Exec(`UPDATE catalog_entities SET info_source_blockers=1 WHERE id=?`, f.item.ID)
	one, e := f.r.PrepareOpening(context.Background(), "family", f.item.Public, "detail", "none")
	if e != nil || one.Preparing == nil {
		t.Fatal(one, e)
	}
	two, e := f.r.PrepareOpening(context.Background(), "family", f.item.Public, "detail", "none")
	if e != nil || two.Preparing == nil {
		t.Fatal(two, e)
	}
	first, second := one.Preparing.QueryID, two.Preparing.QueryID
	if first == second {
		t.Fatal("independent opening interests share cancellation ownership")
	}
	f.c.Exec(`UPDATE catalog_entities SET info_source_blockers=0 WHERE id=?`, f.item.ID)
	for _, id := range []string{first, second} {
		out, e := f.r.Poll(context.Background(), "family", id)
		deadline := time.Now().Add(5 * time.Second)
		for e == nil && out.Preparing != nil && time.Now().Before(deadline) {
			out.Release()
			time.Sleep(time.Millisecond)
			out, e = f.r.Poll(context.Background(), "family", id)
		}
		if e != nil || out.Opening == nil {
			t.Fatal("one release retired another opening interest", out, e)
		}
		out.Release()
	}
}

func TestFileInfoPreparedOpeningHasFirstRequestHeadroomAtBucketBoundary(t *testing.T) {
	f := newFileInfoLaneFixture(t)
	now := time.Unix(25*60*100, 0).Add(-time.Nanosecond)
	f.r.now = func() time.Time { return now }
	f.r.anchors.now = f.r.now
	out, e := f.r.PrepareOpening(context.Background(), "family", f.item.Public, "detail", "none")
	if e != nil || out.Opening == nil {
		t.Fatal(out, e)
	}
	defer out.Release()
	var token string
	e = dbwork.WithReadSnapshot(context.Background(), f.c.DB, func(ctx context.Context) error {
		var e error
		token, e = f.r.AcceptPreparedOpening(ctx, "family", *out.Opening, ContentRevision{Catalog: 71, Viewer: 8})
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	proof, e := f.r.openings.decodeOpening("family", token)
	if e != nil {
		t.Fatal(e)
	}
	if remaining := proof.Expires.Sub(now); remaining < 5*time.Minute || remaining > 30*time.Minute {
		t.Fatal("new opening expires before its first request", remaining)
	}
}
