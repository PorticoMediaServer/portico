package operations_test

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestSetupRemoteChoicesTx(t *testing.T) {
	c := catalogtest.Open(t)
	ctx := context.Background()
	store := operations.New(c.DB)
	before, err := store.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = operations.SetupRemoteChoicesTx(ctx, tx, "private", "standard", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := store.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	if after.Effective.RemoteAccess != "private" || after.Effective.RemoteSecurity != "standard" || after.Revision <= before.Revision {
		t.Fatalf("settings after setup: %s/%s revision %d (was %d)", after.Effective.RemoteAccess, after.Effective.RemoteSecurity, after.Revision, before.Revision)
	}
	// Only these two were written: a computed default stays computed.
	if len(after.Effective.LANNetworks) != len(before.Effective.LANNetworks) {
		t.Fatalf("home networks changed: %v → %v", before.Effective.LANNetworks, after.Effective.LANNetworks)
	}
	var queued int
	if err = c.DB.QueryRow(`SELECT count(*) FROM server_configuration_pending WHERE field IN ('remoteAccess','remoteSecurity')`).Scan(&queued); err != nil || queued != 2 {
		t.Fatalf("file projection not queued: %d %v", queued, err)
	}
	tx, _ = c.DB.BeginTx(ctx, nil)
	defer tx.Rollback()
	if err = operations.SetupRemoteChoicesTx(ctx, tx, "sometimes", "enhanced", time.Now()); err == nil {
		t.Fatal("an unknown choice was written")
	}
}
