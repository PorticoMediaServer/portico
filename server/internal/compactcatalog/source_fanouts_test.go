package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func sourceTestFinish(t *testing.T, db *sql.DB, domains ...int) {
	t.Helper()
	for i := 0; i < 4000; i++ {
		worked := false
		for _, domain := range domains {
			worked = facetTestTurn(t, db, domain, 1) || worked
		}
		if !worked {
			return
		}
	}
	t.Fatal("upstream sources never settled")
}
func sourceTestAsset(t *testing.T, db *sql.DB, items []int64, path string) int64 {
	t.Helper()
	var asset int64
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		asset, _, err = UpsertAssetTx(ctx, tx, Asset{Path: path, Size: 100, Duration: 60})
		if err != nil {
			return err
		}
		for _, id := range items {
			if err = LinkAssetTx(ctx, tx, id, asset, Link{}); err != nil {
				return err
			}
		}
		return nil
	})
	return asset
}

func TestSourceAssetFanoutAcceptsOneMarkerRollbackResetAndReopen(t *testing.T) {
	db := cc1OpenDB(t, "source-asset.sqlite")
	lib := cc1Library(t, db, "source-asset", "Source asset", "movie", "/source-asset")
	items := make([]int64, 17)
	for i := range items {
		items[i] = facetTestEntity(t, db, lib, fmt.Sprintf("movie:%02d", i))
	}
	asset := sourceTestAsset(t, db, items, "/source-asset/shared.mkv")
	cc1Drain(t, db)
	before := cc1Scalar(t, db, `SELECT sum(source_revision) FROM metadata_publication_heads`)
	cc1Exec(t, db, `UPDATE screen_metadata_work SET status='matched',lease='old',lease_until='future'`)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAssetTx(ctx, tx, asset, map[string]any{"size": int64(200), "available": false})
	})
	if cc1Scalar(t, db, `SELECT sum(source_revision) FROM metadata_publication_heads`) != before {
		t.Fatal("accepting asset write enumerated metadata heads")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, asset) != 1 {
		t.Fatal("asset did not coalesce to one source key")
	}
	if ready, err := PublicationSourcesReady(context.Background(), db); err != nil || ready {
		t.Fatalf("in-flight provider publication admitted: %v %v", ready, err)
	}
	if cc1Scalar(t, db, `SELECT available FROM catalog_assets WHERE id=?`, asset) != 0 || cc1Scalar(t, db, `SELECT available FROM inventory_item_availability WHERE item_id=?`, items[0]) != 0 {
		t.Fatal("raw source availability was delayed")
	}
	checkpoint := groupRows(t, db, `SELECT * FROM catalog_upstream_sources WHERE domain=41`)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := readKeys(tx.QueryContext(context.Background(), `SELECT entity_id,revision FROM catalog_dirty WHERE domain=41 ORDER BY entity_id`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = drainUpstreamSources(context.Background(), tx, 41, keys, 1); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	tx.Rollback()
	if groupRows(t, db, `SELECT * FROM catalog_upstream_sources WHERE domain=41`) != checkpoint || cc1Scalar(t, db, `SELECT sum(source_revision) FROM metadata_publication_heads`) != before {
		t.Fatal("fanout rollback split checkpoint/output")
	}
	// Evidence/frontier phases and FI1's informational pre-fence/apply publish
	// no owner head. Step the real source through them; the first owner cursor
	// step must publish exactly one head and only after clearing the FI1 bit.
	for turn := 0; turn < 8*(len(items)+5); turn++ {
		if !facetTestTurn(t, db, 41, 1) {
			t.Fatal("FI1 source stopped before its owner cursor")
		}
		head := cc1Scalar(t, db, `SELECT sum(source_revision) FROM metadata_publication_heads`)
		if head > before+1 {
			t.Fatal("one raw edge exceeded its metadata output budget")
		}
		if head != before+1 {
			continue
		}
		if flags := cc1Scalar(t, db, `SELECT flags FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, asset); flags&sourceInfo != 0 {
			t.Fatal("FI1 informational work published an owner head")
		}
		break
	}
	if cc1Scalar(t, db, `SELECT sum(source_revision) FROM metadata_publication_heads`) != before+1 {
		t.Fatal("FI1 source never reached its owner cursor")
	}
	// Re-touch preserves this finite pass and owes one rerun after its frontier.
	cc1Exec(t, db, `UPDATE catalog_assets SET size=300 WHERE id=?`, asset)
	if cc1Scalar(t, db, `SELECT has_after FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, asset) != 1 {
		t.Fatal("source re-touch reset the finite raw cursor")
	}
	facetTestTurn(t, db, 41, 1)
	var seq int
	var name, path string
	if err = db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 2*(len(items)+5); i++ {
		old := cc1Scalar(t, db, `SELECT sum(source_revision) FROM metadata_publication_heads`)
		if !facetTestTurn(t, db, 41, 1) {
			break
		}
		if next := cc1Scalar(t, db, `SELECT sum(source_revision) FROM metadata_publication_heads`); next-old > 1 {
			t.Fatal("resumed fanout exceeded one raw edge")
		}
	}
	// Missing projection waits for source41 and its informational obligation;
	// let the real scheduler discharge both in their production order.
	cc1Drain(t, db)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_asset_links WHERE asset_id=? AND available=0`, asset) != 17 {
		t.Fatal("availability consumers lost a raw target")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM screen_metadata_work WHERE status='pending' AND lease=''`) != 17 {
		t.Fatal("screen wake/lease semantics lost targets")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_missing_titles`) != 17 {
		t.Fatal("bounded presence reducer missed targets")
	}
	if ready, err := PublicationSourcesReady(context.Background(), db); err != nil || !ready {
		t.Fatalf("completed source stayed unavailable: %v %v", ready, err)
	}
}

func TestSourceInventoryOldNewOwnersAndLiveAuthority(t *testing.T) {
	db := cc1OpenDB(t, "source-inventory.sqlite")
	lib := cc1Library(t, db, "source-inventory", "Source inventory", "movie", "/source-inventory")
	a := facetTestEntity(t, db, lib, "movie:a")
	b := facetTestEntity(t, db, lib, "movie:b")
	aa := sourceTestAsset(t, db, []int64{a}, "/source-inventory/a.mkv")
	bb := sourceTestAsset(t, db, []int64{b}, "/source-inventory/b.mkv")
	cc1Exec(t, db, `INSERT INTO library_sources(id,library_id,configured_root,root,identity_confirmed) VALUES('source','source-inventory','/source-inventory','/source-inventory',1) ON CONFLICT(library_id,root) DO UPDATE SET identity_confirmed=1`)
	var source string
	if err := db.QueryRow(`SELECT id FROM library_sources WHERE library_id='source-inventory' AND root='/source-inventory'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	cc1Drain(t, db)
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
 SELECT 'object',s.id,a.token,s.incarnation,'a.mkv','one','{}',1,1,'missing' FROM catalog_assets a CROSS JOIN library_sources s WHERE a.id=? AND s.id=?`, aa, source)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`) != 0 {
		t.Fatal("inventory insert enumerated item targets")
	}
	cc1Exec(t, db, `UPDATE inventory_objects SET asset_id=(SELECT token FROM catalog_assets WHERE id=?) WHERE id='object'`, bb)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_upstream_sources WHERE domain=41 AND source_id IN(?,?)`, aa, bb) != 2 {
		t.Fatal("inventory repoint missed old/new asset source")
	}
	cc1Drain(t, db)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_missing_titles WHERE entity_id=?`, a) != 0 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_missing_titles WHERE entity_id=?`, b) != 1 {
		t.Fatal("inventory repoint changed presence semantics")
	}
	cc1Exec(t, db, `UPDATE inventory_objects SET state='available' WHERE id='object'`)
	cc1Drain(t, db)
	cc1Exec(t, db, `UPDATE library_sources SET enabled=0 WHERE id=?`, source)
	// Permission/root source state and the live availability view change NOW,
	// before retained/background browse outputs have had any turn.
	if cc1Scalar(t, db, `SELECT enabled FROM library_sources WHERE id=?`, source) != 0 || cc1Scalar(t, db, `SELECT available FROM inventory_item_availability WHERE item_id=?`, b) != 0 {
		t.Fatal("source revocation delayed behind a worker")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=28 AND entity_id=?`, lib) != 1 {
		t.Fatal("source health lost its library marker")
	}
	cc1Drain(t, db)
	if cc1Scalar(t, db, `SELECT available FROM catalog_asset_links WHERE entity_id=?`, b) != 0 {
		t.Fatal("derived availability failed to catch up")
	}
}

func TestSourceLibraryRejectCursorAndParentFanoutAreBounded(t *testing.T) {
	db := cc1OpenDB(t, "source-parent.sqlite")
	lib := cc1Library(t, db, "source-parent", "Source parent", "tv", "/source-parent")
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show", Title: "Show"}, map[string]any{"local_key": "show"})
	for i := 0; i < 17; i++ {
		cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Key: fmt.Sprintf("ep:%d", i), Title: "Episode"}, map[string]any{"show_id": show, "numbering": "seasonal", "number": i + 1})
	}
	cc1Drain(t, db)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchTx(ctx, tx, 28, lib) })
	facetTestTurn(t, db, 28, 1)
	// The first row is a rejected show. It still advances, with no output20.
	if cc1Scalar(t, db, `SELECT after FROM catalog_fanouts WHERE domain=28 AND key=?`, lib) != int(show) || cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`) != 0 {
		t.Fatal("library cursor filtered before raw LIMIT")
	}
	sourceTestFinish(t, db, 28)
	cc1Drain(t, db)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchEntityTx(ctx, tx, show) })
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=17`) != 1 {
		t.Fatal("parent TouchEntity enumerated episodes in accepting transaction")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_upstream_sources WHERE domain=42 AND source_id=?`, show) != 1 {
		t.Fatal("parent source marker missing")
	}
	for i := 0; i < 40; i++ {
		old := cc1Scalar(t, db, `SELECT COALESCE(sum(revision),0) FROM catalog_dirty WHERE domain=17`)
		if !facetTestTurn(t, db, 42, 1) {
			break
		}
		if next := cc1Scalar(t, db, `SELECT COALESCE(sum(revision),0) FROM catalog_dirty WHERE domain=17`); next-old > 1 {
			t.Fatal("parent fanout exceeded total raw quantum")
		}
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=17`) != 18 {
		t.Fatal("parent raw source cursor lost episodes")
	}
}

func TestSourceGenerationWaitsAndReportsPoison(t *testing.T) {
	db := cc1OpenDB(t, "source-generation.sqlite")
	lib := cc1Library(t, db, "source-generation", "Source generation", "movie", "/source-generation")
	id := facetTestEntity(t, db, lib, "movie:generation")
	cc1Drain(t, db)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := beginGeneration(ctx, tx); err != nil {
			return err
		}
		return TouchTx(ctx, tx, 33, id)
	})
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return finishGeneration(ctx, tx) })
	if cc1Scalar(t, db, `SELECT retaining FROM catalog_generations`) != 1 {
		t.Fatal("generation released before facet checkpoint")
	}
	cc1Exec(t, db, `INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(33,?,'producer test',0)`, id)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return finishGeneration(ctx, tx) })
	if cc1Scalar(t, db, `SELECT retaining FROM catalog_generations`) != 1 {
		t.Fatal("poison exposed a partial accepted projection")
	}
	if _, err := CaptureFacetProducerFence(context.Background(), db); !errors.Is(err, ErrFacetProducerFailed) {
		t.Fatalf("poison did not report truthful failure: %v", err)
	}
	cc1Exec(t, db, `DELETE FROM catalog_derivation_failures WHERE domain=33 AND entity_id=?`, id)
	cc1Drain(t, db)
	if cc1Scalar(t, db, `SELECT retaining FROM catalog_generations`) != 0 {
		t.Fatal("source/facet dependencies retained forever after recovery")
	}
}

func TestSourceLibraryKeepsMetadataTVDBHeadInvalidation(t *testing.T) {
	db := cc1OpenDB(t, "source-library-heads.sqlite")
	lib := cc1Library(t, db, "source-library-heads", "Shows", "tv", "/shows")
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show", Title: "Show"}, map[string]any{"local_key": "show"})
	ep := cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Key: "episode", Title: "Episode"}, map[string]any{"show_id": show, "numbering": "seasonal", "number": 1})
	cc1Drain(t, db)
	cc1Exec(t, db, `INSERT INTO tvdb_publication_heads(show_id) VALUES(?) ON CONFLICT DO NOTHING`, show)
	metadataBefore := cc1Scalar(t, db, `SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, ep)
	tvdbBefore := cc1Scalar(t, db, `SELECT source_revision FROM tvdb_publication_heads WHERE show_id=?`, show)
	cc1Exec(t, db, `UPDATE libraries SET root='/shows-new' WHERE id='source-library-heads'`)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_upstream_sources WHERE domain=43 AND source_id=? AND (flags&68)=68`, lib) != 1 {
		t.Fatal("library edit lost metadata/TVDB source marker")
	}
	if ready, err := PublicationSourcesReady(context.Background(), db); err != nil || ready {
		t.Fatalf("deferred source heads admitted stale publication: %v %v", ready, err)
	}

	// A persisted pre-correction phase0 screen cursor is not an entity
	// continuation. Its large target ID must not skip metadata/TVDB heads.
	cc1Exec(t, db, `UPDATE catalog_upstream_sources SET phase=0,has_after=1,after_id=9223372036854775807,after_key='item' WHERE domain=43 AND source_id=?`, lib)
	sourceTestFinish(t, db, 43)
	if cc1Scalar(t, db, `SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, ep) <= metadataBefore {
		t.Fatal("metadata source head not invalidated")
	}
	if cc1Scalar(t, db, `SELECT source_revision FROM tvdb_publication_heads WHERE show_id=?`, show) <= tvdbBefore {
		t.Fatal("TVDB source head not invalidated")
	}
	if ready, err := PublicationSourcesReady(context.Background(), db); err != nil || !ready {
		t.Fatalf("settled head work not accepted: %v %v", ready, err)
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM libraries WHERE id='source-library-heads' AND root='/shows-new'`) != 1 {
		t.Fatal("live library source state delayed")
	}
}

func TestSourceAssetEvidenceConvergesThroughChurnAndCapturedTail(t *testing.T) {
	db := cc1OpenDB(t, "source-evidence-churn.sqlite")
	lib := cc1Library(t, db, "evidence", "Evidence", "movie", "/evidence")
	item := facetTestEntity(t, db, lib, "evidence:item")
	asset := sourceTestAsset(t, db, []int64{item}, "/evidence/shared.mkv")
	cc1Drain(t, db)
	token := availabilityToken(t, db, asset)
	cc1Exec(t, db, `WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<24)
 INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
 SELECT printf('object-%03d',i),s.id,?,s.incarnation,printf('file-%03d.mkv',i),'r','{}',1,1,'missing' FROM n CROSS JOIN library_sources s WHERE s.id='evidence'`, token)
	facetTestTurn(t, db, 41, 1) // capture both frontiers
	captured := cc1Scalar(t, db, `SELECT pass_revision FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, asset)
	if captured == 0 {
		t.Fatal("source did not capture its revision")
	}
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
 SELECT 'zz-tail',s.id,?,s.incarnation,'tail.mkv','r','{}',1,1,'available' FROM library_sources s WHERE s.id='evidence'`, token)
	ended := false
	for turn := 0; turn < 40; turn++ {
		// An accepting edit before every turn must never reset the captured pass.
		cc1Exec(t, db, `UPDATE inventory_objects SET retired=1-retired WHERE id='object-001'`)
		facetTestTurn(t, db, 41, 1)
		if cc1Scalar(t, db, `SELECT phase FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, asset) == 0 {
			ended = true
			break
		}
		if got := cc1Scalar(t, db, `SELECT pass_revision FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, asset); got != captured {
			t.Fatalf("churn reset revision %d to %d", captured, got)
		}
	}
	if !ended {
		t.Fatal("captured pass did not finish under continuous same-asset churn")
	}
	sourceTestFinish(t, db, 41, 44, 20)
	cc1Drain(t, db)
	availabilityParity(t, db)
	if got := cc1Scalar(t, db, `SELECT available FROM catalog_assets WHERE id=?`, asset); got != 1 {
		t.Fatal("rerun lost appended positive evidence")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_missing_titles WHERE entity_id=?`, item) != 0 {
		t.Fatal("positive evidence kept title missing")
	}
	// The final missing mark has the same exact eventual result as the former
	// synchronous aggregate, after all source/holder/missing work drains.
	cc1Exec(t, db, `UPDATE inventory_objects SET state='missing',retired=0 WHERE asset_id=?`, token)
	sourceTestFinish(t, db, 41, 44, 20)
	cc1Drain(t, db)
	availabilityParity(t, db)
	want := cc1Scalar(t, db, `SELECT available FROM inventory_asset_availability WHERE asset_id=?`, token)
	got := cc1Scalar(t, db, `SELECT available FROM catalog_assets WHERE id=?`, asset)
	if got != want || got != 0 {
		t.Fatalf("asset aggregate got %d, synchronous oracle %d", got, want)
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_missing_titles WHERE entity_id=?`, item) != 1 {
		t.Fatal("all-missing evidence did not settle missing title")
	}
}

func TestSourceAssetEvidenceIgnoresUnrelatedImportsAndFencesConfig(t *testing.T) {
	for _, change := range []string{"unrelated", "config"} {
		t.Run(change, func(t *testing.T) {
			db := cc1OpenDB(t, "source-config-"+change+".sqlite")
			lib := cc1Library(t, db, "config", "Config", "movie", "/config")
			item := facetTestEntity(t, db, lib, "config:item")
			asset := sourceTestAsset(t, db, []int64{item}, "/config/file.mkv")
			cc1Drain(t, db)
			token := availabilityToken(t, db, asset)
			cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
   SELECT 'object',id,?,incarnation,'file.mkv','r','{}',1,1,'missing' FROM library_sources WHERE id='config'`, token)
			facetTestTurn(t, db, 41, 1) // capture frontiers and one raw missing object
			epoch := cc1Scalar(t, db, `SELECT availability_epoch FROM catalog_facet_source_state`)
			facetTestTurn(t, db, 41, 1) // EOF
			if phase := cc1Scalar(t, db, `SELECT phase FROM catalog_upstream_sources WHERE domain=41 AND source_id=?`, asset); phase != 2 {
				t.Fatalf("configuration edit must precede publication, got phase %d", phase)
			}
			if change == "config" {
				cc1Exec(t, db, `UPDATE library_sources SET enabled=0 WHERE id='config'`)
			} else {
				for i := 0; i < 8; i++ {
					facetTestEntity(t, db, lib, fmt.Sprintf("unrelated:%d", i))
				}
				if cc1Scalar(t, db, `SELECT availability_epoch FROM catalog_facet_source_state`) != epoch {
					t.Fatal("unrelated import moved configuration witness")
				}
			}
			facetTestTurn(t, db, 41, 1) // publication is one same-writer point
			got := cc1Scalar(t, db, `SELECT available FROM catalog_assets WHERE id=?`, asset)
			if change == "config" {
				if got != 1 {
					t.Fatal("configuration drift published mixed evidence")
				}
			} else if got != 0 {
				t.Fatal("unrelated imports starved exact evidence publication")
			}
			sourceTestFinish(t, db, 41, 44, 20)
			cc1Drain(t, db)
			if cc1Scalar(t, db, `SELECT available FROM catalog_assets WHERE id=?`, asset) != 0 {
				t.Fatal("final evidence did not converge")
			}
		})
	}
}
