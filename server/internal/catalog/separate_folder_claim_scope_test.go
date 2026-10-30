package catalog

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"reflect"
	"strings"
	"testing"
)

// Split retirement must preserve both unbound claims and the claims for files
// that remain on the original item.
func TestSeparateFolderSplitPreservesUnmovedAndUnboundClaims(t *testing.T) {
	for _, scope := range []string{"unbound", "unmoved-asset"} {
		t.Run(scope, func(t *testing.T) {
			f := newSeparateFolderCommandFixture(t, 1)
			var item int64
			if err := f.c.DB.QueryRow(`SELECT entity_id FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, f.tokens[0]).Scan(&item); err != nil {
				t.Fatal(err)
			}
			asset := ""
			if scope == "unmoved-asset" {
				if err := f.c.DB.QueryRow(`SELECT token FROM catalog_assets WHERE path='/music/flac/01.flac'`).Scan(&asset); err != nil {
					t.Fatal(err)
				}
			}
			f.c.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,source_asset_id,created_ms,updated_ms) VALUES('preserve-prep','p','owner','account','p',?,'music','original','item','queued',?,1,1)`, item, asset)
			h := (&separateFolderRetirementFixture{c: f.c, now: 1700000000000}).hooks()
			ctx := WithSeparateFolderHooks(context.Background(), SeparateFolderHooks{CancelJob: h.CancelJob, Interrupt: h.Interrupt, RevokeReceipt: h.RevokeReceipt})
			public, err := New(f.c.DB).SeparateFolder(ctx, "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
			if err != nil || public == "" {
				t.Fatalf("split: %q %v", public, err)
			}
			if n := rows(t, f.c, `SELECT count(*) FROM download_preparations WHERE id='preserve-prep' AND item_id=? AND source_asset_id=? AND state='queued' AND updated_ms=1`, item, asset); n != 1 {
				t.Fatal("split retired a preparation not bound to the moved asset")
			}
		})
	}
}

func TestSeparateFolderMarkedPreparationReopensForLateChildren(t *testing.T) {
	f := newSeparateFolderRetirementFixture(t)
	f.seedClaims(t)
	f.drive(t, 96)
	if retirementInt(t, f.c, `SELECT separate_retired FROM download_preparations WHERE id='prep-live'`) != 1 {
		t.Fatal("not marked")
	}
	for _, query := range []string{
		`INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms) SELECT 'late-grant',id,profile_key,item_id,1,99 FROM download_preparations WHERE id='prep-live'`,
		`UPDATE download_grants SET revoked=0 WHERE hash='grant-live'`,
		`INSERT INTO download_receipts(id,profile_key,authority,account_id,profile_id,item_id,library_id,preparation_id,quality,publication_required,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,sequence) SELECT 'late-receipt',profile_key,authority,account_id,profile_id,item_id,library_id,preparation_id,quality,publication_required,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,0 FROM download_receipts WHERE id='receipt-live'`,
		`UPDATE download_receipts SET revoked_ms=0 WHERE id='receipt-live'`,
		`INSERT INTO download_request_items(request_id,ordinal,item_id,preparation_id) SELECT 'request-sibling',1,item_id,id FROM download_preparations WHERE id='prep-live'`,
		`UPDATE download_request_items SET reason='' WHERE request_id='request-1' AND ordinal=0`,
		`UPDATE download_preparations SET state='queued' WHERE id='prep-live'`,
	} {
		f.c.Exec(query)
		if retirementInt(t, f.c, `SELECT separate_retired FROM download_preparations WHERE id='prep-live'`) != 0 {
			t.Fatalf("did not reopen: %s", query)
		}
		unit := retirementUnit(t, f.plan, "asset-preparation-drain")
		for n := 0; n < 12; n++ {
			f.c.Write(func(ctx context.Context, tx *sql.Tx) error { _, err := unit.Run(ctx, tx, nil); return err })
			if retirementInt(t, f.c, `SELECT separate_retired FROM download_preparations WHERE id='prep-live'`) == 1 {
				break
			}
		}
		if retirementInt(t, f.c, `SELECT separate_retired FROM download_preparations WHERE id='prep-live'`) != 1 {
			t.Fatal("late debt not drained")
		}
	}
	// Terminal history remains terminal even when it had an old receipt.
	f.c.Exec(`UPDATE download_preparations SET state='expired',reason='expired',separate_retired=0 WHERE id='prep-live'`)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := separateFolderPreparationTurn(ctx, tx, f.item.ID, f.item.Token, f.hooks())
		return err
	})
	if retirementText(t, f.c, `SELECT state||':'||reason FROM download_preparations WHERE id='prep-live'`) != "expired:expired" {
		t.Fatal("terminal history overwritten")
	}
	if SeparateFolderRetirementPlanDigest(f.item.ID, f.item.Token) == SeparateFolderRetirementPlanDigest(f.item.ID, "other-asset") {
		t.Fatal("digest does not bind asset")
	}
}

func TestSeparateFolderWholeMoveAllSevenClaimTablesUnchanged(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 2)
	var item, sibling int64
	if err := f.c.DB.QueryRow(`SELECT entity_id FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, f.tokens[0]).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if err := f.c.DB.QueryRow(`SELECT entity_id FROM catalog_asset_links WHERE asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, f.tokens[1]).Scan(&sibling); err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`DELETE FROM catalog_asset_links WHERE asset_id IN(SELECT id FROM catalog_assets WHERE path='/music/flac/01.flac')`)
	var unmoved string
	if err := f.c.DB.QueryRow(`SELECT token FROM catalog_assets WHERE path='/music/flac/02.flac'`).Scan(&unmoved); err != nil {
		t.Fatal(err)
	}
	claims := &separateFolderRetirementFixture{c: f.c, item: catalogtest.Item{ID: item, Token: f.tokens[0]}, sibling: catalogtest.Item{ID: sibling, Token: unmoved}, now: 1700000000000}
	claims.seedClaims(t)
	before := retirementSnapshot(t, f.c)
	public, err := New(f.c.DB).SeparateFolder(WithSeparateFolderHooks(context.Background(), SeparateFolderHooks{}), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
	if err != nil || public == "" {
		t.Fatalf("whole move: %q %v", public, err)
	}
	if !reflect.DeepEqual(before, retirementSnapshot(t, f.c)) {
		t.Fatal("whole reparent changed claims")
	}
}

func TestSeparateFolderAssetHeadsSkipLongRetiredAndUnmatchedPrefixes(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: pressure/scale proof with 2,000 raw preparation rows")
	}
	f := newSeparateFolderRetirementFixture(t)
	f.c.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<2000)
 INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,source_asset_id,separate_retired,created_ms,updated_ms)
 SELECT printf('prefix-%05d',x),'p','local','a','p',?,'books','original','item','expired',CASE WHEN x<=1000 THEN ? ELSE 'other-asset' END,CASE WHEN x<=1000 THEN 1 ELSE 0 END,1,1 FROM n`, f.item.ID, f.item.Token)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		found, err := separateFolderClaimsExistTx(ctx, tx, f.item.ID, f.item.Token)
		if err != nil {
			return err
		}
		if found {
			t.Fatal("retired/unmatched prefix created debt")
		}
		result, err := separateFolderPreparationTurn(ctx, tx, f.item.ID, f.item.Token, f.hooks())
		if err == nil && !result.Drained {
			t.Fatal("prefix was not drained")
		}
		return err
	})
	plan := retirementExplain(t, f.c, separateAssetPreparationHead, f.item.ID, f.item.Token)
	if !strings.Contains(plan, "separate_asset_preparations") || strings.Contains(plan, "SCAN ") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("unbounded head: %s", plan)
	}
}

func TestSeparateFolderSplitKeepsAllUnmovedAssetClaimsByteIdentical(t *testing.T) {
	f := newSeparateFolderRetirementFixture(t)
	f.seedClaims(t)
	unmoved := f.sibling.Token
	f.c.Exec(`INSERT INTO catalog_asset_links(entity_id,asset_id) SELECT ?,id FROM catalog_assets WHERE token=?`, f.item.ID, unmoved)
	f.c.Exec(`UPDATE prepared_media_jobs SET item_id=?,asset_id=? WHERE id='job-sibling'`, f.item.ID, unmoved)
	f.c.Exec(`UPDATE prepared_media_versions SET item_id=?,asset_id=? WHERE id='version-sibling'`, f.item.ID, unmoved)
	f.c.Exec(`UPDATE download_preparations SET item_id=?,source_asset_id=? WHERE id='prep-sibling'`, f.item.ID, unmoved)
	f.c.Exec(`UPDATE download_grants SET item_id=? WHERE hash='grant-sibling'`, f.item.ID)
	f.c.Exec(`UPDATE download_receipts SET item_id=? WHERE id='receipt-sibling'`, f.item.ID)
	f.c.Exec(`UPDATE download_request_items SET item_id=?,preparation_id='prep-sibling' WHERE request_id='request-sibling'`, f.item.ID)
	snapshot := func() []string {
		var kept []string
		for _, row := range retirementSnapshot(t, f.c) {
			if strings.Contains(row, "sibling") {
				kept = append(kept, row)
			}
		}
		return kept
	}
	before := snapshot()
	f.drive(t, 96)
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("unmoved asset claims changed")
	}
	if retirementText(t, f.c, `SELECT state FROM prepared_media_versions WHERE id='version-published'`) != "deleting" {
		t.Fatal("moved version not retired")
	}
}
