package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"strings"
	"time"
)

func artworkPhase(ctx context.Context, db *sql.DB, phase string, started time.Time, err error) {
	if os.Getenv("PORTICO_ARTWORK_DIAGNOSTIC") != "1" || !strings.HasPrefix(ownerFrom(ctx), "ARTWORK-") {
		return
	}
	deadline, _ := ctx.Deadline()
	log.Printf("ARTWORK-PHASE at=%d owner=%s phase=%s elapsed=%s left=%s err=%v ctxerr=%v snapshots=%+v pool=%+v", time.Now().UnixNano(), ownerFrom(ctx), phase, time.Since(started), time.Until(deadline), err, ctx.Err(), Snapshots(), ReadHandle(ctx, db).Stats())
}

func artworkOpened(name string, readers *readerScope, role connRole, started, acquired time.Time, err error) {
	if os.Getenv("PORTICO_ARTWORK_DIAGNOSTIC") != "1" || !strings.Contains(name, "TestArtworkSlowPublicationDoesNotBlockReadsOrPreviews") {
		return
	}
	log.Printf("ARTWORK-OPEN at=%d scope=%p role=%v lock_wait=%s driver_open=%s err=%v", time.Now().UnixNano(), readers, role, acquired.Sub(started), time.Since(acquired), err)
}

func artworkRejectCold(ctx context.Context) error {
	if os.Getenv("PORTICO_ARTWORK_REJECT_COLD") == "1" && strings.HasPrefix(ownerFrom(ctx), "ARTWORK-") {
		return errors.New("diagnostic: publication phase attempted a cold foreground reader")
	}
	return nil
}
func artworkGateMutation(ctx context.Context) bool {
	return os.Getenv("PORTICO_ARTWORK_GATE_MUTATION") == "1" && strings.HasPrefix(ownerFrom(ctx), "ARTWORK-")
}
