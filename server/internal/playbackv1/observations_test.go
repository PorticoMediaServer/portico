package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// observationClock is the service's wall clock in these tests; the batch
// worker reads it, so nothing is written until a test moves it.
type observationClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *observationClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *observationClock) add(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// provedLog records what the observation applies hand the evidence path.
type provedLog struct {
	mu        sync.Mutex
	watched   int64
	calls     int
	positions []float64
}

func (l *provedLog) record(_ *sql.Tx, _ identity.Viewer, _, _ string, _ int64, position, _ float64, _ string, _ bool, _ float64, _ *bool, _ int, _ bool, _ time.Time, proved *catalog.PlayedTime) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.positions = append(l.positions, position)
	if proved != nil {
		l.watched += proved.WatchedMs
	}
	return nil
}

func (l *provedLog) total() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.watched
}

type observationFixture struct {
	*carryFixture
	clock      *observationClock
	proved     *provedLog
	session    SessionView
	generation int
}

func newObservationFixture(t *testing.T) *observationFixture {
	t.Helper()
	f := newCarryFixture(t, false)
	clock := &observationClock{at: time.Now()}
	proved := &provedLog{}
	f.service.Playback.PersonalProgress = proved.record
	f.service.Now = clock.now
	q := f.queue()
	t.Cleanup(func() {
		// Let the worker exit with the test.
		if o := f.service.obs; o != nil {
			o.mu.Lock()
			o.entries = map[string]*observer{}
			o.wakeLocked()
			o.mu.Unlock()
		}
	})
	of := &observationFixture{carryFixture: f, clock: clock, proved: proved, session: *q.Session}
	if err := f.catalog.DB.QueryRow(`SELECT generation FROM playback_v1_sessions WHERE id=?`, q.Session.ID).Scan(&of.generation); err != nil {
		t.Fatal(err)
	}
	return of
}

func (f *observationFixture) claim(id string, replaces string) string {
	f.t.Helper()
	grant, err := f.service.ClaimObserver(context.Background(), f.caller, id, ObserverClaim{Generation: f.generation, Replaces: replaces})
	if err != nil {
		f.t.Fatal(err)
	}
	return grant.Epoch
}

// tick sends one healthy 1 Hz sample: the device has played every second.
func (f *observationFixture) tick(id, epoch string, seq int64, state string) (ObservationAck, error) {
	return f.service.Observe(context.Background(), f.caller, id, Observation{
		Epoch: epoch, Seq: seq, Generation: f.generation, State: state,
		PositionMs: 60_000 + seq*1000, MonotonicMs: seq * 1000, PlayClockMs: seq * 1000, Rate: 1,
	})
}

// waitApplied waits until the worker has written every listed session.
func (f *observationFixture) waitApplied(ids ...string) {
	f.t.Helper()
	o := f.service.observations()
	deadline := time.Now().Add(10 * time.Second)
	for {
		o.mu.Lock()
		o.wakeLocked()
		pending := 0
		for _, id := range ids {
			if e := o.entries[id]; e != nil && (e.dirty || e.busy || e.credited() != e.applied) {
				pending++
			}
		}
		o.mu.Unlock()
		if pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("%d of %d sessions still unwritten", pending, len(ids))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// cloneSessions adds n live sessions on the same device, copies of the
// fixture's, so many owners can tick at once.
func (f *observationFixture) cloneSessions(n int) []string {
	f.t.Helper()
	ids := []string{f.session.ID}
	for i := 1; i < n; i++ {
		id := fmt.Sprintf("%s-copy-%03d", f.session.ID, i)
		f.catalog.Exec(`CREATE TEMP TABLE IF NOT EXISTS observation_clone AS SELECT * FROM playback_v1_sessions WHERE 0`)
		f.catalog.Exec(`DELETE FROM observation_clone`)
		f.catalog.Exec(`INSERT INTO observation_clone SELECT * FROM playback_v1_sessions WHERE id=?`, f.session.ID)
		f.catalog.Exec(`UPDATE observation_clone SET id=?,start_key=?`, id, fmt.Sprintf("observation-clone-key-%04d", i))
		f.catalog.Exec(`INSERT INTO playback_v1_sessions SELECT * FROM observation_clone`)
		ids = append(ids, id)
	}
	return ids
}

func establishedPlaybackTickets() uint64 {
	return dbwork.WriteGate().Stats().ByClass[dbwork.ClassEstablishedPlayback.String()].Acquired
}

// TestObservationTicksTakeNoWriterTicket is the gate's first required test:
// 100 VOD sessions ticking at 1 Hz take no writer ticket per tick, and their
// batch is ceil(100/16) writer transactions.
func TestObservationTicksTakeNoWriterTicket(t *testing.T) {
	f := newObservationFixture(t)
	ids := f.cloneSessions(100)
	epochs := make([]string, len(ids))
	for i, id := range ids {
		epochs[i] = f.claim(id, "")
	}
	before := establishedPlaybackTickets()
	var wg sync.WaitGroup
	errs := make(chan error, len(ids))
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for seq := int64(1); seq <= 10; seq++ {
				if _, err := f.tick(ids[i], epochs[i], seq, "playing"); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := establishedPlaybackTickets() - before; got != 0 {
		t.Fatalf("1000 ticks took %d writer tickets, want 0", got)
	}
	stats := f.service.ObservationStats()
	if stats.Accepted != 1000 || stats.Coalesced != 900 {
		t.Fatalf("accepted %d, coalesced %d; want 1000 and 900", stats.Accepted, stats.Coalesced)
	}

	f.clock.add(observationFlush + time.Second)
	f.waitApplied(ids...)
	if got := establishedPlaybackTickets() - before; got != 7 {
		t.Fatalf("100 sessions were written in %d writer transactions, want 7 (16 a batch)", got)
	}
	var written int
	if err := f.catalog.DB.QueryRow(`SELECT count(*) FROM playback_v1_sessions WHERE id IN (SELECT id FROM playback_v1_sessions WHERE ended_ms=0) AND position_ms=?`, 70_000).Scan(&written); err != nil {
		t.Fatal(err)
	}
	if written != 100 {
		t.Fatalf("%d sessions hold the latest position, want 100", written)
	}
}

// TestObservationRestartRecoversThroughOneClaim is the gate's second required
// test: after a restart the old epoch gets observation_epoch_changed, one
// claim that names it recovers, and only the unwritten window is lost.
func TestObservationRestartRecoversThroughOneClaim(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	for seq := int64(1); seq <= 15; seq++ {
		f.clock.add(time.Second)
		if _, err := f.tick(id, epoch, seq, "playing"); err != nil {
			t.Fatal(err)
		}
	}
	f.clock.add(observationFlush)
	f.waitApplied(id)
	if got := f.proved.total(); got != 15_000 {
		t.Fatalf("written watched time is %d ms, want 15000", got)
	}
	// Five more seconds reach memory but not the database before the crash.
	for seq := int64(16); seq <= 20; seq++ {
		f.clock.add(time.Second)
		if _, err := f.tick(id, epoch, seq, "playing"); err != nil {
			t.Fatal(err)
		}
	}

	// Restart: every in-memory owner is gone.
	old := f.service.obs
	old.mu.Lock()
	old.entries = map[string]*observer{}
	old.wakeLocked()
	old.mu.Unlock()
	f.service.obs, f.service.obsOnce = nil, sync.Once{}

	if _, err := f.tick(id, epoch, 21, "playing"); !errors.Is(err, ErrObservationEpoch) {
		t.Fatalf("old epoch after restart: %v, want ErrObservationEpoch", err)
	}
	fresh := f.claim(id, epoch)
	if fresh == epoch {
		t.Fatal("restart reissued the old epoch")
	}
	for seq := int64(1); seq <= 10; seq++ {
		f.clock.add(time.Second)
		if _, err := f.tick(id, fresh, seq, "playing"); err != nil {
			t.Fatal(err)
		}
	}
	f.clock.add(observationFlush)
	f.waitApplied(id)
	stats := f.service.ObservationStats()
	if stats.Claims != 1 || stats.WrongEpoch != 1 {
		t.Fatalf("recovery took %d claims after %d refusals, want 1 and 1", stats.Claims, stats.WrongEpoch)
	}
	if got, lost := f.proved.total(), int64(5_000); got != 25_000 || lost > observationFlush.Milliseconds() {
		t.Fatalf("watched time is %d ms, want 25000 (only the unwritten %d ms lost)", got, lost)
	}
}

// TestStopRacingABatchDrainsExactlyOnce is the gate's third required test: a
// Stop and a due batch race for the writer; whichever wins, the credit is
// written once and the latest position is the final one.
func TestStopRacingABatchDrainsExactlyOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for round := 0; round < 4; round++ {
		t.Run(fmt.Sprint(round), func(t *testing.T) {
			f := newObservationFixture(t)
			id := f.session.ID
			epoch := f.claim(id, "")
			for seq := int64(1); seq <= 12; seq++ {
				f.clock.add(time.Second)
				if _, err := f.tick(id, epoch, seq, "playing"); err != nil {
					t.Fatal(err)
				}
			}
			f.clock.add(observationFlush)
			stopped := make(chan error, 1)
			go func() { stopped <- f.service.Stop(context.Background(), f.caller, id, nil) }()
			o := f.service.observations()
			o.mu.Lock()
			o.wakeLocked()
			o.mu.Unlock()
			if err := <-stopped; err != nil {
				t.Fatal(err)
			}
			if got := f.proved.total(); got != 12_000 {
				t.Fatalf("watched time is %d ms, want exactly 12000", got)
			}
			var position, ended int64
			var reason string
			if err := f.catalog.DB.QueryRow(`SELECT position_ms,ended_ms,end_reason FROM playback_v1_sessions WHERE id=?`, id).Scan(&position, &ended, &reason); err != nil {
				t.Fatal(err)
			}
			if position != 72_000 || ended == 0 || reason != "stopped" {
				t.Fatalf("stopped at %d (ended %d, %q), want 72000 and stopped", position, ended, reason)
			}
			o.mu.Lock()
			_, still := o.entries[id]
			o.mu.Unlock()
			if still {
				t.Fatal("the ended session's owner is still in memory")
			}
		})
	}
}

// A controller's Stop position is not the renderer's: the drained sample wins.
func TestControllerStopPositionDoesNotOverrideTheRenderer(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	controller := f.caller
	controller.DeviceID = "another-device"
	claimed := int64(5_000)
	if err := f.service.Stop(context.Background(), controller, id, &claimed); err != nil {
		t.Fatal(err)
	}
	var position int64
	if err := f.catalog.DB.QueryRow(`SELECT position_ms FROM playback_v1_sessions WHERE id=?`, id).Scan(&position); err != nil {
		t.Fatal(err)
	}
	if position != 61_000 {
		t.Fatalf("stopped at %d, want the renderer's 61000", position)
	}
}

// A sweep that chose a session before it was heard from again ends nothing
// (CS-06): the drained sample renews the lease inside the end's transaction.
func TestLeaseSweepSparesASessionHeardSinceItWasChosen(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	// The durable lease lapsed (a stalled writer), but the device is live.
	f.catalog.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=?`, f.clock.now().UnixMilli()-1, id)
	f.service.ExpireLapsed(context.Background())
	var ended, lease int64
	if err := f.catalog.DB.QueryRow(`SELECT ended_ms,lease_expires_ms FROM playback_v1_sessions WHERE id=?`, id).Scan(&ended, &lease); err != nil {
		t.Fatal(err)
	}
	if ended != 0 || lease <= f.clock.now().UnixMilli() {
		t.Fatalf("a live session was swept: ended %d, lease %d", ended, lease)
	}
}

// The watched-time ceiling is cumulative (LOCAL P3): time a late packet could
// not prove is credited by the next, and nothing beyond the server's own
// elapsed time (plus the claim reply's carry) is ever credited.
func TestWatchedTimeCeilingCarriesJitterAndNeverOutrunsTheServer(t *testing.T) {
	o := &observations{entries: map[string]*observer{}, kick: make(chan struct{}, 1)}
	start := time.Unix(1_000_000, 0)
	e := &observer{baseAt: start}
	// The device samples every second; packets arrive 0 or 900 ms late in
	// turn. Per-pair clipping would keep losing the late half.
	for i := int64(1); i <= 20; i++ {
		late := time.Duration(i%2) * 900 * time.Millisecond
		o.acceptLocked(e, Observation{Seq: i, State: "playing", MonotonicMs: i * 1000, PlayClockMs: i * 1000}, start.Add(time.Duration(i)*time.Second+late))
	}
	if e.credited() != 20_000 {
		t.Fatalf("credited %d ms of 20000 played under jitter", e.credited())
	}

	// A device claiming a minute of play ten seconds after its claim.
	liar := &observer{baseAt: start}
	o.acceptLocked(liar, Observation{Seq: 1, State: "playing", MonotonicMs: 60_000, PlayClockMs: 60_000}, start.Add(10*time.Second))
	if liar.credited() != 12_000 {
		t.Fatalf("credited %d ms ten seconds after the claim, want 12000", liar.credited())
	}

	// Clocks going backwards keep what was earned and start a new basis.
	o.acceptLocked(e, Observation{Seq: 21, State: "playing", MonotonicMs: 500, PlayClockMs: 500}, start.Add(22*time.Second))
	o.acceptLocked(e, Observation{Seq: 22, State: "playing", MonotonicMs: 2500, PlayClockMs: 2500}, start.Add(24*time.Second))
	if e.credited() != 22_000 || o.counts.rebased.Load() != 1 {
		t.Fatalf("after a clock restart credited %d (rebased %d), want 22000 and 1", e.credited(), o.counts.rebased.Load())
	}

	// Pauses are not credited: play clock stands still while time passes.
	paused := &observer{baseAt: start}
	o.acceptLocked(paused, Observation{Seq: 1, State: "playing", MonotonicMs: 5000, PlayClockMs: 5000}, start.Add(5*time.Second))
	o.acceptLocked(paused, Observation{Seq: 2, State: "paused", MonotonicMs: 65_000, PlayClockMs: 5000}, start.Add(65*time.Second))
	if paused.credited() != 5000 {
		t.Fatalf("a minute's pause credited %d ms, want 5000", paused.credited())
	}
}

// A second process on the same device cannot take a session whose owner is
// reporting, but the owner's handover (naming its epoch) can, and the
// unwritten credit carries over.
func TestObserverHandoverCarriesCreditAndRefusesASteal(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	first := f.claim(id, "")
	for seq := int64(1); seq <= 3; seq++ {
		f.clock.add(time.Second)
		if _, err := f.tick(id, first, seq, "playing"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.ClaimObserver(context.Background(), f.caller, id, ObserverClaim{Generation: f.generation}); !errors.Is(err, ErrObservationOwned) {
		t.Fatalf("steal: %v, want ErrObservationOwned", err)
	}
	second := f.claim(id, first)
	if _, err := f.tick(id, first, 4, "playing"); !errors.Is(err, ErrObservationEpoch) {
		t.Fatalf("old owner after handover: %v, want ErrObservationEpoch", err)
	}
	for seq := int64(1); seq <= 2; seq++ {
		f.clock.add(time.Second)
		if _, err := f.tick(id, second, seq, "playing"); err != nil {
			t.Fatal(err)
		}
	}
	f.clock.add(observationFlush)
	f.waitApplied(id)
	if got := f.proved.total(); got != 5000 {
		t.Fatalf("watched time across the handover is %d ms, want 5000", got)
	}
}

// A grant the server rotated on its own reaches the session playing it in the
// same transaction (V10): new media generation, new session generation and URL.
func TestServerReplacedGrantIsPublishedToTheSession(t *testing.T) {
	f := newCarryFixture(t, false)
	session := *f.queue().Session
	before, err := loadRow(context.Background(), f.catalog.DB, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.service.Playback.GrantReplacedTx == nil {
		t.Fatal("the playback service has no replacement hook")
	}
	url := "/v1/media/replaced-grant/master.m3u8"
	if err = dbwork.WithWriteTxContext(context.Background(), f.catalog.DB, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
		return f.service.Playback.GrantReplacedTx(ctx, tx, before.media, before.mediaGeneration+1, url)
	}); err != nil {
		t.Fatal(err)
	}
	after, err := f.service.Get(context.Background(), f.caller, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := loadRow(context.Background(), f.catalog.DB, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.mediaGeneration != before.mediaGeneration+1 || row.generation != before.generation+1 || row.revision != before.revision+1 || after.Presentation.URL != url || after.Presentation.Generation != before.generation+1 {
		t.Fatalf("session after replacement: media %d gen %d rev %d url %q; before media %d gen %d rev %d", row.mediaGeneration, row.generation, row.revision, after.Presentation.URL, before.mediaGeneration, before.generation, before.revision)
	}
}

// Buffering is not an edge: it changes nothing written, so a flapping network
// never forces early writes. A pause after it is one.
func TestBufferingIsNotAnEarlyWriteEdge(t *testing.T) {
	o := &observations{entries: map[string]*observer{}, kick: make(chan struct{}, 1)}
	start := time.Unix(1_000_000, 0)
	e := &observer{baseAt: start}
	for i, state := range []string{"playing", "buffering", "playing", "buffering", "paused"} {
		e.edge = false
		seq := int64(i + 1)
		o.acceptLocked(e, Observation{Seq: seq, State: state, MonotonicMs: seq * 1000, PlayClockMs: seq * 500}, start.Add(time.Duration(seq)*time.Second))
		if want := i == 0 || state == "paused"; e.edge != want {
			t.Fatalf("sample %d (%s): edge %v, want %v", seq, state, e.edge, want)
		}
	}
}

// The control revision moves only on an accepted pause, play or seek (RN-09):
// the player's own reports change the session's state but never the command.
func TestControlRevisionMovesOnlyOnCommands(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	view, err := f.service.Get(context.Background(), f.caller, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.Control == nil || view.Control.Revision != "1" || view.Control.State != "playing" {
		t.Fatalf("started control %+v, want revision 1 playing", view.Control)
	}
	paused, err := f.service.Patch(context.Background(), f.caller, id, view.Revision, Change{State: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	if paused.Control.Revision != "2" || paused.Control.State != "paused" {
		t.Fatalf("after pause %+v, want revision 2 paused", paused.Control)
	}
	// The renderer reports playing (a late own report): state moves, control does not.
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err = f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	f.clock.add(observationFlush)
	f.waitApplied(id)
	after, err := f.service.Get(context.Background(), f.caller, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Control.Revision != "2" || after.Control.State != "paused" {
		t.Fatalf("an observation moved the command: %+v", after.Control)
	}
	sought, err := f.service.Patch(context.Background(), f.caller, id, after.Revision, Change{Seek: &SeekRequest{PositionMs: 5000, ID: "seek-one"}})
	if err != nil {
		t.Fatal(err)
	}
	if sought.Control.Revision != "3" || sought.Presentation.SeekID != "seek-one" {
		t.Fatalf("after seek control %+v seek %q", sought.Control, sought.Presentation.SeekID)
	}
}

// A client that does not declare the protocol is refused before any session
// exists.
func TestStartRefusesAClientWithoutTheObservationProtocol(t *testing.T) {
	f := newCarryFixture(t, false)
	f.caller.PlaybackProtocol = 0
	_, err := f.service.CreateQueue(context.Background(), f.caller, "carry-create-queue-0001", CreateQueueRequest{Segments: []SegmentInput{{Source: Selector{Items: &ItemList{IDs: []string{f.items[0].Public}}}}}, StartPlayback: &StartPlayback{State: "playing"}})
	if !errors.Is(err, ErrClientUpdateRequired) {
		t.Fatalf("old client start: %v, want ErrClientUpdateRequired", err)
	}
	var sessions int
	if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM playback_v1_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("%d sessions after a refused start (%v)", sessions, err)
	}
	f.caller.PlaybackProtocol = PlaybackProtocol
	if q := f.queue(); q.Session == nil {
		t.Fatal("a current client could not start")
	}
}

// Review fixes (Sol, 28 Oct).

// A title the profile may no longer play refuses its pending evidence, but
// never blocks the end it was drained into.
func TestRefusedEvidenceNeverBlocksAnEnd(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	f.service.Playback.PersonalProgress = func(*sql.Tx, identity.Viewer, string, string, int64, float64, float64, string, bool, float64, *bool, int, bool, time.Time, *catalog.PlayedTime) error {
		return identity.ErrContentRestricted
	}
	if err := f.service.End(context.Background(), id, "terminated", ""); err != nil {
		t.Fatalf("end with refused evidence: %v", err)
	}
	var ended int64
	if err := f.catalog.DB.QueryRow(`SELECT ended_ms FROM playback_v1_sessions WHERE id=?`, id).Scan(&ended); err != nil || ended == 0 {
		t.Fatalf("not ended (%v)", err)
	}
	o := f.service.observations()
	o.mu.Lock()
	_, still := o.entries[id]
	o.mu.Unlock()
	if still {
		t.Fatal("the ended session's owner is still in memory")
	}
}

// Reading a session whose durable lease lapsed, while its device is still
// heard, writes the sample and keeps it playing (as the sweep does).
func TestReadingALapsedLeaseSparesALiveSession(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	f.catalog.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=?`, f.clock.now().UnixMilli()-1, id)
	view, err := f.service.Get(context.Background(), f.caller, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == "ended" {
		t.Fatalf("a live session was ended by a read: %+v", view.End)
	}
}

// A repeated sample is liveness: an expiry drained before the batch renewal
// still sees it.
func TestLivenessAloneSparesASessionFromExpiry(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	f.clock.add(observationFlush)
	f.waitApplied(id)
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil { // a repeat: no new state
		t.Fatal(err)
	}
	f.catalog.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=?`, f.clock.now().UnixMilli()-1, id)
	f.service.ExpireLapsed(context.Background())
	var ended int64
	if err := f.catalog.DB.QueryRow(`SELECT ended_ms FROM playback_v1_sessions WHERE id=?`, id).Scan(&ended); err != nil || ended != 0 {
		t.Fatalf("a heard session expired: ended %d (%v)", ended, err)
	}
}

// The server replacing a media grant writes the old generation's evidence
// before the generation moves, so no credit is consumed unwritten.
func TestGrantReplacementWritesEvidenceUnderTheOldGeneration(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	r, err := loadRow(context.Background(), f.catalog.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = dbwork.WithWriteTxContext(context.Background(), f.catalog.DB, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
		if err := f.service.Playback.GrantReplacingTx(ctx, tx, r.media); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE playback_sessions SET generation=generation+1 WHERE id=?`, r.media); err != nil {
			return err
		}
		return f.service.Playback.GrantReplacedTx(ctx, tx, r.media, r.mediaGeneration+1, "/v1/media/replaced/master.m3u8")
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.proved.total(); got != 1000 {
		t.Fatalf("written watched time %d ms, want 1000", got)
	}
}

// StopGeneration writes the pending sample while the media still accepts it.
func TestStopGenerationWritesThePendingCredit(t *testing.T) {
	f := newObservationFixture(t)
	f.service.StopAuthorityTx = func(context.Context, *sql.Tx, Caller) error { return nil }
	f.caller.AuthorizeItem = func(context.Context, string) error { return nil }
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(time.Second)
	if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.StopGeneration(context.Background(), f.caller, id, f.generation); err != nil {
		t.Fatal(err)
	}
	if got := f.proved.total(); got != 1000 {
		t.Fatalf("written watched time %d ms, want 1000", got)
	}
}

// Clock restarts cannot earn the claim reply's carry again: all credit stays
// within the server's time since the record was claimed, plus one carry.
func TestRepeatedClockRestartsNeverOutrunTheServer(t *testing.T) {
	o := &observations{entries: map[string]*observer{}, kick: make(chan struct{}, 1)}
	start := time.Unix(1_000_000, 0)
	e := &observer{baseAt: start, chainAt: start}
	at := start
	for i := int64(1); i <= 10; i++ {
		at = at.Add(time.Second)
		// The device claims a big jump, then restarts its clocks, over and over.
		o.acceptLocked(e, Observation{Seq: 2*i - 1, State: "playing", MonotonicMs: 60_000, PlayClockMs: 60_000}, at)
		o.acceptLocked(e, Observation{Seq: 2 * i, State: "playing", MonotonicMs: 0, PlayClockMs: 0}, at)
	}
	if limit := at.Sub(start).Milliseconds() + observationCarry.Milliseconds(); e.credited() > limit {
		t.Fatalf("credited %d ms after %d ms of server time", e.credited(), at.Sub(start).Milliseconds())
	}
}
