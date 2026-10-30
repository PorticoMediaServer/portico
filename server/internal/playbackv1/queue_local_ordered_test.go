package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type localQueueTestIterator struct {
	ids            []string
	at             int
	preparing      bool
	checks         int
	closed, paused bool
}

func (it *localQueueTestIterator) Next(context.Context) (catalog.QueueLocalOrderedBatch, error) {
	if it.preparing {
		return catalog.QueueLocalOrderedBatch{}, nil
	}
	end := min(len(it.ids), it.at+128)
	out := append([]string{}, it.ids[it.at:end]...)
	it.at = end
	return catalog.QueueLocalOrderedBatch{IDs: out, Ready: true, EOF: end == len(it.ids)}, nil
}
func (it *localQueueTestIterator) Close()                                    { it.closed = true }
func (it *localQueueTestIterator) Pause()                                    { it.paused = true }
func (it *localQueueTestIterator) ReserveDelivery(int64) error               { return nil }
func (it *localQueueTestIterator) CheckBasis(context.Context, *sql.Tx) error { it.checks++; return nil }

func TestQueueLocalOrderedPreparingReturnsWithoutRetainingCapture(t *testing.T) {
	c := catalogtest.Open(t)
	s := New(c.DB, nil, nil)
	it := &localQueueTestIterator{preparing: true}
	s.QueryKeys = func(context.Context, *sql.Tx, identity.Principal, Selector) (catalog.QueueSelection, error) {
		return catalog.QueueSelection{Iterator: it, Revision: "r", CheckBasis: func(context.Context, *sql.Tx) error { return nil }}, nil
	}
	in := SegmentInput{Source: Selector{Query: &catalog.QueueQuery{LibraryID: "a"}}}
	_, err := s.buildSnapshot(context.Background(), identity.Principal{}, "q", in, "source", 100, nil, scanBudget{until: time.Now().Add(-time.Second)})
	if !errors.Is(err, ErrQueueBuilding) || !it.paused || it.closed {
		t.Fatalf("preparing err=%v iterator=%+v", err, it)
	}
	select {
	case queryCaptureSlots <- struct{}{}:
		<-queryCaptureSlots
	default:
		t.Fatal("retained capture")
	}
}

func TestQueueLocalOrderedRestartReconcilesAndExtendsPartialChunk(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("a", "Movies", "movie", "/a")
	ids := []string{}
	for n := 0; n < 270; n++ {
		item := c.Movie(lib, fmt.Sprintf("/a/%03d.mkv", n), fmt.Sprintf("Film %03d", n), 2026)
		ids = append(ids, item.Public)
	}
	c.Drain()
	s := New(c.DB, nil, nil)
	s.HoldQueueBuilds()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p"}}
	s.QueryKeys = func(context.Context, *sql.Tx, identity.Principal, Selector) (catalog.QueueSelection, error) {
		return catalog.QueueSelection{Iterator: &localQueueTestIterator{ids: ids}, Revision: "r", Label: "Movies", CheckBasis: func(context.Context, *sql.Tx) error { return nil }}, nil
	}
	in := SegmentInput{Source: Selector{Query: &catalog.QueueQuery{LibraryID: "a"}}, Anchor: &Anchor{ItemID: ids[260]}}
	snap, err := s.buildSnapshot(context.Background(), p, "q_local", in, "source", MaxQueueKeys, nil, scanBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.count != 128 || !snap.partial || snap.anchor != -1 || snap.anchorItem != ids[260] {
		t.Fatalf("initial %+v", snap)
	}
	snap.caller = `{"viewer":{"authority":"local","accountId":"a","profileId":"p"}}`
	snap.start = `{"mode":"ordered","anchorItem":"` + ids[260] + `"}`
	if _, err = c.DB.Exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES('q_local','device','device','local','a','p',1,0,0)`); err != nil {
		t.Fatal(err)
	}
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = storeSnapshot(context.Background(), tx, "q_local", &snap); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO queue_v1_segments(id,queue_id,sort_key,snapshot_id,first_ordinal,count) VALUES('g_local','q_local','a',?,0,?)`, snap.id, snap.count); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.continueBuild(context.Background(), snap.id); err != nil {
		t.Fatal(err)
	}
	var built, current int64
	var state string
	if err = c.DB.QueryRow(`SELECT built,state FROM queue_v1_snapshots WHERE id=?`, snap.id).Scan(&built, &state); err != nil {
		t.Fatal(err)
	}
	if built != 270 || state != "ready" {
		t.Fatalf("built=%d %s", built, state)
	}
	if err = c.DB.QueryRow(`SELECT current_position FROM queues_v1 WHERE id='q_local'`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != 260 {
		t.Fatalf("anchor %d", current)
	}
	got := []string{}
	rows, err := c.DB.Query(`SELECT keys FROM queue_v1_chunks WHERE snapshot_id=? ORDER BY chunk_no`, snap.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		for n := 0; ; n++ {
			key, ok := chunkKey(raw, n)
			if !ok {
				break
			}
			got = append(got, key)
		}
	}
	if !reflect.DeepEqual(got, ids) {
		t.Fatalf("chunk sequence count %d want %d", len(got), len(ids))
	}
}

func TestQueueLocalOrderedRestartRejectsDifferentAcceptedSequence(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("a", "Movies", "movie", "/a")
	a := c.Movie(lib, "/a/a.mkv", "A", 2000)
	b := c.Movie(lib, "/a/b.mkv", "B", 2000)
	c.Drain()
	s := New(c.DB, nil, nil)
	if _, err := c.DB.Exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES('q','device','device','local','a','p',1,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`INSERT INTO queue_v1_snapshots(id,queue_id,kind,selector,label,width,count,built,state) VALUES('s_local','q','selector','{}','',0,1,1,'building')`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`INSERT INTO queue_v1_chunks(snapshot_id,chunk_no,keys) VALUES('s_local',0,?)`, encodeChunk([]string{a.Public})); err != nil {
		t.Fatal(err)
	}
	it := &localQueueTestIterator{ids: []string{b.Public, a.Public}}
	k := selectorKeys{iterator: it, checkBasis: func(context.Context, *sql.Tx) error { return nil }}
	err := s.continueLocalOrderedBuild(context.Background(), identity.Principal{}, "s_local", "q", "selector", "{}", "", 1, 1, nil, k)
	if !errors.Is(err, errSelectionChanged) {
		t.Fatalf("reordered accepted sequence: %v", err)
	}
	// A held proof can change after the fresh acceptance read. The final writer
	// must still refuse the new chunk and the EOF/ready transition atomically.
	k.iterator = &localQueueTestIterator{ids: []string{a.Public, b.Public}}
	k.checkBasis = func(ctx context.Context, _ *sql.Tx) error {
		if dbwork.InWriteTx(ctx) {
			return ErrRevision
		}
		return nil
	}
	err = s.continueLocalOrderedBuild(context.Background(), identity.Principal{}, "s_local", "q", "selector", "{}", "", 1, 1, nil, k)
	if !errors.Is(err, ErrRevision) {
		t.Fatalf("final writer drift: %v", err)
	}
	var built int
	var state string
	if err = c.DB.QueryRow(`SELECT built,state FROM queue_v1_snapshots WHERE id='s_local'`).Scan(&built, &state); err != nil {
		t.Fatal(err)
	}
	if built != 1 || state != "building" {
		t.Fatalf("published on stale basis %d %s", built, state)
	}
}
