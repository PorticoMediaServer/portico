package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

type adminEventsAdmissionConnector struct {
	driver.Connector
	connect func(context.Context) (driver.Conn, error)
}

func (c adminEventsAdmissionConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.connect(ctx)
}

// Observe the permit at physical acquisition, before any query can hide a
// cold-pool admission bypass. Request reuse must use the same admission.
func TestAdminEventsUseSnapshotAdmission(t *testing.T) {
	for _, request := range []bool{false, true} {
		t.Run(map[bool]string{false: "private", true: "request"}[request], func(t *testing.T) {
			base, err := sqlite.NewConnector(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			held := dbwork.Snapshots().Held
			stopped := errors.New("stop at cold reader acquisition")
			calls := 0
			db := sql.OpenDB(adminEventsAdmissionConnector{base, func(ctx context.Context) (driver.Conn, error) {
				calls++
				if got := dbwork.Snapshots().Held; got != held+1 {
					t.Errorf("cold admin event read bypassed admission: held=%d, want %d", got, held+1)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > dbwork.ForegroundSnapshotLifetime {
					t.Error("reader lost its foreground lifetime")
				}
				return nil, stopped
			}})
			defer db.Close()
			ctx := context.Background()
			if request {
				var end func()
				ctx, end = dbwork.WithRequestSnapshot(ctx)
				defer end()
			}
			out := adminEvents{Limit: adminEventLimit}
			if err := readAdminEvents(ctx, db, &out); !errors.Is(err, stopped) || calls != 1 {
				t.Fatalf("acquisition failure: err=%v opens=%d", err, calls)
			}
			if got := dbwork.Snapshots().Held; got != held {
				t.Fatalf("failed acquisition leaked permit: %d, want %d", got, held)
			}
		})
	}
}

// The finite projection stays exact, bounded, and on its caller's carried
// transaction. Closing its own request snapshot must precede the later fresh
// authority proof; a carried transaction continues to belong to its caller.
func TestAdminEventsSnapshotProjectionAndOwnership(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE console_audit(sequence INTEGER PRIMARY KEY,time_ms INTEGER,action TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 24; i++ {
		code := "settings.apply"
		if i%2 == 0 {
			code = "job.pause"
		}
		if i > 20 {
			code = "private.unsupported"
		}
		if _, err := db.Exec(`INSERT INTO console_audit VALUES(?,?,?)`, i, 1000+i, code); err != nil {
			t.Fatal(err)
		}
	}
	want := []adminEvent{}
	for i := 20; i > 12; i-- {
		code := adminEventCode("settings.apply")
		if i%2 == 0 {
			code = "job.pause"
		}
		want = append(want, adminEvent{At: apiwire.Instant(1000 + i), Code: code})
	}
	for _, request := range []bool{false, true} {
		ctx := context.Background()
		end := func() {}
		if request {
			ctx, end = dbwork.WithRequestSnapshot(ctx)
		}
		out := adminEvents{Items: []adminEvent{}, Limit: adminEventLimit}
		if err := readAdminEvents(ctx, db, &out); err != nil {
			end()
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out.Items, want) {
			t.Fatalf("projection=%+v, want %+v", out.Items, want)
		}
		if got := db.Stats().InUse; got != 0 {
			t.Errorf("event read retained %d connections before authority proof", got)
		}
		end()
	}
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		out := adminEvents{Limit: adminEventLimit}
		if err := readAdminEvents(ctx, db, &out); err != nil {
			return err
		}
		if !reflect.DeepEqual(out.Items, want) {
			t.Fatal("carried projection changed")
		}
		var one int
		return tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
	}); err != nil {
		t.Fatal("reader closed its caller's transaction", err)
	}
}
