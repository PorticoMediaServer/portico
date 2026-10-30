package compactcatalog_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestInfoInvalidationSameStatReplacementPrefencesBeforeClearingTechnicalRows(t *testing.T) {
	f := newInfoPublicationFixture(t, 2)
	var size, modified, epoch int64
	if e := f.c.DB.QueryRow(`SELECT size,modified_ns,info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&size, &modified, &epoch); e != nil {
		t.Fatal(e)
	}
	f.c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES(?,1,?,?,'original')`, f.token, size, modified)
	for i := 0; i < 96; i++ {
		f.c.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,?,'audio','aac','','',2,'stereo',0,0)`, f.token, i)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if e := compactcatalog.SetAssetTx(ctx, tx, f.asset, map[string]any{"duration": 0, "video_codec": "", "audio_codec": ""}); e != nil {
			return e
		}
		return compactcatalog.InvalidateInfoTechnicalTx(ctx, tx, f.asset)
	})
	var count int
	if e := f.c.DB.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, f.token).Scan(&count); e != nil || count != 96 {
		t.Fatal("accepting invalidation erased OLD", count, e)
	}
	for _, item := range f.items {
		if !f.head(t, item).Ready() {
			t.Fatal("OLD became unready before prefence")
		}
	}
	ctx, stats := dbwork.Measure(context.Background())
	worker := compactcatalog.NewWorker(f.c.DB)
	worker.PaceToHold(time.Millisecond)
	dbwork.ResetPlans()
	for turn := 0; turn < 1000; turn++ {
		more, e := worker.Step(ctx, 1)
		if e != nil {
			t.Fatal(e)
		}
		plan := dbwork.Plans()
		if plan.Overruns != 0 || plan.Largest.Rows > 160 || plan.Largest.Pages > 256 || plan.Largest.VMOps > 250000 {
			t.Fatalf("actual producer quantum exceeded unchanged plan: %+v", plan)
		}
		var left int
		f.c.DB.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, f.token).Scan(&left)
		if left > 0 && left < 96 {
			for _, item := range f.items {
				if f.head(t, item).Ready() {
					t.Fatal("partial canonical set escaped ready", left)
				}
			}
		}
		if more == 0 {
			break
		}
		if turn == 999 {
			rows, e := f.c.DB.Query(`SELECT domain,source_id,flags,revision,pass_revision,phase,after_id,after_aux FROM catalog_upstream_sources`)
			if e == nil {
				defer rows.Close()
				for rows.Next() {
					var d, id, flags, rev, pass, phase, after, rerun int64
					rows.Scan(&d, &id, &flags, &rev, &pass, &phase, &after, &rerun)
					t.Log("source", d, id, flags, rev, pass, phase, after, rerun)
				}
			} else {
				t.Log("diagnostic", e)
			}
			rows, e = f.c.DB.Query(`SELECT id,slot,phase,family_mask,copy_family,copy_phase,copy_aux,input_revision FROM catalog_info_changes WHERE asset_id=?`, f.asset)
			if e == nil {
				defer rows.Close()
				for rows.Next() {
					var a, b, c, d, e, g, h, i int64
					rows.Scan(&a, &b, &c, &d, &e, &g, &h, &i)
					t.Log("input", a, b, c, d, e, g, h, i)
				}
			}
			t.Log("remaining streams", left)
			t.Fatal("invalidation did not finish")
		}
	}
	f.c.DB.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, f.token).Scan(&count)
	var after int64
	f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&after)
	if count != 0 || after != epoch+1 {
		t.Fatal(count, after, epoch)
	}
	for _, item := range f.items {
		if !f.head(t, item).Ready() {
			t.Fatal("unknown baseline not applied", f.head(t, item))
		}
	}
	var pending int
	f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, f.asset).Scan(&pending)
	if pending != 0 {
		t.Fatal("source input not reclaimed", pending)
	}
	t.Logf("same-stat unknown publication %+v", stats())
}

func TestInfoFrozenOldLastHolderRetirementReclaimsCharge(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	var epoch int64
	f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&epoch)
	f.c.Exec(`UPDATE catalog_assets SET duration=5700 WHERE id=?`, f.asset)
	spec := compactcatalog.InfoInputSpec{AssetID: f.asset, Token: f.token, ExpectedEpoch: epoch, Fingerprint: [32]byte{1}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{1}}}}
	in := infoInputBegin(t, f.c, spec)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, e := compactcatalog.AbandonInfoInputTx(ctx, tx, in.Ref)
		return e
	})
	for i := 0; i < 100; i++ {
		if f.step(t) {
			break
		}
	}
	var frozen int
	f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id=? AND copy_aux=-2`, f.asset).Scan(&frozen)
	if frozen != 1 {
		t.Fatal(frozen)
	}
	f.c.Exec(`DELETE FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, f.items[0], f.asset)
	catalogtest.New(t, f.c.DB).Drain()
	f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, f.asset).Scan(&frozen)
	if frozen != 0 {
		t.Fatal("last-holder OLD leaked", frozen)
	}
}
