package eventfeed

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

func TestRetiringFlushWakeDistinguishesChangingPeerCommits(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "nonempty-flush-unchanged-peer"
		if empty {
			name = "empty-flush-one-changing-peer"
		}
		t.Run(name, func(t *testing.T) {
			db, err := persistence.Open(filepath.Join(t.TempDir(), "peer.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if !empty {
				if _, err := db.Exec(`INSERT INTO api_change_pending(audience,type,resource_kind,resource_id,revision,data) VALUES('server','settings.changed','server','settings','1','{}')`); err != nil {
					t.Fatal(err)
				}
			}
			signal := worker.NewSignal()
			stop := dbwork.WakeOnTables(signal, "api_events")
			defer stop()
			ctx := context.Background()
			before, publications := dbwork.ChangingCommits(), dbwork.Publications()
			count := -1
			if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				var err error
				count, err = apievents.Flush(ctx, tx, 64)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if empty && count != 0 || !empty && count != 1 {
				t.Fatalf("unexpected own flush count %d", count)
			}
			if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				if empty {
					_, err := tx.ExecContext(ctx, `INSERT INTO api_events(audience,type,at_ms) VALUES('server','settings.changed',0)`)
					return err
				}
				var one int
				return tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
			}); err != nil {
				t.Fatal(err)
			}
			if dbwork.ChangingCommits() != before+1 || dbwork.Publications() != publications+2 {
				t.Fatal("fixture must commit one changing and one unchanged transaction")
			}
			retireOwnFlushWake(signal, before, count > 0)
			if pending := signal.Take(); pending != empty {
				t.Fatalf("pending peer wake = %v; empty own flush = %v", pending, empty)
			}
		})
	}
}
