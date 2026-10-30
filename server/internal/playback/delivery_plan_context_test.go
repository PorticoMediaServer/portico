package playback

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// One plan load must carry its caller's context through every read (plan, pin
// existence, prepared version and conversion target). With the pool genuinely
// exhausted by a held connection, a cancelled caller must return at once instead
// of waiting for a background-context query to get a slot.

const planContextBound = 2 * time.Second

func openPlanContextFixture(t *testing.T) (*HLS, string) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, item, _ := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "item", "Film", compactcatalog.Asset{Path: "/test/file.mp4", Size: 20, ModifiedNS: 30, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 100})
	principal := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, principal.AccountID, principal.ProfileID)
	session, err := New(db).Create(principal, item, "automatic", "request")
	if err != nil {
		t.Fatal(err)
	}
	return &HLS{db: db, finite: &FiniteHLS{}}, session.ID
}

// exhaustPlanPool leaves the only pool slot in the caller's hand. The caller
// closes it (also registered here) before the test ends, so no goroutine
// outlives the test or blocks db.Close.
func exhaustPlanPool(t *testing.T, h *HLS) *sql.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), planContextBound)
	defer cancel()
	h.db.SetMaxOpenConns(1)
	held, err := h.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	return held
}

// boundedCall is one lookup running in its own goroutine. Cleanup is registered
// before the goroutine starts: it cancels, releases the held slot and joins on a
// bounded channel, so an early Fatal cannot leak the goroutine or block db.Close.
type boundedCall struct {
	done   chan error
	joined chan struct{}
}

func startBounded(t *testing.T, h *HLS, held *sql.Conn, cancel context.CancelFunc, run func() error) *boundedCall {
	t.Helper()
	c := &boundedCall{done: make(chan error, 1), joined: make(chan struct{})}
	t.Cleanup(func() {
		if cancel != nil {
			cancel()
		}
		if held != nil {
			_ = held.Close()
		}
		select {
		case <-c.joined:
		case <-time.After(planContextBound):
			t.Errorf("lookup did not return within %s of cleanup", planContextBound)
		}
	})
	go func() {
		err := run()
		c.done <- err
		close(c.joined)
	}()
	return c
}

func (c *boundedCall) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-c.done:
		return err
	case <-time.After(planContextBound):
		t.Fatalf("lookup still waiting after %s; a cancelled caller must not wait for a pool slot", planContextBound)
		return nil
	}
}

// awaitPoolWait waits boundedly for the running lookup to actually reach the
// held connection, starting from a captured baseline so earlier waits on the
// same pool are not credited to this call. The goroutine startup itself races,
// so this polls instead of sampling once.
func awaitPoolWait(t *testing.T, h *HLS, baseline int64) {
	t.Helper()
	deadline := time.Now().Add(planContextBound)
	for time.Now().Before(deadline) {
		if h.db.Stats().WaitCount > baseline {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("lookup did not reach the held pool slot (waits stayed at %d)", baseline)
}

func TestPlanLoadCanceledContextDoesNotWaitForPoolSlot(t *testing.T) {
	h, id := openPlanContextFixture(t)
	held := exhaustPlanPool(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	call := startBounded(t, h, held, cancel, func() error { _, err := loadDeliveryPlan(ctx, h.db, id); return err })
	if err := call.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled plan load returned %v, want context.Canceled", err)
	}
}

func TestHLSReadyCanceledContextDoesNotWaitForPoolSlot(t *testing.T) {
	h, id := openPlanContextFixture(t)
	held := exhaustPlanPool(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	call := startBounded(t, h, held, cancel, func() error { return h.Ready(ctx, id) })
	if err := call.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Ready returned %v, want context.Canceled", err)
	}
}

// FileContext's own grant lookup is already context-aware, so this is an honest
// entry-cancellation control only: it shows the request boundary cancels before
// the plan read. It does not by itself prove the changed plan seam.
func TestHLSFileContextCanceledEntryControl(t *testing.T) {
	h, _ := openPlanContextFixture(t)
	held := exhaustPlanPool(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	call := startBounded(t, h, held, cancel, func() error { _, err := h.FileContext(ctx, "grant", "master.m3u8"); return err })
	if err := call.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled FileContext returned %v, want context.Canceled", err)
	}
}

func TestPlanLoadLiveReadAfterPoolRelease(t *testing.T) {
	h, id := openPlanContextFixture(t)
	held := exhaustPlanPool(t, h)
	baseline := h.db.Stats().WaitCount
	ctx, cancel := context.WithTimeout(context.Background(), planContextBound)
	defer cancel()
	var plan *DeliveryPlan
	var lookupErr error
	call := startBounded(t, h, held, cancel, func() error { plan, lookupErr = loadDeliveryPlan(ctx, h.db, id); return nil })
	awaitPoolWait(t, h, baseline)
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	_ = call.wait(t)
	if lookupErr != nil || plan == nil || plan.SourceSize != 20 {
		t.Fatalf("live read after release: %+v %v", plan, lookupErr)
	}
}

func TestPlanLoadProducerCancellationUnblocksLookup(t *testing.T) {
	h, id := openPlanContextFixture(t)
	held := exhaustPlanPool(t, h)
	baseline := h.db.Stats().WaitCount
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	call := startBounded(t, h, held, cancel, func() error { _, err := loadDeliveryPlan(ctx, h.db, id); return err })
	awaitPoolWait(t, h, baseline)
	cancel()
	if err := call.wait(t); !errors.Is(err, context.Canceled) {
		t.Fatalf("producer cancellation returned %v, want context.Canceled", err)
	}
}

func TestPlanLoadMissingPlanStaysNil(t *testing.T) {
	h, _ := openPlanContextFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), planContextBound)
	defer cancel()
	plan, err := loadDeliveryPlan(ctx, h.db, "no-such-session")
	if err != nil || plan != nil {
		t.Fatalf("missing plan = %+v, %v; want nil, nil", plan, err)
	}
}
