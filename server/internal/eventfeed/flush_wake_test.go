package eventfeed

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

func TestHubDoesNotWakeItselfAfterDrainingItsOwnFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "feed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO api_change_pending(audience,type,resource_kind,resource_id,revision,data) VALUES('server','settings.changed','server','settings','1','{}')`)
	if err != nil {
		t.Fatal(err)
	}
	hub := New(db)
	wake, release := hub.Subscribe()
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	dbwork.ProfileStatements(true)
	defer dbwork.ProfileStatements(false)
	go func() { defer close(done); hub.Run(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-wake:
	case <-time.After(3 * time.Second):
		t.Fatal("initial outbox flush did not notify")
	}
	// A complete batch has no pending work. Its own api_events publication
	// must not schedule a second transaction at the feed's two-second floor.
	time.Sleep(2300 * time.Millisecond)
	var reads int64
	for _, p := range dbwork.StatementProfile() {
		if strings.HasPrefix(p.Shape, outboxReadShape) {
			reads += p.Count
		}
	}
	if reads != 1 {
		t.Fatalf("own flush scheduled %d outbox passes", reads)
	}
}

func TestRetiringOwnFlushWakePreservesPeerPublications(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "peer.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	signal := worker.NewSignal()
	stop := dbwork.WakeOnTables(signal, "api_events")
	defer stop()
	commit := func() {
		t.Helper()
		if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO api_events(audience,type,at_ms) VALUES('server','settings.changed',0)`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := dbwork.ChangingCommits()
	commit()
	retireOwnFlushWake(signal, before, true)
	if signal.Take() {
		t.Fatal("own covered publication retained work")
	}
	before = dbwork.ChangingCommits()
	commit()
	commit()
	retireOwnFlushWake(signal, before, true)
	if !signal.Take() {
		t.Fatal("peer publication during flush was lost")
	}
	before = dbwork.ChangingCommits()
	commit()
	retireOwnFlushWake(signal, before, true)
	commit()
	if !signal.Take() {
		t.Fatal("peer publication after retirement was lost")
	}
}

// TestEmptyFlushPublishesOneCommit ties the retirement rule to the actual write
// gate. An empty flush given an already-open transaction still publishes that
// transaction even though it cannot wake subscribers; the feed no longer opens
// one for an empty outbox (see flushPendingHints), so retirement only has to
// distinguish a nonempty flush from a changing peer commit.
func TestEmptyFlushPublishesOneCommit(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "emptyflush.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	before := dbwork.Publications()
	count := -1
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var err error
		count, err = apievents.Flush(ctx, tx, 64)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("empty outbox flushed %d hints", count)
	}
	if got := dbwork.Publications(); got != before+1 {
		t.Fatalf("empty flush published %d transactions, want 1", got-before)
	}
}

// A pass with no changing commit has already covered its pending wake.
func TestRetiringOwnFlushWakeWithNoChangesDoesNotRearm(t *testing.T) {
	signal := worker.NewSignal()
	signal.Wake()
	before := dbwork.ChangingCommits()
	retireOwnFlushWake(signal, before, false)
	if signal.Take() {
		t.Fatal("a pass that changed nothing re-armed a covered wake")
	}
}

// An unknown write scope wakes every subscriber. When it lands during a pass it
// must keep its own pass, exactly like a named peer commit.
func TestRetiringOwnFlushWakeRetainsAnUnknownPeerPublication(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "unknown.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	signal := worker.NewSignal()
	stop := dbwork.WakeOnTables(signal, "api_events")
	defer stop()
	before := dbwork.ChangingCommits()
	// The pass's own flush commit, then an unknown-scope peer commit.
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO api_events(audience,type,at_ms) VALUES('server','settings.changed',0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `WITH row(audience) AS (SELECT 'server') INSERT INTO api_events(audience,type,at_ms) SELECT audience,'settings.changed',0 FROM row`); err != nil {
		t.Fatal(err)
	}
	retireOwnFlushWake(signal, before, true)
	if !signal.Take() {
		t.Fatal("an unknown-scope peer publication was retired with the flush")
	}
}

// outboxReadShape identifies the feed's bounded outbox read in the profile.
const outboxReadShape = "SELECT audience,type,resource_kind"

// outboxProbeShape identifies the feed's read-only pending preflight. It is the
// shape every pass runs, whether or not the outbox holds anything.
const outboxProbeShape = "SELECT EXISTS(SELECT 1 FROM api_change_pending)"

func outboxProbes() int64 {
	var probes int64
	for _, p := range dbwork.StatementProfile() {
		if strings.HasPrefix(p.Shape, outboxProbeShape) {
			probes += p.Count
		}
	}
	return probes
}

func waitForOutboxPasses(t *testing.T, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for outboxProbes() < want {
		if time.Now().After(deadline) {
			t.Fatalf("outbox saw %d passes, want at least %d", outboxProbes(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startFeed runs the hub on db with statement profiling on. The returned stop
// cancels the loop, waits for it to finish and turns profiling off, in that
// order, so a later test starts from a quiet profile.
func startFeed(t *testing.T, db *sql.DB) (stop func()) {
	t.Helper()
	hub := New(db)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	dbwork.ProfileStatements(true)
	go func() { defer close(done); hub.Run(ctx) }()
	return func() {
		cancel()
		<-done
		dbwork.ProfileStatements(false)
	}
}

// An idle server's feed must settle. Its startup pass reads an empty outbox and
// publishes nothing that wakes it, so it must not schedule itself again, and it
// must not take the writer gate to discover there is nothing to publish.
func TestHubStaysQuietWhenTheOutboxStartsEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "empty.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before := dbwork.Publications()
	gateBefore := dbwork.WriteGate().Stats().Acquired
	stop := startFeed(t, db)
	defer stop()
	waitForOutboxPasses(t, 1)
	// A wrongly re-armed wake runs again at the feed's two-second floor.
	time.Sleep(2300 * time.Millisecond)
	if passes := outboxProbes(); passes != 1 {
		t.Fatalf("an empty pass scheduled %d outbox passes", passes)
	}
	if got := dbwork.Publications(); got != before {
		t.Fatalf("empty startup pass published %d transactions, want none", got-before)
	}
	if got := dbwork.WriteGate().Stats().Acquired; got != gateBefore {
		t.Fatalf("empty startup pass acquired the writer gate %d times, want none", got-gateBefore)
	}
}

// A wake that queues no outbox hint is read through by the one pass it causes.
// That pass publishes no transaction, and retiring the wake must not leave the
// wake that triggered the pass armed behind it.
func TestHubDoesNotRearmAWakeThatQueuedNoHints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "peerwake.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stop := startFeed(t, db)
	defer stop()
	waitForOutboxPasses(t, 1)
	// Let the startup pass finish reading and retire before the peer commit, so
	// the commit is a wake of its own rather than work already in flight.
	time.Sleep(300 * time.Millisecond)
	// A committed event wakes the feed without queueing a coalesced hint.
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO api_events(audience,type,at_ms) VALUES('server','settings.changed',0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	waitForOutboxPasses(t, 2)
	time.Sleep(2300 * time.Millisecond)
	if passes := outboxProbes(); passes != 2 {
		t.Fatalf("one peer wake scheduled %d outbox passes", passes)
	}
}
