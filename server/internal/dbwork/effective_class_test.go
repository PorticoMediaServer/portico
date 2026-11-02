package dbwork

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// A helper written for a worker names background; called from a request it must
// run at the request's class, never behind every scan batch (RD-4, J-18).
func TestEffectiveClassFollowsTheRequestNotTheCallSite(t *testing.T) {
	request := WithRequest(WithClass(context.Background(), ClassEstablishedPlayback), ClassEstablishedPlayback)
	if got := Effective(request, ClassBackgroundMedia); got != ClassEstablishedPlayback {
		t.Fatalf("background write under a request runs at %s", got)
	}
	if got := Effective(request, ClassSecurityFence); got != ClassSecurityFence {
		t.Fatalf("a higher class named under a request was changed to %s", got)
	}
	if got := ClassFrom(WithClass(request, ClassBackgroundMedia), ClassInteractive); got != ClassEstablishedPlayback {
		t.Fatalf("a request that marks itself background reads at %s", got)
	}
	if got := Effective(context.Background(), ClassBackgroundMedia); got != ClassBackgroundMedia {
		t.Fatalf("a worker without a request was promoted to %s", got)
	}
	owner := WithRequest(context.Background(), ClassMaintenance)
	if got := Effective(owner, ClassBackgroundMedia); got != ClassBackgroundMedia {
		t.Fatalf("a maintenance-class owner lane promoted background work to %s", got)
	}
	var supervised Class
	supervise.Loop(request, "test.worker-started-by-request", func(ctx context.Context) {
		supervised = Effective(ctx, ClassBackgroundMedia)
	})
	if supervised != ClassBackgroundMedia {
		t.Fatalf("a supervised worker started from a request runs at %s", supervised)
	}
}

func TestBackgroundWritesUnderARequestTakeTheRequestsGateClass(t *testing.T) {
	db := testDB(t)
	request := WithRequest(context.Background(), ClassEstablishedPlayback)
	before := WriteGate().Stats().ByClass
	if _, err := ExecWrite(request, db, ClassBackgroundMedia, `INSERT INTO rows(value) VALUES('exec')`); err != nil {
		t.Fatal(err)
	}
	if err := WithWriteTx(request, db, ClassBackgroundMedia, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO rows(value) VALUES('tx')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after := WriteGate().Stats().ByClass
	playback, background := ClassEstablishedPlayback.String(), ClassBackgroundMedia.String()
	if got := after[playback].Acquired - before[playback].Acquired; got != 2 {
		t.Fatalf("request writes acquired the gate %d times at %s, want 2", got, playback)
	}
	if got := after[background].Acquired - before[background].Acquired; got != 0 {
		t.Fatalf("request writes acquired the gate %d times at %s, want 0", got, background)
	}
}
