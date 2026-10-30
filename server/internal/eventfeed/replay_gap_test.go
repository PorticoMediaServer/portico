package eventfeed

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestPrunedEmptyRingResyncsOnlyBehindReaders(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO api_events(audience,type,at_ms) VALUES('server','library.changed',1),('server','library.changed',2)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM api_events`); err != nil {
		t.Fatal(err)
	}
	h := New(db)
	for _, tc := range []struct {
		after  int64
		resync bool
	}{{0, true}, {1, true}, {2, false}, {3, true}} {
		page, err := h.readAt(context.Background(), identity.Principal{}, false, tc.after, true)
		if err != nil {
			t.Fatal(err)
		}
		got := len(page.Events) == 1 && page.Events[0].Type == "stream.resync"
		if got != tc.resync || page.NextAfter != "2" {
			t.Fatalf("after %d: want resync=%v; got %+v", tc.after, tc.resync, page)
		}
	}
	page, err := h.readAt(context.Background(), identity.Principal{}, false, 0, false)
	if err != nil || len(page.Events) != 0 || page.NextAfter != "2" {
		t.Fatalf("initial stream: %+v %v", page, err)
	}
}
