package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestP252bSchedulerRealRestartReconcilesRootAndCleanup(t *testing.T) {
	c, album, _ := drainFixture(t, 0)
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "release", ID: "restart"}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if e := compactcatalog.RecordProviderIdentityTx(ctx, tx, album, p); e != nil {
			return e
		}
		if e := enqueueIdentityAdoptionTx(ctx, tx, album, p); e != nil {
			return e
		}
		return EnqueueMutationCleanupTx(ctx, tx)
	})
	c.Exec(`UPDATE console_operations SET state='running' WHERE kind='catalog-mutation'`)
	var sequence int
	var name, native string
	if e := c.DB.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &native); e != nil {
		t.Fatal(e)
	}
	if e := c.DB.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := persistence.Open(native)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	c = catalogtest.New(t, reopened)
	scheduler := operations.NewScheduler(operations.New(reopened))
	adapter := New(reopened).MutationAdapter()
	step := adapter.Step
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sawRecovery := false
	adapter.Step = func(ctx context.Context, id string) (operations.JobObservation, error) {
		var state string
		if e := reopened.QueryRow(`SELECT state FROM console_operations WHERE kind='catalog-mutation' AND resource=?`, id).Scan(&state); e != nil {
			return operations.JobObservation{}, e
		}
		sawRecovery = state == "reconciling"
		out, e := step(ctx, id)
		cancel()
		return out, e
	}
	if e = scheduler.Register(adapter); e != nil {
		t.Fatal(e)
	}
	scheduler.Run(ctx)
	if !sawRecovery {
		t.Fatal("actual scheduler restart did not dispatch reconciliation")
	}
	for turn := 0; turn < 40; turn++ {
		rows, e := reopened.Query(`SELECT resource FROM console_operations WHERE kind='catalog-mutation' AND state IN('queued','running','reconciling') ORDER BY sequence LIMIT 8`)
		if e != nil {
			t.Fatal(e)
		}
		var resources []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				t.Fatal(e)
			}
			resources = append(resources, id)
		}
		rows.Close()
		if len(resources) == 0 {
			return
		}
		for _, id := range resources {
			if _, e = New(reopened).MutationAdapter().Step(context.Background(), id); e != nil {
				t.Fatal(id, e)
			}
		}
	}
	t.Fatal("restart stranded root/cleanup")
}
func TestP252bStepCommandCancelIsRejectedWithoutEffect(t *testing.T) {
	c, album, _ := drainFixture(t, 0)
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "release", ID: "noncancellable"}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if e := compactcatalog.RecordProviderIdentityTx(ctx, tx, album, p); e != nil {
			return e
		}
		if e := enqueueIdentityAdoptionTx(ctx, tx, album, p); e != nil {
			return e
		}
		return EnqueueMutationCleanupTx(ctx, tx)
	})
	scheduler := operations.NewScheduler(operations.New(c.DB))
	if e := scheduler.Register(New(c.DB).MutationAdapter()); e != nil {
		t.Fatal(e)
	}
	rows, e := c.DB.Query(`SELECT id,revision FROM console_operations WHERE kind='catalog-mutation' ORDER BY sequence`)
	if e != nil {
		t.Fatal(e)
	}
	type op struct {
		id       string
		revision int64
	}
	var jobs []op
	for rows.Next() {
		var j op
		rows.Scan(&j.id, &j.revision)
		jobs = append(jobs, j)
	}
	rows.Close()
	var principal identity.Principal
	principal.Authority = "local"
	principal.AccountID = "owner"
	for _, j := range jobs {
		_, e = scheduler.Command(context.Background(), principal, func(context.Context, *sql.Tx, string) error { return nil }, j.id, "cancel", operations.JobCommand{ExpectedRevision: j.revision, IdempotencyKey: "cancel-" + j.id})
		if !errors.Is(e, operations.ErrConflict) {
			t.Fatal("Step cancel admitted", e)
		}
	}
	if n := rowsCount(t, c.DB, `SELECT count(*) FROM console_operations WHERE kind='catalog-mutation' AND state='queued'`); n != 2 {
		t.Fatal("attempted cancel changed actual domain custody", n)
	}
}
func rowsCount(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if e := db.QueryRow(q, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestP252bConflictingRecordingIdentityUnwindsBeforePinDrain(t *testing.T) {
	c, album, songs := drainFixture(t, 1)
	source := songs[0]
	var library int64
	c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, album).Scan(&library)
	var target int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		target, _, e = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Track, Parent: album, Key: "conflict-target", Title: "Song 0"})
		if e != nil {
			return e
		}
		if e = compactcatalog.SetFactsTx(ctx, tx, target, map[string]any{"album_id": album, "track_number": 1}); e != nil {
			return e
		}
		if e = compactcatalog.RecordProviderIdentityTx(ctx, tx, source, compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "recording", ID: "one"}); e != nil {
			return e
		}
		return compactcatalog.RecordProviderIdentityTx(ctx, tx, target, compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "recording", ID: "two"})
	})
	drainEvidence(t, c, "conflict-pin", 1)
	drainPin(t, c, source, "conflict-pin", false)
	var ref compactcatalog.MutationRef
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		ref, e = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: source, Target: target, OwnerKind: "identity", OwnerID: "semantic", OwnerGeneration: 1, Mode: "merge"})
		return e
	})
	step := mutationTurn(t, c, ref)
	if !step.Complete || step.Outcome != "stale" || step.PinDrained {
		t.Fatal(step)
	}
	if n := rows(t, c, `SELECT count(*) FROM mb_song_links WHERE item_id=?`, source); n != 1 {
		t.Fatal("semantic conflict drained old pin")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE family_id=? AND status<>'terminal'`, ref.ID); n != 0 {
		t.Fatal("permanent conflict left fences")
	}
}
func TestP252bAutomaticMissingNeverUnwindsAndRestoresAcquisition(t *testing.T) {
	c, _, songs := drainFixture(t, 1)
	song := songs[0]
	drainEvidence(t, c, "auto-off", 1)
	drainPin(t, c, song, "auto-off", false)
	asset, token := identityFile(t, c, "m", "/m/auto-off.flac", 1)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, song, asset, compactcatalog.Link{})
	})
	markMissing(t, c, "m", token, time.Now().Add(-time.Hour))
	c.Exec(`INSERT INTO server_settings(singleton,"library.missingTitles") VALUES(1,'immediately') ON CONFLICT(singleton) DO UPDATE SET "library.missingTitles"='immediately'`)
	ctx := context.WithValue(context.Background(), automaticMissingKey{}, true)
	cutoff := time.Now()
	pumpFixture(t, "automatic missing pin flip", pinnedRemovalTurns(1), func() bool {
		if _, e := New(c.DB).EmptyMissing(ctx, "m", cutoff, nil); e != nil {
			t.Fatal(e)
		}
		return rows(t, c, `SELECT count(*) FROM mb_song_links WHERE item_id=?`, song) == 0
	})
	c.Exec(`UPDATE server_settings SET "library.missingTitles"='never' WHERE singleton=1`)
	out, e := New(c.DB).EmptyMissingDue(context.Background())
	if e != nil || out.Titles != 0 || out.Files != 0 {
		t.Fatal("revoked automatic rule deleted", out, e)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE cause_operation='automatic-missing' AND status='active'`); n != 0 {
		t.Fatal("never retained active automatic fence", n)
	}
	s := New(c.DB)
	sources, e := s.LibrarySources(context.Background(), "m")
	if e != nil || len(sources) != 1 {
		t.Fatal(sources, e)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, e := s.observeInventoryTx(ctx, tx, "restored-after-never", sources[0], "/m/auto-off.flac", storage.Snapshot{Path: "/m/auto-off.flac", Size: 1000, ModifiedNS: 1}, "", ScanPolicy{Tier: "file_list_only"}, nil)
		if e != nil {
			return e
		}
		return compactcatalog.LinkAssetTx(ctx, tx, song, asset, compactcatalog.Link{})
	})
	// Inventory restoration is published by the ordinary source/availability
	// worker; observing the file is not itself a maintained missing-row update.
	c.Drain()
	if n := rows(t, c, `SELECT count(*) FROM catalog_missing_titles WHERE entity_id=?`, song); n != 0 {
		t.Fatal("actual restoration left missing title", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, song); n != 1 {
		t.Fatal("automatic never physically deleted source")
	}
}

func TestP252bAutomaticNeverPreservesManualMissingCommand(t *testing.T) {
	c, _, songs := drainFixture(t, 1)
	song := songs[0]
	drainEvidence(t, c, "manual-never", 1)
	drainPin(t, c, song, "manual-never", false)
	asset, token := identityFile(t, c, "m", "/m/manual-never.flac", 1)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, song, asset, compactcatalog.Link{})
	})
	markMissing(t, c, "m", token, time.Now().Add(-time.Hour))
	c.Exec(`INSERT INTO server_settings(singleton,"library.missingTitles") VALUES(1,'never') ON CONFLICT(singleton) DO UPDATE SET "library.missingTitles"='never'`)
	s := New(c.DB)
	cutoff := time.Now()
	if _, e := s.EmptyMissing(context.Background(), "m", cutoff, nil); e != nil {
		t.Fatal(e)
	}
	if out, e := s.EmptyMissingDue(context.Background()); e != nil || out.Titles != 0 {
		t.Fatal(out, e)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE cause_operation='manual-missing' AND status='active'`); n != 1 {
		t.Fatal("automatic policy consumed manual custody", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM mb_song_links WHERE item_id=?`, song); n != 1 {
		t.Fatal("automatic never drained manual pin")
	}
	titles := 0
	pumpFixture(t, "manual missing command under never", pinnedRemovalTurns(1), func() bool {
		out, e := s.EmptyMissing(context.Background(), "m", cutoff, nil)
		if e != nil {
			t.Fatal(e)
		}
		titles += out.Titles
		return !out.More
	})
	if titles != 1 {
		t.Fatal("manual empty-now under never did not finish", titles)
	}
}
