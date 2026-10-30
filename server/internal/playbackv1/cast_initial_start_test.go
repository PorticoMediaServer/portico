package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// An initial Cast start has no occurrence to adopt yet: the narrow start
// authority binds the viewer's live device queue and the exact selected entry
// and resolved item, and the accepting writer repeats those facts before it
// inserts. A queue that gains a session meanwhile is refused.
func TestInitialCastStartBindsSelectedEntryAndRefusesConcurrentSession(t *testing.T) {
	c, s, caller, q := sonicQueueFixture(t)
	cast := caller
	cast.Principal.Grant = identity.GrantCastTarget

	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.readLayout(context.Background(), tx, q)
	if err != nil {
		t.Fatal(err)
	}
	entryID, item, ok := l.entryAt(0)
	stored, err := loadQueue(context.Background(), tx, q.id)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !ok || entryID == "" || item == "" {
		t.Fatal("fixture queue has no first entry")
	}

	admitted := 0
	s.CastQueueStartAuthority = func(ctx context.Context, tx *sql.Tx, c Caller, queue, entry, itemArg string) error {
		if tx == nil || c.Principal != cast.Principal {
			t.Fatalf("start authority lost its transaction or caller: %v %v", tx, c.Principal)
		}
		// Every read attempt and every missing-window capture repeats exactly
		// these bound facts; the count is the queue's own capture policy.
		if queue != q.id || entry != entryID || itemArg != item {
			t.Fatalf("start authority binding drifted: %q %q %q", queue, entry, itemArg)
		}
		admitted++
		return nil
	}
	resolved, initial, revision, err := s.withMusicLayoutStart(context.Background(), cast, q.id, entryID, sonicEntryWindow(entryID), func(*layout) error { return nil })
	if err != nil || !initial || resolved != item || revision != stored.revision {
		t.Fatalf("initial start did not bind: item=%q initial=%v revision=%d err=%v", resolved, initial, revision, err)
	}
	if admitted == 0 {
		t.Fatal("initial start never reached the start authority")
	}

	// The accepting writer repeats the bound facts and refuses a queue that
	// gained a session while the start was in flight.
	bound := queueInitialStart{queue: q.id, entry: entryID, item: item, revision: revision}
	base := row{queueID: q.id, entryID: entryID, item: item}
	check := func() error {
		checkTx, err := c.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer checkTx.Rollback()
		return s.recheckInitialQueueStartTx(context.Background(), checkTx, cast, bound, base)
	}
	if err = check(); err != nil {
		t.Fatalf("matching facts refused: %v", err)
	}
	c.Exec(`UPDATE queues_v1 SET session_id='other' WHERE id='q_capture'`)
	if err = check(); !errors.Is(err, ErrRevision) {
		t.Fatalf("gained session not refused at publication: %v", err)
	}
	if _, _, _, err = s.withMusicLayoutStart(context.Background(), cast, q.id, entryID, sonicEntryWindow(entryID), func(*layout) error { return nil }); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("session queue skipped the adopted-occurrence guard: %v", err)
	}
	c.Exec(`UPDATE queues_v1 SET session_id='' WHERE id='q_capture'`)
	// A changed revision alone (order/source drift with the same selected entry and
	// item) is refused by the accepting writer.
	c.Exec(`UPDATE queues_v1 SET revision=revision+1 WHERE id='q_capture'`)
	if err = check(); !errors.Is(err, ErrRevision) {
		t.Fatalf("revision-only drift not refused: %v", err)
	}
	c.Exec(`UPDATE queues_v1 SET revision=revision-1 WHERE id='q_capture'`)
	base.item = "other-item"
	if err = check(); !errors.Is(err, ErrRevision) {
		t.Fatalf("mismatched bound item not refused: %v", err)
	}

	// A refused narrow admission stops before any capture or projection.
	s.CastQueueStartAuthority = func(context.Context, *sql.Tx, Caller, string, string, string) error {
		return identity.ErrForbidden
	}
	if _, _, _, err = s.withMusicLayoutStart(context.Background(), cast, q.id, entryID, sonicEntryWindow(entryID), func(*layout) error { return nil }); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("refused start authority was ignored: %v", err)
	}
}

// An already-adopted queue keeps the generic occurrence authority, so a new
// Sonic window it needs can still be captured instead of looping to ErrRevision.
// The initial-only start authority must never run on this path.
func TestAdoptedShuffledQueueCapturesNewSonicWindow(t *testing.T) {
	c, s, caller, q := sonicQueueFixture(t)
	cast := caller
	cast.Principal.Grant = identity.GrantCastTarget
	adopted := 0
	s.CastQueueAuthority = func(ctx context.Context, tx *sql.Tx, got Caller, queue string) error {
		if tx == nil || got.Principal != cast.Principal || queue != q.id {
			t.Fatalf("adopted authority lost its transaction or binding: %v %v %q", tx, got.Principal, queue)
		}
		adopted++
		return nil
	}
	s.CastQueueStartAuthority = func(context.Context, *sql.Tx, Caller, string, string, string) error {
		t.Fatal("initial-only start authority used for an adopted queue")
		return identity.ErrForbidden
	}
	s.SmoothOrder = func(ctx context.Context, tx *sql.Tx, actor identity.Principal, ids []string) (catalog.MusicSmoothOrder, error) {
		return catalog.MusicSmoothOrder{Positions: []int64{0, 4, 3, 2, 1}, BySound: true, Model: &sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}}, nil
	}
	c.Exec(`UPDATE queues_v1 SET session_id='ps_adopted' WHERE id='q_capture'`)
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.readLayout(context.Background(), tx, q)
	if err != nil {
		t.Fatal(err)
	}
	entryID, item, ok := l.entryAt(0)
	_ = tx.Rollback()
	if !ok || item == "" {
		t.Fatal("fixture queue has no first entry")
	}
	var captured int
	resolved, initial, _, err := s.withMusicLayoutStart(context.Background(), cast, q.id, entryID, sonicEntryWindow(entryID), func(l *layout) error {
		w, ok := l.sonicWindows[0]
		if !ok || w.model == nil || len(w.ordinals) == 0 {
			t.Fatal("projected before the missing window was captured")
		}
		captured++
		return nil
	})
	if err != nil || initial || resolved != item || captured != 1 {
		t.Fatalf("adopted capture: item=%q initial=%v captured=%d err=%v", resolved, initial, captured, err)
	}
	if adopted == 0 {
		t.Fatal("adopted capture never took the generic occurrence authority")
	}
}
