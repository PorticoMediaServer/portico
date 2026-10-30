package compactcatalog_test

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"testing"
)

func TestInfoDiscSubtitleApplyKeepsAcceptedStatThroughRawSuccessor(t *testing.T) {
	f := newDiscProjectionFixture(t, 1)
	var size, modified int64
	if err := f.c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, f.asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	mask, families := f.facetStream(f.object, f.token, f.revision, "accepted-new1", discProjectionStreams())
	change := f.publish(mask, families)
	first := false
	for i := 0; i < 500; i++ {
		f.advance(change)
		var n int
		if err := f.c.DB.QueryRow(`SELECT count(*) FROM subtitle_resources WHERE source_id=?`, f.token).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			first = true
			break
		}
	}
	if !first {
		t.Fatal("first accepted stream never applied")
	}
	// The physical observation and raw stat advance after one resource quantum.
	// The sealed NEW1 input must still project its second stream with NEW1 stat.
	f.c.Exec(`UPDATE inventory_objects SET revision='raw-new2' WHERE id=?`, f.object)
	f.c.Exec(`UPDATE catalog_assets SET size=size+7,modified_ns=modified_ns+11 WHERE id=?`, f.asset)
	var successor bool
	if err := f.c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_info_changes WHERE asset_id=? AND slot=1)`, f.asset).Scan(&successor); err != nil || !successor {
		t.Fatal("raw successor missing", successor, err)
	}
	retired := false
	for i := 0; i < 500; i++ {
		f.advance(change)
		var exists bool
		if err := f.c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_info_changes WHERE id=?)`, change).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			retired = true
			break
		}
	}
	if !retired {
		t.Fatal("accepted NEW1 never retired before deferred NEW2")
	}
	var count, bad int
	if err := f.c.DB.QueryRow(`SELECT count(*),COALESCE(sum(r.source_size<>? OR r.source_modified_ns<>? OR d.source_revision<>?),0) FROM subtitle_resources s JOIN subtitle_revisions r ON r.resource_id=s.id AND r.revision=s.current_revision JOIN subtitle_disc_resources d ON d.resource_id=s.id WHERE s.source_id=?`, size, modified, f.revision, f.token).Scan(&count, &bad); err != nil || count != 2 || bad != 0 {
		t.Fatal("accepted resource frame changed with raw NEW2", count, bad, err)
	}
}

func TestInfoDiscDischargeStillAppliesLiveTechnicalFamilies(t *testing.T) {
	f := newDiscProjectionFixture(t, 1)
	var size, modified, epoch int64
	if err := f.c.DB.QueryRow(`SELECT size,modified_ns,info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&size, &modified, &epoch); err != nil {
		t.Fatal(err)
	}
	mask, families := f.facetStream(f.object, f.token, f.revision, "vanished-disc", discProjectionStreams())
	families = append([]compactcatalog.InfoSourceFamily{
		{Number: 1, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			return compactcatalog.InfoStreamFactRow{InputOrdinal: i, AssetID: f.token, Revision: 4, Size: size, ModifiedNS: modified, Fingerprint: "new-technical", DetailVersion: 1}, nil
		}},
		{Number: 2, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
			return compactcatalog.InfoStreamRow{InputOrdinal: i, AssetID: f.token, StreamIndex: 7, Type: "audio", Codec: "aac", Language: "eng", DetailJSON: "{}"}, nil
		}},
		{Number: 3, Count: 0},
	}, families...)
	change := f.publish(mask|7, families)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := compactcatalog.CreateAssetTx(ctx, tx, "replacement-disc-token", compactcatalog.Asset{Path: "/replacement-disc", Size: size, ModifiedNS: modified, Container: "disc"}, false)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET asset_id='replacement-disc-token' WHERE id=?`, f.object)
		return err
	})
	f.drive(change)
	var revision, installed, current int64
	if err := f.c.DB.QueryRow(`SELECT revision FROM asset_stream_facts WHERE asset_id=?`, f.token).Scan(&revision); err != nil || revision != 4 {
		t.Fatal("live technical head was discharged", revision, err)
	}
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=? AND stream_index=7 AND codec='aac'`, f.token).Scan(&installed); err != nil || installed != 1 {
		t.Fatal("live technical child was discharged", installed, err)
	}
	if err := f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&current); err != nil || current != epoch+1 {
		t.Fatal("accepted input did not advance exactly once", current, epoch, err)
	}
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM inventory_disc_facts WHERE object_id=?`, f.object).Scan(&installed); err != nil || installed != 0 {
		t.Fatal("vanished NEW disc facts reached replacement", installed, err)
	}
}
