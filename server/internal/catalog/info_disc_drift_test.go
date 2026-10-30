package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestInfoDiscRepointDischargesAndNormalProducerReanalyzes(t *testing.T) {
	for _, late := range []bool{false, true} {
		name := "before-materialize"
		if late {
			name = "before-activate"
		}
		t.Run(name, func(t *testing.T) { testInfoDiscRepointReanalyzes(t, late) })
	}
}

func testInfoDiscRepointReanalyzes(t *testing.T, late bool) {
	s, db, source, job, object, policy, probe := discPublicationFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, db)
	var oldAsset int64
	var oldToken, revision string
	var size, modified int64
	if err := db.QueryRow(`SELECT a.id,a.token,o.revision,o.size,o.modified_ns FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=?`, object).Scan(&oldAsset, &oldToken, &revision, &size, &modified); err != nil {
		t.Fatal(err)
	}
	oldItem := c.Entity(compactcatalog.Entity{Library: c.Handle(source.LibraryID), Kind: compactcatalog.Movie, Key: "disc-old", Title: "Old disc"}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, oldItem.ID, oldAsset, compactcatalog.Link{})
	})
	first, err := acceptDiscProbeForTest(t, s, ctx, job, object, revision, policy, probe)
	if err != nil {
		t.Fatal(err)
	}
	c.Drain()
	var acceptedFingerprint string
	if err = db.QueryRow(`SELECT fingerprint FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&acceptedFingerprint); err != nil {
		t.Fatal(err)
	}
	probe.Titles[0].DurationMs += 123000
	pending, err := acceptDiscProbeForTest(t, s, ctx, job, object, revision, policy, probe)
	if err != nil || pending.State != compactcatalog.InfoInputAcceptedPending || pending.ExpectedEpoch <= first.ExpectedEpoch {
		t.Fatal("new accepted input", pending, err)
	}
	// The exact NEW owner is protected before the first materializer admission.
	if _, err = db.Exec(`DELETE FROM inventory_objects WHERE id=?`, object); err == nil {
		t.Fatal("accepted target was not retained")
	}
	if late {
		for i := 0; i < 500; i++ {
			var phase int
			if err = db.QueryRow(`SELECT phase FROM catalog_info_changes WHERE id=?`, pending.ChangeID).Scan(&phase); err != nil {
				t.Fatal(err)
			}
			if phase == 3 {
				break
			}
			if i == 499 {
				t.Fatal("materializer never reached activation boundary")
			}
			err = dbwork.WithWriteTxContext(ctx, db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
				_, e := compactcatalog.AdvanceInfoPublicationTx(ctx, tx, oldAsset, oldToken, compactcatalog.NewInfoMaterializer())
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if err = db.QueryRow(`SELECT fingerprint FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&acceptedFingerprint); err != nil {
			t.Fatal(err)
		}
	}
	newToken := identity.Token()
	var newAsset int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		newAsset, e = compactcatalog.CreateAssetTx(ctx, tx, newToken, compactcatalog.Asset{Path: "/replacement/VIDEO_TS", Size: size, ModifiedNS: modified, Container: "disc"}, false)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE inventory_objects SET asset_id=?,analysis_revision=revision,analysis_policy=?,analysis_operations_json='["probe"]',analysis_state='done' WHERE id=?`, newToken, policy, object)
		return e
	})
	newItem := c.Entity(compactcatalog.Entity{Library: c.Handle(source.LibraryID), Kind: compactcatalog.Movie, Key: "disc-new", Title: "Replacement disc"}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, newItem.ID, newAsset, compactcatalog.Link{})
	})
	c.Exec(`INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?)`, source.ID, job)
	// Finish just the accepted old input. No replacement producer runs in this
	// loop, so neither discharge nor a fabricated probe can supply its facts.
	done := false
	for i := 0; i < 1000 && !done; i++ {
		err = dbwork.WithWriteTxContext(ctx, db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
			var e error
			done, e = compactcatalog.AdvanceInfoPublicationTx(ctx, tx, oldAsset, oldToken, compactcatalog.NewInfoMaterializer())
			return e
		})
		if err != nil {
			t.Fatal("discharge wedged accepted input", err)
		}
	}
	if !done {
		t.Fatal("vanished obligation did not retire")
	}
	var epoch, changes int64
	if err = db.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, oldAsset).Scan(&epoch); err != nil || epoch != pending.ExpectedEpoch+1 {
		t.Fatal("accepted epoch must advance once", epoch, pending, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE id=?`, pending.ChangeID).Scan(&changes); err != nil || changes != 0 {
		t.Fatal("unused NEW input not reclaimed", changes, err)
	}
	var token, fingerprint string
	if err = db.QueryRow(`SELECT asset_id,fingerprint FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&token, &fingerprint); err != nil || token != oldToken || fingerprint != acceptedFingerprint {
		t.Fatal("old NEW facts written to replacement", token, fingerprint, err)
	}
	var queued bool
	if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_analysis_queue q JOIN inventory_objects o ON o.id=q.object_id WHERE q.job_id=? AND q.object_id=? AND q.revision=o.revision AND q.state='pending' AND o.analysis_revision='' AND o.analysis_state='pending')`, job, object).Scan(&queued); err != nil || !queued {
		t.Fatal("replacement has no normal reanalysis obligation", queued, err)
	}
	// The ordinary structure producer, with its real object/job/policy fence,
	// now consumes that observation and replaces the old object's complete tree.
	replacement, err := acceptDiscProbeForTest(t, s, ctx, job, object, revision, policy, probe)
	if err != nil || replacement.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("normal replacement producer", replacement, err)
	}
	c.Drain()
	if err = db.QueryRow(`SELECT asset_id FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&token); err != nil || token != newToken {
		t.Fatal("replacement facts never arrived", token, err)
	}
	got, err := ReadInventoryDiscProbe(ctx, db, object, revision)
	if err != nil || len(got.Titles) != len(probe.Titles) || got.Titles[0].DurationMs != probe.Titles[0].DurationMs {
		t.Fatal("normal replacement observation differs", got, err)
	}
	var pendingCount int64
	if err = db.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id IN(?,?)`, oldAsset, newAsset).Scan(&pendingCount); err != nil || pendingCount != 0 {
		t.Fatal("publication left wedged input", pendingCount, err)
	}
}
