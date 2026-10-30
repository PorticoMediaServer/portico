package catalog

import (
	"context"
	"database/sql"
	"errors"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// fileInfoWindowTransportAuthority is the fake live authority used by these transport tests.
// It admits one exact private caller and identity, can deny entirely, and can
// fail one exact call so the "after" reauthentication is observable.
type fileInfoWindowTransportAuthority struct {
	ad     FileInfoAdmission
	id     FileInfoIdentity
	calls  int
	failAt int
	deny   bool
}

func (g *fileInfoWindowTransportAuthority) check(_ context.Context, caller readprep.Caller, id FileInfoIdentity) (FileInfoAdmission, error) {
	g.calls++
	if g.deny || (g.failAt > 0 && g.calls >= g.failAt) || caller != "family" || id != g.id {
		return FileInfoAdmission{}, readprep.ErrStale
	}
	return g.ad, nil
}

type fileInfoWindowTransportFixture struct {
	runtime *FileInfoRuntime
	store   *readprep.Store
	db      *sql.DB
	guard   *fileInfoWindowTransportAuthority
	proof   fileInfoOpeningProof
	opening string
	advance func(time.Duration)
}

func fileInfoWindowTransportBase() time.Time {
	return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
}

func newFileInfoWindowTransportFixture(t *testing.T, cfg readprep.Config) *fileInfoWindowTransportFixture {
	t.Helper()
	c := catalogtest.Open(t)
	var clock atomic.Int64
	clock.Store(fileInfoWindowTransportBase().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }
	if cfg.Workers == 0 {
		cfg.Workers = 1
	}
	cfg.Now = now
	if cfg.OriginalTTL == 0 {
		cfg.OriginalTTL = 2 * time.Minute
	}
	if cfg.CompleteTTL == 0 {
		cfg.CompleteTTL = 5 * time.Minute
	}
	store, err := readprep.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	id := FileInfoIdentity{Item: 1, Library: 1, PublicItem: [16]byte{1, 2, 3}}
	g := &fileInfoWindowTransportAuthority{id: id, ad: FileInfoAdmission{
		Identity:              id,
		ItemIncarnation:       [16]byte{9, 8, 7},
		Head:                  compactcatalog.InfoHead{Desired: 17, Applied: 17},
		ConfigurationRevision: 4,
		Kind:                  "movie",
		Playable:              true,
		Owner:                 true,
		DetailScope:           DetailScope{"server", "library", "item", "original-detail-authority"},
		Scope:                 DetailScope{"server", "library", "item", "separate-file-authority"},
	}}
	runtime, err := NewFileInfoRuntime(c.DB, store, g.check)
	if err != nil {
		t.Fatal(err)
	}
	runtime.now = now
	runtime.anchors.now = now
	ctx := WithFileInfoAuthority(context.Background(), g.check)
	var opening string
	err = dbwork.WithReadSnapshot(ctx, c.DB, func(snapshot context.Context) error {
		var e error
		opening, e = runtime.AcceptDetailOpening(snapshot, "family", id, ContentRevision{Catalog: 5, Viewer: 6})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, _, err := runtime.opening(ctx, "family", opening)
	if err != nil {
		t.Fatal(err)
	}
	return &fileInfoWindowTransportFixture{
		runtime: runtime,
		store:   store,
		db:      c.DB,
		guard:   g,
		proof:   proof,
		opening: opening,
		advance: func(d time.Duration) { clock.Add(int64(d)) },
	}
}

func (f *fileInfoWindowTransportFixture) descriptor(lane byte, limit int) fileInfoWindowDescriptor {
	desc := fileInfoWindowDescriptor{
		Opening:   f.opening,
		Witness:   fileInfoWitness(f.guard.ad),
		Lane:      lane,
		Limit:     limit,
		Direction: fileInfoForward,
		Target:    fileInfoOccurrence{Item: f.proof.Item},
	}
	if fileInfoWindowLaneAsset(lane) {
		desc.Target = fileInfoOccurrence{Item: 41, Asset: 42}
	}
	return desc
}

// fileInfoWindowTransportPlan is the simplest legal numeric computation: a fixed
// charge, an optional one-time gate that keeps the preparation pending without
// sleeping, and a three-byte numeric payload.
type fileInfoWindowTransportPlan struct {
	gate    <-chan struct{}
	steps   int
	payload []byte
}

func (p *fileInfoWindowTransportPlan) AccountedBytes() int64 { return 256 }

func (p *fileInfoWindowTransportPlan) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	if err := b.Resize(256); err != nil {
		return readprep.StepResult{}, err
	}
	if p.gate != nil {
		select {
		case <-p.gate:
			p.gate = nil
		case <-ctx.Done():
			return readprep.StepResult{}, ctx.Err()
		}
	}
	if p.steps > 0 {
		p.steps--
		return readprep.StepResult{}, nil
	}
	return readprep.StepResult{Done: true, Payload: p.payload}, nil
}

func fileInfoWindowTransportPlanFactory(steps int, gate <-chan struct{}, initial int64) fileInfoWindowPlan {
	return func(context.Context, readprep.Caller, fileInfoOpeningProof, FileInfoAdmission, fileInfoWindowDescriptor) (readprep.Plan, error) {
		work := &fileInfoWindowTransportPlan{gate: gate, steps: steps, payload: []byte{7, 8, 9}}
		return readprep.Plan{InitialBytes: initial, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
			return work, nil
		}}, nil
	}
}

type fileInfoWindowTransportRecorder struct {
	calls   int
	desc    fileInfoWindowDescriptor
	proof   fileInfoOpeningProof
	queryID string
	fail    bool
}

func (h *fileInfoWindowTransportRecorder) hydrate(_ context.Context, _ readprep.Caller, proof fileInfoOpeningProof, _ FileInfoAdmission, desc fileInfoWindowDescriptor, summary readprep.Result, queryID string) (FileInfoWindowResult, error) {
	h.calls++
	h.desc, h.proof, h.queryID = desc, proof, queryID
	if h.fail {
		return FileInfoWindowResult{}, readprep.ErrInvalid
	}
	if summary.State != readprep.Ready || len(summary.Payload) != 3 || summary.Payload[0] != 7 || summary.Payload[2] != 9 {
		return FileInfoWindowResult{}, readprep.ErrStale
	}
	out := FileInfoWindowResult{}
	out.Files = &FileInfoFilesPage{Kind: "files", Files: []FileInfoFile{}}
	return out, nil
}

// fileInfoWindowTransportSettle polls a pending carrier until it is ready. It never
// sleeps: the bounded worker is given a scheduling opportunity and the loop is
// bounded by wall-clock time.
func fileInfoWindowTransportSettle(t *testing.T, r *FileInfoRuntime, caller readprep.Caller, out FileInfoWindowResult, err error, hydrate fileInfoWindowHydrate) (FileInfoWindowResult, error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for err == nil && out.Preparing != nil {
		queryID := out.Preparing.QueryID
		out.Release()
		if time.Now().After(deadline) {
			t.Fatal("file info preparation did not complete")
		}
		goruntime.Gosched()
		out, err = r.windowPoll(context.Background(), caller, queryID, hydrate)
	}
	return out, err
}

func fileInfoWindowTransportReady(t *testing.T, f *fileInfoWindowTransportFixture, caller readprep.Caller, out FileInfoWindowResult, err error, hydrate fileInfoWindowHydrate) FileInfoWindowResult {
	t.Helper()
	out, err = fileInfoWindowTransportSettle(t, f.runtime, caller, out, err, hydrate)
	if err != nil {
		t.Fatal(err)
	}
	if out.Files == nil {
		t.Fatal("missing ready page")
	}
	return out
}

func fileInfoWindowTransportSubmit(t *testing.T, f *fileInfoWindowTransportFixture, caller readprep.Caller, desc fileInfoWindowDescriptor, plan fileInfoWindowPlan, hydrate fileInfoWindowHydrate) FileInfoWindowResult {
	t.Helper()
	out, err := f.runtime.windowSubmit(context.Background(), caller, desc, plan, hydrate, time.Time{})
	return fileInfoWindowTransportReady(t, f, caller, out, err, hydrate)
}

func fileInfoWindowTransportContinue(t *testing.T, f *fileInfoWindowTransportFixture, caller readprep.Caller, opening, cursor string, limit int, present bool, plan fileInfoWindowPlan, hydrate fileInfoWindowHydrate) FileInfoWindowResult {
	t.Helper()
	out, err := f.runtime.windowContinue(context.Background(), caller, opening, cursor, limit, present, plan, hydrate)
	return fileInfoWindowTransportReady(t, f, caller, out, err, hydrate)
}

func fileInfoWindowTransportDecodeQuery(t *testing.T, f *fileInfoWindowTransportFixture, caller readprep.Caller, queryID string) fileInfoAnchor {
	t.Helper()
	opening, signed, err := fileInfoWindowSplitQueryID(queryID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.runtime.anchors.decodeWindowAnchor(caller, signed, fileInfoOpeningDigest(opening))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func fileInfoWindowTransportDecodeCursor(t *testing.T, f *fileInfoWindowTransportFixture, caller readprep.Caller, opening, cursor string) fileInfoAnchor {
	t.Helper()
	a, err := f.runtime.anchors.decodeWindowAnchor(caller, cursor, fileInfoOpeningDigest(opening))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestFileInfoWindowTransportSubmitReadyPollRelease(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 10)
	rec := &fileInfoWindowTransportRecorder{}
	out := fileInfoWindowTransportSubmit(t, f, "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate)
	if rec.calls != 1 {
		t.Fatalf("hydrate calls = %d", rec.calls)
	}
	if rec.desc != desc {
		t.Fatalf("hydrated descriptor changed: %+v", rec.desc)
	}
	if rec.proof != f.proof {
		t.Fatal("hydrated proof changed")
	}
	if len(rec.queryID) != fileInfoQueryIDSize {
		t.Fatalf("query id size = %d", len(rec.queryID))
	}
	if stats := f.store.Stats(); stats.Deliveries != 1 {
		t.Fatalf("ready delivery not held through transport: %+v", stats)
	}
	out.Release()
	out.Release()
	if stats := f.store.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
		t.Fatalf("delivery reservation leaked: %+v", stats)
	}

	// The root anchor signs the opening's root item, row zero and forward
	// direction, never a current revision or a page offset.
	a := fileInfoWindowTransportDecodeQuery(t, f, "family", rec.queryID)
	if a.Item != f.proof.Item || a.Asset != 0 || a.Row != 0 || a.Direction != fileInfoForward || a.Lane != fileInfoLaneFiles {
		t.Fatalf("root anchor = %+v", a)
	}

	key, err := fileInfoWindowKey("family", desc)
	if err != nil {
		t.Fatal(err)
	}
	if key.Namespace != fileInfoWindowNamespace || key.Basis != fileInfoWindowBasis(f.opening) || !strings.Contains(key.Canonical, `"family"`) {
		t.Fatalf("key = %+v", key)
	}
}

func TestFileInfoWindowTransportPendingReleasesAndKeepsOriginalDetail(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	gate := make(chan struct{})
	desc := f.descriptor(fileInfoLaneFiles, 10)
	rec := &fileInfoWindowTransportRecorder{}
	out, err := f.runtime.windowSubmit(context.Background(), "family", desc, fileInfoWindowTransportPlanFactory(0, gate, 256), rec.hydrate, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Preparing == nil {
		t.Fatal("gated preparation reported ready")
	}
	p := out.Preparing
	if p.Kind != "file-info-preparing" || p.State != "preparing" || p.QueryID == "" || p.RetryAfterMs != fileInfoWindowRetryMS {
		t.Fatalf("preparing carrier = %+v", p)
	}
	if p.Detail.Scope != f.guard.ad.DetailScope || p.Detail.Revision != f.proof.Revision {
		t.Fatalf("pending detail capture = %+v", p.Detail)
	}
	if rec.calls != 0 {
		t.Fatal("pending hydrated content")
	}
	if stats := f.store.Stats(); stats.Deliveries != 0 {
		t.Fatalf("pending kept a delivery reservation: %+v", stats)
	}

	// A newer accepted Detail must not replace the original capture on the
	// pending carrier.
	var newer string
	ctx := WithFileInfoAuthority(context.Background(), f.guard.check)
	err = dbwork.WithReadSnapshot(ctx, f.db, func(snapshot context.Context) error {
		var e error
		newer, e = f.runtime.AcceptDetailOpening(snapshot, "family", f.guard.id, ContentRevision{Catalog: 9, Viewer: 10})
		return e
	})
	if err != nil || newer == f.opening {
		t.Fatal("newer opening was not minted", err)
	}
	out, err = f.runtime.windowPoll(context.Background(), "family", p.QueryID, rec.hydrate)
	if err != nil {
		t.Fatal(err)
	}
	if out.Preparing == nil || out.Preparing.Detail.Revision != f.proof.Revision {
		t.Fatalf("pending adopted newer revision: %+v", out.Preparing)
	}

	close(gate)
	out, err = fileInfoWindowTransportSettle(t, f.runtime, "family", out, nil, rec.hydrate)
	if err != nil || out.Files == nil {
		t.Fatal("gated preparation never completed", err)
	}
	if rec.desc != desc {
		t.Fatalf("ready descriptor = %+v", rec.desc)
	}
	out.Release()
	if stats := f.store.Stats(); stats.Deliveries != 0 {
		t.Fatalf("ready delivery leaked: %+v", stats)
	}
}

func TestFileInfoWindowTransportAnchorExpiryIsActualLease(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 4)
	rec := &fileInfoWindowTransportRecorder{}
	out := fileInfoWindowTransportSubmit(t, f, "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate)
	out.Release()

	want := fileInfoWindowTransportBase().Add(2 * time.Minute)
	a := fileInfoWindowTransportDecodeQuery(t, f, "family", rec.queryID)
	if !a.Expires.Equal(want) {
		t.Fatalf("query anchor expiry = %s, want the actual lease %s (not the 30m opening)", a.Expires, want)
	}
	cursor, err := f.runtime.signFileInfoWindowAnchor("family", f.opening, rec.queryID, fileInfoOccurrence{Item: 11}, 1, fileInfoForward)
	if err != nil {
		t.Fatal(err)
	}
	c := fileInfoWindowTransportDecodeCursor(t, f, "family", f.opening, cursor)
	if !c.Expires.Equal(want) {
		t.Fatalf("cursor expiry = %s, want %s", c.Expires, want)
	}

	// The opening itself still has 30 minutes; only the private lease expires.
	f.advance(121 * time.Second)
	if _, _, err = f.runtime.opening(context.Background(), "family", f.opening); err != nil {
		t.Fatal("opening expired before its own capture", err)
	}
	if _, err = f.runtime.windowPoll(context.Background(), "family", rec.queryID, rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("expired lease served: %v", err)
	}
}

func TestFileInfoWindowTransportPrivateCallerAndTamper(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 4)
	rec := &fileInfoWindowTransportRecorder{}
	out := fileInfoWindowTransportSubmit(t, f, "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate)
	out.Release()

	if _, err := f.runtime.windowPoll(context.Background(), "other", rec.queryID, rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("foreign caller polled: %v", err)
	}
	cursor, err := f.runtime.signFileInfoWindowAnchor("family", f.opening, rec.queryID, fileInfoOccurrence{Item: 12}, 2, fileInfoForward)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.runtime.windowContinue(context.Background(), "other", f.opening, cursor, 0, false, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("foreign caller continued: %v", err)
	}
	if err = f.runtime.windowCancel(context.Background(), "other", rec.queryID); err != nil {
		t.Fatalf("foreign cancel = %v", err)
	}

	// A hidden foreign cancel did not retire the owner's handle.
	out, err = f.runtime.windowPoll(context.Background(), "family", rec.queryID, rec.hydrate)
	if err != nil || out.Files == nil {
		t.Fatal("foreign cancel released the owner's demand", err)
	}
	out.Release()

	// A cursor signed under another accepted opening never crosses over.
	ctx := WithFileInfoAuthority(context.Background(), f.guard.check)
	var otherOpening string
	err = dbwork.WithReadSnapshot(ctx, f.db, func(snapshot context.Context) error {
		var e error
		otherOpening, e = f.runtime.AcceptDetailOpening(snapshot, "family", f.guard.id, ContentRevision{Catalog: 7, Viewer: 8})
		return e
	})
	if err != nil || otherOpening == f.opening {
		t.Fatal("second opening not minted", err)
	}
	if _, err = f.runtime.windowContinue(context.Background(), "family", otherOpening, cursor, 0, false, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("cursor crossed openings: %v", err)
	}

	tampered := []byte(rec.queryID)
	if tampered[10] == 'A' {
		tampered[10] = 'B'
	} else {
		tampered[10] = 'A'
	}
	if _, err = f.runtime.windowPoll(context.Background(), "family", string(tampered), rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("tampered query id polled: %v", err)
	}
	if err = f.runtime.windowCancel(context.Background(), "family", string(tampered)); err != nil {
		t.Fatalf("tampered cancel = %v", err)
	}
	if _, err = f.runtime.windowContinue(context.Background(), "family", f.opening, string(tampered), 0, false, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("tampered cursor continued: %v", err)
	}
}

func TestFileInfoWindowTransportReauthorizesBeforeAndAfter(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 4)
	rec := &fileInfoWindowTransportRecorder{}
	out := fileInfoWindowTransportSubmit(t, f, "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate)
	queryID := rec.queryID
	out.Release()

	// The pre-response check rejects a lost audience and retires the handle.
	f.guard.deny = true
	if _, err := f.runtime.windowPoll(context.Background(), "family", queryID, rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("denied audience polled: %v", err)
	}
	f.guard.deny = false
	if _, err := f.runtime.windowPoll(context.Background(), "family", queryID, rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("denied poll left the private handle alive")
	}
	if stats := f.store.Stats(); stats.Deliveries != 0 {
		t.Fatalf("denied poll leaked a delivery: %+v", stats)
	}

	// A fresh lane proves the post-response check also retires the handle and
	// releases the ready delivery, even though hydration succeeded.
	desc2 := f.descriptor(fileInfoLaneSources, 4)
	rec2 := &fileInfoWindowTransportRecorder{}
	out = fileInfoWindowTransportSubmit(t, f, "family", desc2, fileInfoWindowTransportPlanFactory(0, nil, 256), rec2.hydrate)
	out.Release()
	f.guard.failAt = f.guard.calls + 2
	if _, err := f.runtime.windowPoll(context.Background(), "family", rec2.queryID, rec2.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("post-response reauth: %v", err)
	}
	if stats := f.store.Stats(); stats.Deliveries != 0 {
		t.Fatalf("failed reauth leaked a delivery: %+v", stats)
	}
	f.guard.failAt = 0
	if _, err := f.runtime.windowPoll(context.Background(), "family", rec2.queryID, rec2.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("post-response failure did not cancel the handle")
	}
}

func TestFileInfoWindowTransportContinueInheritsAnchorAndExpiry(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 3)
	first := &fileInfoWindowTransportRecorder{}
	out := fileInfoWindowTransportSubmit(t, f, "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), first.hydrate)
	out.Release()

	occurrence := fileInfoOccurrence{Item: 41}
	cursor, err := f.runtime.signFileInfoWindowAnchor("family", f.opening, first.queryID, occurrence, 5, fileInfoForward)
	if err != nil {
		t.Fatal(err)
	}
	second := &fileInfoWindowTransportRecorder{}
	out = fileInfoWindowTransportContinue(t, f, "family", f.opening, cursor, 0, false, fileInfoWindowTransportPlanFactory(0, nil, 256), second.hydrate)
	if second.queryID == first.queryID {
		t.Fatal("continuation reused the old query id")
	}
	want := desc
	want.Anchor, want.HasAnchor, want.Row, want.Direction = occurrence, true, 5, fileInfoForward
	if second.desc != want {
		t.Fatalf("continued descriptor = %+v, want %+v", second.desc, want)
	}
	if second.proof != f.proof {
		t.Fatal("continuation changed the original proof")
	}
	out.Release()

	// The new root anchor keeps the inherited lease expiry exactly.
	before := fileInfoWindowTransportDecodeQuery(t, f, "family", first.queryID)
	after := fileInfoWindowTransportDecodeQuery(t, f, "family", second.queryID)
	if !after.Expires.Equal(before.Expires) {
		t.Fatalf("continuation expiry %s != %s", after.Expires, before.Expires)
	}
	if after.Item != f.proof.Item || after.Asset != 0 || after.Row != 0 || after.Direction != fileInfoForward {
		t.Fatalf("continued root anchor = %+v", after)
	}

	// The explicit limit must match the original; omission inherits it.
	third := &fileInfoWindowTransportRecorder{}
	out = fileInfoWindowTransportContinue(t, f, "family", f.opening, cursor, desc.Limit, true, fileInfoWindowTransportPlanFactory(0, nil, 256), third.hydrate)
	if third.desc.Limit != desc.Limit || third.desc.Anchor != occurrence || third.desc.Row != 5 {
		t.Fatalf("explicit-limit continuation = %+v", third.desc)
	}
	out.Release()

	// The original lease is a hard ceiling: advancing past it stales both the
	// cursor and the query id even though the opening is still valid.
	f.advance(121 * time.Second)
	if _, err = f.runtime.windowContinue(context.Background(), "family", f.opening, cursor, 0, false, fileInfoWindowTransportPlanFactory(0, nil, 256), third.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("expired cursor continued: %v", err)
	}
	if _, err = f.runtime.windowPoll(context.Background(), "family", third.queryID, third.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("expired continuation polled: %v", err)
	}
}

func TestFileInfoWindowTransportContinueValidatesLimitsAndCoordinates(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 3)
	rec := &fileInfoWindowTransportRecorder{}
	out := fileInfoWindowTransportSubmit(t, f, "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate)
	out.Release()
	cursor, err := f.runtime.signFileInfoWindowAnchor("family", f.opening, rec.queryID, fileInfoOccurrence{Item: 41}, 1, fileInfoForward)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		limit   int
		present bool
	}{
		{0, true}, {101, true}, {4, true},
	} {
		if _, err = f.runtime.windowContinue(context.Background(), "family", f.opening, cursor, tc.limit, tc.present, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate); !errors.Is(err, readprep.ErrInvalid) {
			t.Fatalf("limit %d present=%v = %v", tc.limit, tc.present, err)
		}
	}
	for _, tc := range []struct {
		occurrence fileInfoOccurrence
		row        int64
		direction  byte
	}{
		{fileInfoOccurrence{Item: 0}, 1, fileInfoForward},
		{fileInfoOccurrence{Item: 41, Asset: -1}, 1, fileInfoForward},
		{fileInfoOccurrence{Item: 41}, -1, fileInfoForward},
		{fileInfoOccurrence{Item: 41}, 1, 0},
		{fileInfoOccurrence{Item: 41}, 1, 4},
	} {
		if _, err = f.runtime.signFileInfoWindowAnchor("family", f.opening, rec.queryID, tc.occurrence, tc.row, tc.direction); !errors.Is(err, readprep.ErrInvalid) {
			t.Fatalf("coordinate %+v row=%d dir=%d = %v", tc.occurrence, tc.row, tc.direction, err)
		}
	}

	// Exact-occurrence lanes must carry their child asset in the root anchor and
	// in every cursor.
	audio := f.descriptor(fileInfoLaneAudio, 3)
	audioRec := &fileInfoWindowTransportRecorder{}
	out = fileInfoWindowTransportSubmit(t, f, "family", audio, fileInfoWindowTransportPlanFactory(0, nil, 256), audioRec.hydrate)
	out.Release()
	a := fileInfoWindowTransportDecodeQuery(t, f, "family", audioRec.queryID)
	if a.Asset != 42 {
		t.Fatalf("audio root asset = %d", a.Asset)
	}
	if _, err = f.runtime.signFileInfoWindowAnchor("family", f.opening, audioRec.queryID, fileInfoOccurrence{Item: 50}, 1, fileInfoForward); !errors.Is(err, readprep.ErrInvalid) {
		t.Fatalf("exact lane cursor without asset = %v", err)
	}
}

func TestFileInfoWindowTransportCancelIsIdempotentAndHidden(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 3)
	rec := &fileInfoWindowTransportRecorder{}
	out := fileInfoWindowTransportSubmit(t, f, "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate)
	out.Release()

	if err := f.runtime.windowCancel(context.Background(), "family", rec.queryID); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.windowCancel(context.Background(), "family", rec.queryID); err != nil {
		t.Fatalf("second cancel = %v", err)
	}
	if err := f.runtime.windowCancel(context.Background(), "family", "garbage"); err != nil {
		t.Fatalf("unknown cancel = %v", err)
	}
	if _, err := f.runtime.windowPoll(context.Background(), "family", rec.queryID, rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("cancelled handle polled: %v", err)
	}
}

func TestFileInfoWindowTransportPreservesCapacityAndStale(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 4096})
	desc := f.descriptor(fileInfoLaneFiles, 3)
	// An oversized admission is rejected by the shared store's ordinary cap and
	// the raw sentinel reaches the HTTP mapper unchanged.
	if _, err := f.runtime.windowSubmit(context.Background(), "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 1<<20), (&fileInfoWindowTransportRecorder{}).hydrate, time.Time{}); !errors.Is(err, readprep.ErrCapacity) {
		t.Fatalf("capacity = %v", err)
	}
	// Invalid descriptors never reach the authority or the store.
	before := f.guard.calls
	for _, limit := range []int{0, 101} {
		bad := f.descriptor(fileInfoLaneFiles, limit)
		if _, err := f.runtime.windowSubmit(context.Background(), "family", bad, fileInfoWindowTransportPlanFactory(0, nil, 256), (&fileInfoWindowTransportRecorder{}).hydrate, time.Time{}); !errors.Is(err, readprep.ErrInvalid) {
			t.Fatalf("limit %d = %v", limit, err)
		}
	}
	wrongWitness := f.descriptor(fileInfoLaneFiles, 3)
	wrongWitness.Witness.Owner = false
	if _, err := f.runtime.windowSubmit(context.Background(), "family", wrongWitness, fileInfoWindowTransportPlanFactory(0, nil, 256), (&fileInfoWindowTransportRecorder{}).hydrate, time.Time{}); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("changed witness = %v", err)
	}
	if f.guard.calls != before+1 {
		t.Fatalf("invalid descriptors consulted authority: %d -> %d", before, f.guard.calls)
	}

	// An expired original opening is stale even for a small store.
	f.advance(31 * time.Minute)
	if _, err := f.runtime.windowSubmit(context.Background(), "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), (&fileInfoWindowTransportRecorder{}).hydrate, time.Time{}); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("expired opening = %v", err)
	}
}

func TestFileInfoWindowTransportCanonicalKeyRejectsMismatch(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 3)
	key, err := fileInfoWindowKey("family", desc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.runtime.windowDescriptorFromKey("family", f.opening, fileInfoLaneFiles, key)
	if err != nil || got != desc {
		t.Fatalf("canonical descriptor = %+v, %v", got, err)
	}
	for _, tc := range []struct {
		caller  readprep.Caller
		opening string
		lane    byte
		key     readprep.Key
	}{
		{"other", f.opening, fileInfoLaneFiles, key},
		{"family", f.opening, fileInfoLaneAudio, key},
		{"family", "other-opening", fileInfoLaneFiles, key},
		{"family", f.opening, fileInfoLaneFiles, readprep.Key{Namespace: "elsewhere", Basis: key.Basis, Canonical: key.Canonical}},
		{"family", f.opening, fileInfoLaneFiles, readprep.Key{Namespace: key.Namespace, Basis: "other-basis", Canonical: key.Canonical}},
		{"family", f.opening, fileInfoLaneFiles, readprep.Key{Namespace: key.Namespace, Basis: key.Basis, Canonical: " " + key.Canonical}},
	} {
		if _, err = f.runtime.windowDescriptorFromKey(tc.caller, tc.opening, tc.lane, tc.key); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("mismatched key accepted: %+v", tc)
		}
	}
}

func TestFileInfoWindowTransportQueryIDIsFixedWidth(t *testing.T) {
	valid := strings.Repeat("a", fileInfoOpeningTokenSize) + "." + strings.Repeat("b", fileInfoAnchorTokenSize)
	opening, token, err := fileInfoWindowSplitQueryID(valid)
	if err != nil || opening != strings.Repeat("a", fileInfoOpeningTokenSize) || token != strings.Repeat("b", fileInfoAnchorTokenSize) {
		t.Fatalf("valid split = %q, %q, %v", opening, token, err)
	}
	for _, queryID := range []string{
		"",
		"x",
		strings.Repeat("a", fileInfoQueryIDSize-1),
		strings.Repeat("a", fileInfoQueryIDSize),
		strings.Repeat("a", fileInfoQueryIDSize+1),
		strings.Repeat("a", 1<<20),
		strings.Repeat("a", fileInfoOpeningTokenSize) + "-" + strings.Repeat("b", fileInfoAnchorTokenSize),
	} {
		if _, _, err = fileInfoWindowSplitQueryID(queryID); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("query id of length %d accepted: %v", len(queryID), err)
		}
	}
}

func TestFileInfoWindowTransportHydrateFailureReleases(t *testing.T) {
	f := newFileInfoWindowTransportFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	desc := f.descriptor(fileInfoLaneFiles, 3)
	rec := &fileInfoWindowTransportRecorder{}
	out := fileInfoWindowTransportSubmit(t, f, "family", desc, fileInfoWindowTransportPlanFactory(0, nil, 256), rec.hydrate)
	out.Release()

	rec.fail = true
	if _, err := f.runtime.windowPoll(context.Background(), "family", rec.queryID, rec.hydrate); !errors.Is(err, readprep.ErrInvalid) {
		t.Fatalf("hydrate failure = %v", err)
	}
	if stats := f.store.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
		t.Fatalf("hydrate failure leaked delivery: %+v", stats)
	}
	rec.fail = false
	if _, err := f.runtime.windowPoll(context.Background(), "family", rec.queryID, rec.hydrate); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("hydrate failure left the private handle alive")
	}
}
