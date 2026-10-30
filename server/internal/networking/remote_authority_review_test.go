package networking

import (
	"errors"
	"testing"
	"time"
)

// Uses the normal SQLite/current-claim fixture, not the offline protocol subset.
func TestW2I02RemoteIntentAndPublicationRecheckInstalledAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, cert, ctx := certificateFixture(t)
	if e := seedRemoteSettings(ctx, f.db); e != nil {
		t.Fatal(e)
	}
	m := &RemoteManager{h: &ClaimHandler{store: f.store, certificates: cert}}
	c, _, authority, state, key, e := m.snapshot(ctx)
	clear(key)
	if e != nil || authority == "" {
		t.Fatal("missing current claim", e)
	}
	row := reviewMapping()
	row.ID = "review-owned-intent"
	row.Authority = authority
	row.Revision = c.Revision
	row.PotentialUntil = time.Now().Add(time.Hour)
	if e = m.saveMappingIntent(ctx, row); e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE fixture_server_authority SET allowed=0`); e != nil {
		t.Fatal(e)
	}
	if e = m.saveMappingIntent(ctx, row); !errors.Is(e, ErrStale) {
		t.Fatal("stale installed claim admitted an effect", e)
	}
	if e = m.saveState(ctx, authority, c, state, RemoteStatus{State: "reachable"}); !errors.Is(e, ErrStale) {
		t.Fatal("stale claim published reachability", e)
	}
	// Old owned effects still need a durable cleanup receipt after authority loss.
	row.State = "cleanup_pending"
	if e = m.saveMapping(ctx, row); e != nil {
		t.Fatal("cleanup receipt lost", e)
	}
	rows, e := m.mappings(ctx)
	if e != nil || len(rows) != 1 || rows[0].State != "cleanup_pending" || !rows[0].MutationPending {
		t.Fatal(rows, e)
	}
}
func TestW2I02RemoteIntentAndPublicationRejectStaleConfigRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, cert, ctx := certificateFixture(t)
	if e := seedRemoteSettings(ctx, f.db); e != nil {
		t.Fatal(e)
	}
	m := &RemoteManager{h: &ClaimHandler{store: f.store, certificates: cert}}
	c, _, authority, state, key, e := m.snapshot(ctx)
	clear(key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.db.Exec(`UPDATE networking_remote_settings SET revision=revision+1,enabled=0`); e != nil {
		t.Fatal(e)
	}
	row := reviewMapping()
	row.ID = "stale-config"
	row.Authority = authority
	row.Revision = c.Revision
	if e = m.saveMappingIntent(ctx, row); !errors.Is(e, ErrStale) {
		t.Fatal("disabled config admitted stale intent", e)
	}
	if e = m.saveState(ctx, authority, c, state, RemoteStatus{State: "reachable"}); !errors.Is(e, ErrStale) {
		t.Fatal("stale config published status", e)
	}
	rows, e := m.mappings(ctx)
	if e != nil || len(rows) != 0 {
		t.Fatal("stale intent persisted", rows, e)
	}
}
