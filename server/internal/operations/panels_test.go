package operations

import (
	"context"
	"database/sql"
	"errors"
	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	"testing"
	"time"
)

func TestIndependentPanelsAndDatabaseFailure(t *testing.T) {
	db, e := sql.Open("sqlite", ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = Read(context.Background(), db, "database"); !errors.Is(e, ErrUnavailable) {
		t.Fatal("missing canonical table must fail", e)
	}
	for _, name := range []string{"memory", "build"} {
		p, e := Read(context.Background(), db, name)
		if e != nil {
			t.Fatal(name, e)
		}
		observed, _ := time.Parse(time.RFC3339Nano, p.ObservedAt)
		fresh, _ := time.Parse(time.RFC3339Nano, p.FreshUntil)
		if fresh.Sub(observed) != 30*time.Second {
			t.Fatal("freshness", p)
		}
		if name == "memory" && (p.Memory == nil || p.Memory.RuntimeReservedBytes == 0 || p.Memory.HeapObjectsBytes > p.Memory.RuntimeReservedBytes) {
			t.Fatal("memory measurement", p)
		}
	}
	if _, e = db.Exec(`CREATE TABLE admin_revision(id INTEGER PRIMARY KEY,revision INTEGER);INSERT INTO admin_revision VALUES(1,4)`); e != nil {
		t.Fatal(e)
	}
	p, e := Read(context.Background(), db, "database")
	if e != nil || p.Database == nil || !p.Database.Readable {
		t.Fatal(p, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Read(ctx, db, "memory"); !errors.Is(e, ErrUnavailable) {
		t.Fatal("cancel", e)
	}
	if _, e = Read(context.Background(), db, "host-secrets"); !errors.Is(e, ErrPanel) {
		t.Fatal(e)
	}
}
