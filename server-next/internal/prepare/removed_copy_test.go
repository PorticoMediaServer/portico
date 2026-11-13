package prepare

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestRemovedCopyCannotPublishIntoARetriedCopy(t *testing.T) {
	d, film, _ := filmLibrary(t)
	ctx, now := context.Background(), time.Now()
	old, err := d.WantPrepared(ctx, film.MediaID(), "240p", 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RemovePrepared(ctx, []int64{old.ID}); err != nil {
		t.Fatal(err)
	}
	next, err := d.WantPrepared(ctx, film.MediaID(), "240p", 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if old.ID != next.ID || old.PublicID == next.PublicID {
		t.Fatal("fixture did not reuse the removed SQLite row id", old, next)
	}
	if err := d.SetPreparedState(ctx, old.ID, old.PublicID, db.PreparedFailed, "old worker"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishPrepared(ctx, old.ID, old.PublicID, "mp4", 3, "old bytes", now); err != nil {
		t.Fatal(err)
	}
	got, err := d.PreparedByID(ctx, next.ID)
	if err != nil || got.State != db.PreparedWaiting || got.ContentHash != "" {
		t.Fatal("removed worker changed the retried copy", got, err)
	}
}
