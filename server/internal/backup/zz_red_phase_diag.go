package backup

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"log"
	"time"
)

func redBackupPhase(ctx context.Context, db *sql.DB, phase string) func(*error) {
	start := time.Now()
	return func(err *error) {
		log.Printf("REDS-BACKUP phase=%s elapsed=%s error=%v wal=%+v ledger=%+v gate=%+v", phase, time.Since(start), *err, dbwork.WAL(ctx, db), dbwork.WALReservations(db), dbwork.WriteGate().Stats())
	}
}
