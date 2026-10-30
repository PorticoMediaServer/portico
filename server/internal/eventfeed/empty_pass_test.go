package eventfeed

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// An empty outbox must not cost the single writer gate. The feed's pass probes
// the outbox on a read handle first; only a nonempty probe opens a gated
// transaction.
func TestEmptyOutboxPassTakesNoWriterGate(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "empty-pass.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gateBefore := dbwork.WriteGate().Stats().Acquired
	publicationsBefore := dbwork.Publications()
	changesBefore := dbwork.ChangingCommits()
	count, err := flushPendingHints(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("empty outbox pass published %d hints", count)
	}
	if got := dbwork.WriteGate().Stats().Acquired; got != gateBefore {
		t.Fatalf("empty outbox pass acquired the writer gate %d times, want none", got-gateBefore)
	}
	if got := dbwork.Publications(); got != publicationsBefore {
		t.Fatalf("empty outbox pass published %d transactions, want none", got-publicationsBefore)
	}
	if got := dbwork.ChangingCommits(); got != changesBefore {
		t.Fatalf("empty outbox pass made %d changing commits, want none", got-changesBefore)
	}
}

// A nonempty outbox publishes exactly the first bounded batch, in outbox order,
// and removes exactly those hints. Both halves are one gated transaction: the
// events cannot exist while their hints remain, nor the hints vanish without
// their events.
func TestOutboxPassPublishesFirstBatchInOrder(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "full-pass.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const total, batch = 65, 64
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("%03d", i)
		if _, err := db.Exec(`INSERT INTO api_change_pending(audience,type,resource_kind,resource_id,revision,data) VALUES('server','test.changed','test',?,'1','')`, id); err != nil {
			t.Fatal(err)
		}
	}
	gateBefore := dbwork.WriteGate().Stats().Acquired
	publicationsBefore := dbwork.Publications()
	count, err := flushPendingHints(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if count != batch {
		t.Fatalf("outbox pass published %d hints, want %d", count, batch)
	}
	if got := dbwork.WriteGate().Stats().Acquired; got != gateBefore+1 {
		t.Fatalf("outbox pass acquired the writer gate %d times, want one", got-gateBefore)
	}
	if got := dbwork.Publications(); got != publicationsBefore+1 {
		t.Fatalf("outbox pass published %d transactions, want one", got-publicationsBefore)
	}
	rows, err := db.Query(`SELECT resource_id FROM api_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var published []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		published = append(published, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(published) != batch {
		t.Fatalf("outbox pass published %d events, want %d", len(published), batch)
	}
	for i, id := range published {
		if want := fmt.Sprintf("%03d", i); id != want {
			t.Fatalf("event %d published hint %q, want %q", i, id, want)
		}
	}
	var remaining int
	if err = db.QueryRow(`SELECT count(*) FROM api_change_pending`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != total-batch {
		t.Fatalf("outbox holds %d hints after the batch, want %d", remaining, total-batch)
	}
	var oldest string
	if err = db.QueryRow(`SELECT resource_id FROM api_change_pending`).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%03d", batch); oldest != want {
		t.Fatalf("outbox retained hint %q, want %q", oldest, want)
	}
}

// A hint queued after an empty pass is still published. The skipped gate must
// not drop the wake that the hint's own commit raises.
func TestEmptyOutboxPassKeepsALaterHintDeliverable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "later-hint.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stop := startFeed(t, db)
	defer stop()
	waitForOutboxPasses(t, 1)
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO api_change_pending(audience,type,resource_kind,resource_id,revision,data) VALUES('server','test.changed','test','late','1','')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var events, pending int
		if err = db.QueryRow(`SELECT count(*) FROM api_events WHERE resource_id='late'`).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(`SELECT count(*) FROM api_change_pending`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if events == 1 && pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hint queued after an empty pass published %d events, %d hints remain", events, pending)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
