package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func facetTestTurn(t *testing.T, db *sql.DB, domain, limit int) bool {
	t.Helper()
	var worked bool
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		keys, err := readKeys(tx.QueryContext(ctx, `SELECT entity_id,revision FROM catalog_dirty WHERE domain=? ORDER BY entity_id LIMIT 8`, domain))
		if err != nil || len(keys) == 0 {
			return err
		}
		worked = true
		d, ok := derivation(domain)
		if !ok {
			return fmt.Errorf("missing domain %d", domain)
		}
		done, err := d.Drain(ctx, tx, keys, limit)
		if err != nil {
			return err
		}
		for _, id := range done {
			for _, key := range keys {
				if id == key.ID {
					if err = acknowledgeDerived(ctx, tx, domain, id, key.Revision); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	return worked
}

func facetTestDrain(t *testing.T, db *sql.DB, limit int) {
	t.Helper()
	for i := 0; i < 4000; i++ {
		worked := false
		for _, d := range []int{41, 42, 43, 44, 36, 37, 38, 39, 40, 33} {
			worked = facetTestTurn(t, db, d, limit) || worked
		}
		if !worked {
			if err := CheckFacetCounts(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("facet producer did not settle")
}

func facetTestEntity(t *testing.T, db *sql.DB, lib int64, key string) int64 {
	t.Helper()
	return cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: key, Title: key}, nil)
}

func facetTestMembers(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	return groupRows(t, db, fmt.Sprintf(`SELECT field,value,library_id,rating_key,label_key,recent_key FROM catalog_facet_members WHERE entity_id=%d ORDER BY field,value`, id))
}

func TestFacetProducerStagedRawCursorsRestartAndAtomicMerge(t *testing.T) {
	db := cc1OpenDB(t, "facet-stage.sqlite")
	lib := cc1Library(t, db, "facet-stage", "Facet stage", "movie", "/facet-stage")
	id := facetTestEntity(t, db, lib, "movie:stage")
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, id, "studio", []string{"old"})
	})
	cc1Drain(t, db)
	before := facetTestMembers(t, db, id)
	values := make([]string, 19)
	for i := range values {
		values[i] = fmt.Sprintf("studio %02d", i)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := SetAttributesTx(ctx, tx, id, "studio", values); err != nil {
			return err
		}
		// Rejected fields sort after accepted terms and still consume raw quanta.
		if err := SetAttributesTx(ctx, tx, id, "not-a-facet", []string{"reject 1", "reject 2", "reject 3"}); err != nil {
			return err
		}
		return SetAttributesTx(ctx, tx, id, "network", []string{"", "network"})
	})
	reached := false
	for i := 0; i < 100; i++ {
		facetTestTurn(t, db, 33, 1)
		var phase int
		var after int64
		if err := db.QueryRow(`SELECT phase,after_id FROM catalog_facet_work WHERE entity_id=?`, id).Scan(&phase, &after); err != nil {
			t.Fatal(err)
		}
		if phase == facetHolderAttributes && after > 0 {
			reached = true
			break
		}
		if got := facetTestMembers(t, db, id); got != before {
			t.Fatalf("accepted projection changed during raw staging: %s", got)
		}
	}
	if !reached {
		t.Fatal("did not reach attribute subcursor")
	}
	// Rollback BOTH staged data and its checkpoint. There is no memory cursor.
	checkpoint := groupRows(t, db, `SELECT * FROM catalog_facet_work`)
	stage := groupRows(t, db, `SELECT * FROM catalog_facet_stage ORDER BY 1,2,3,4`)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := readKeys(tx.QueryContext(context.Background(), `SELECT entity_id,revision FROM catalog_dirty WHERE domain=33 ORDER BY entity_id`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = drainFacetHolders(context.Background(), tx, keys, 1); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	tx.Rollback()
	if got := groupRows(t, db, `SELECT * FROM catalog_facet_work`); got != checkpoint {
		t.Fatal("rollback advanced checkpoint")
	}
	if got := groupRows(t, db, `SELECT * FROM catalog_facet_stage ORDER BY 1,2,3,4`); got != stage {
		t.Fatal("rollback retained staged output")
	}
	// An edit behind the raw cursor restarts by dirty revision/source epoch.
	cc1Exec(t, db, `UPDATE catalog_item_attribute_edges SET source_value='replaced' WHERE item_id=? AND term_id=(SELECT min(term_id) FROM catalog_item_attribute_edges WHERE item_id=?)`, id, id)
	// Reopen the real database while staging is incomplete.
	var seq int
	var name, path string
	if err = db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Every quantum writes at most one normalized stage/member row under limit 1.
	for i := 0; i < 1000; i++ {
		oldMembers := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=?`, id)
		oldStage := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id)
		if !facetTestTurn(t, db, 33, 1) {
			break
		}
		newMembers := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=?`, id)
		newStage := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id)
		if newMembers-oldMembers > 1 || oldMembers-newMembers > 1 || newStage-oldStage > 1 || oldStage-newStage > 1 {
			t.Fatal("one raw/output/cleanup quantum exceeded its row budget")
		}
		if i == 999 {
			t.Fatal("staged holder never completed")
		}
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND field='studio'`, id); got != 19 {
		t.Fatalf("studio count=%d", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND value='replaced'`, id); got != 1 {
		t.Fatalf("lost edit behind cursor: %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND (field='not-a-facet' OR value='')`, id); got != 0 {
		t.Fatal("rejected source leaked")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage`) + cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_work`); got != 0 {
		t.Fatal("completed holder left staging")
	}
	if err = CheckFacetCounts(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestFacetProducerSourceFanoutFullKeysBarrierAndRevision(t *testing.T) {
	db := cc1OpenDB(t, "facet-source.sqlite")
	lib := cc1Library(t, db, "facet-source", "Facet source", "movie", "/facet-source")
	a := facetTestEntity(t, db, lib, "movie:a")
	b := facetTestEntity(t, db, lib, "movie:b")
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetTermsTx(ctx, tx, a, VocabGenre, "first", []Term{{Name: "Drama"}})
	})
	var term int64
	if err := db.QueryRow(`SELECT term_id FROM catalog_term_sources WHERE entity_id=?`, a).Scan(&term); err != nil {
		t.Fatal(err)
	}
	cc1Exec(t, db, `INSERT INTO catalog_entity_terms(entity_id,term_id) VALUES(?,?)`, b, term)
	for i := 0; i < 19; i++ {
		owner := a
		if i == 18 {
			owner = b
		}
		provider, source := fmt.Sprintf("p%02d", i), fmt.Sprintf("s%02d", i)
		if i == 17 {
			provider, source = strings.Repeat("p", 8193), strings.Repeat("s", 8194)
		}
		cc1Exec(t, db, `INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,label_override) VALUES(?,?,?,?,?)`, owner, term, provider, source, []any{nil, "", "override"}[i%3])
	}
	cc1Drain(t, db)
	cc1Exec(t, db, `UPDATE catalog_terms SET label='New drama' WHERE id=?`, term)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=36 AND entity_id=?`, term) != 1 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=33`) != 0 {
		t.Fatal("accepting dictionary write enumerated holders")
	}
	// Holder staging cannot race a not-yet-expanded source marker.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchTx(ctx, tx, 33, a) })
	facetTestTurn(t, db, 33, 1)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_work`) != 0 {
		t.Fatal("candidate captured before shared fanout")
	}
	cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=33`)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := readKeys(tx.QueryContext(context.Background(), `SELECT entity_id,revision FROM catalog_dirty WHERE domain=36`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = drainFacetSource(context.Background(), tx, 36, keys, 1); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	tx.Rollback()
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_source_fanouts`)+cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=33`) != 0 {
		t.Fatal("fanout rollback retained output/checkpoint")
	}
	facetTestTurn(t, db, 36, 1)
	first := groupRows(t, db, `SELECT after_entity,after_provider,after_source FROM catalog_facet_source_fanouts`)
	facetTestTurn(t, db, 36, 1)
	if got := groupRows(t, db, `SELECT after_entity,after_provider,after_source FROM catalog_facet_source_fanouts`); got == first {
		t.Fatal("full raw subcursor did not advance within holder")
	}
	cc1Exec(t, db, `UPDATE catalog_terms SET label='Newest drama' WHERE id=?`, term)
	cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=33`)
	for i := 0; i < 40; i++ {
		if !facetTestTurn(t, db, 36, 1) {
			break
		}
	}
	if got := cc1Scalar(t, db, `SELECT revision FROM catalog_dirty WHERE domain=33 AND entity_id=?`, a); got != 19 {
		t.Fatalf("raw duplicate/override rows skipped: revision=%d want 19", got)
	}
	if got := cc1Scalar(t, db, `SELECT revision FROM catalog_dirty WHERE domain=33 AND entity_id=?`, b); got != 1 {
		t.Fatalf("last holder missed: %d", got)
	}
	facetTestDrain(t, db, 1)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE value='Newest drama'`) != 2 {
		t.Fatal("latest dictionary source not applied")
	}
	// Dictionary semantic changes traverse field->term->holder, then withdraw.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, a, "studio", []string{"one", "two", "three"})
	})
	facetTestDrain(t, db, 1)
	cc1Exec(t, db, `UPDATE catalog_attribute_fields SET field='not-a-facet' WHERE field='studio'`)
	facetTestDrain(t, db, 1)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE field='studio'`) != 0 {
		t.Fatal("field rename left accepted studio facts")
	}
}

func TestFacetProducerNoGlobalEpisodeGenresAndOwnerMoves(t *testing.T) {
	db := cc1OpenDB(t, "facet-episode.sqlite")
	lib := cc1Library(t, db, "facet-episode", "Facet episode", "tv", "/facet-episode")
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show:a", Title: "A", Year: 2001}, map[string]any{"local_key": "a"})
	other := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show:b", Title: "B", Year: 2002}, map[string]any{"local_key": "b"})
	ep := cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Key: "ep:a", Title: "Episode"}, map[string]any{"show_id": show, "numbering": "seasonal", "number": 1})
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := SetTermsTx(ctx, tx, show, VocabGenre, "show", []Term{{Name: "Show only"}}); err != nil {
			return err
		}
		if err := SetTermsTx(ctx, tx, other, VocabGenre, "show", []Term{{Name: "Other show"}}); err != nil {
			return err
		}
		if err := SetTermsTx(ctx, tx, ep, VocabGenre, "episode", []Term{{Name: "Episode only"}}); err != nil {
			return err
		}
		return SetAttributesTx(ctx, tx, ep, "studio", []string{"Studio"})
	})
	cc1Drain(t, db)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_episodes WHERE field='genre'`) != 0 {
		t.Fatal("global episode own/inherited genre strings were materialized")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND field='genre' AND value='Show only'`, show) != 1 {
		t.Fatal("unrestricted show lost its own genre")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND value='Episode only'`, show) != 0 {
		t.Fatal("episode genres changed unrestricted show semantics")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_values WHERE field='genre'`) != 0 {
		t.Fatal("global show directory expanded episode genres")
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetTermsTx(ctx, tx, show, VocabGenre, "show", []Term{{Name: "Changed show"}})
	})
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=40 AND entity_id=?`, show) != 1 {
		t.Fatal("show edit missed bounded episode marker")
	}
	facetTestDrain(t, db, 1)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_episodes WHERE field='genre'`) != 0 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND field='genre' AND value='Changed show'`, show) != 1 {
		t.Fatal("show edit expanded global genres or lost own show semantics")
	}
	cc1Exec(t, db, `UPDATE catalog_episodes SET show_id=? WHERE entity_id=?`, other, ep)
	facetTestDrain(t, db, 1)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_episodes WHERE episode_id=? AND show_id=?`, ep, show) != 0 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_episodes WHERE episode_id=? AND field='genre'`, ep) != 0 {
		t.Fatal("episode move kept old show/value")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_values WHERE show_id=? AND field='studio'`, show) != 0 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_values WHERE show_id=? AND value='Studio'`, other) != 1 {
		t.Fatal("episode subtotal move differs")
	}
	// Direct source-owner UPDATE must mark old and new owners.
	a := facetTestEntity(t, db, lib, "movie:a")
	b := facetTestEntity(t, db, lib, "movie:b")
	collection := cc1Entity(t, db, Entity{Library: lib, Kind: Collection, Key: CollectionKey("set"), Title: "Set"}, nil)
	var asset int64
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := SetAttributesTx(ctx, tx, a, "network", []string{"Network"}); err != nil {
			return err
		}
		if err := SetTermsTx(ctx, tx, a, VocabGenre, "move", []Term{{Name: "Moved genre"}}); err != nil {
			return err
		}
		if err := SetCollectionMemberTx(ctx, tx, collection, a, "a", true); err != nil {
			return err
		}
		var err error
		asset, _, err = UpsertAssetTx(ctx, tx, Asset{Path: "/facet-episode/a.mkv", Height: 1080})
		if err != nil {
			return err
		}
		return LinkAssetTx(ctx, tx, a, asset, Link{})
	})
	cc1Drain(t, db)
	cc1Exec(t, db, `INSERT INTO catalog_entity_terms(entity_id,term_id) SELECT ?,term_id FROM catalog_term_sources WHERE entity_id=?`, b, a)
	for _, q := range []string{`UPDATE catalog_term_sources SET entity_id=? WHERE entity_id=?`, `UPDATE catalog_item_attribute_edges SET item_id=? WHERE item_id=?`, `UPDATE catalog_collection_members SET item_id=? WHERE item_id=?`, `UPDATE catalog_asset_links SET entity_id=? WHERE entity_id=?`} {
		cc1Exec(t, db, q, b, a)
	}
	facetTestDrain(t, db, 1)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=?`, a) != 0 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=?`, b) != 4 {
		t.Fatal("source owner UPDATE retained/lost facets")
	}
}

func TestFacetProducerFenceAndSeekPlans(t *testing.T) {
	db := cc1OpenDB(t, "facet-fence.sqlite")
	lib := cc1Library(t, db, "facet-fence", "Facet fence", "movie", "/facet-fence")
	id := facetTestEntity(t, db, lib, "movie:fence")
	cc1Drain(t, db)
	f, err := CaptureFacetProducerFence(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return ValidateFacetProducerFence(ctx, tx, f) })
	// Collection labels and rating classification invalidate even if no facet
	// membership/count changed. Pending work and poison are hard barriers.
	cc1Exec(t, db, `UPDATE catalog_entities SET title='Renamed' WHERE id=?`, id)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateFacetProducerFence(context.Background(), tx, f); !errors.Is(err, ErrFacetProducerChanged) {
		tx.Rollback()
		t.Fatalf("source stamp unchanged: %v", err)
	}
	tx.Rollback()
	f, err = CaptureFacetProducerFence(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	cc1Exec(t, db, `INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('facet-test-rating',12)`)
	next, err := CaptureFacetProducerFence(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if next.SourceEpoch == f.SourceEpoch {
		t.Fatal("classification missing from source stamp")
	}
	cc1Exec(t, db, `INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(33,?,'test',0)`, id)
	if _, err = CaptureFacetProducerFence(context.Background(), db); !errors.Is(err, ErrFacetProducerFailed) {
		t.Fatalf("failed unit accepted: %v", err)
	}
	for _, q := range []struct {
		sql   string
		args  []any
		index string
	}{
		{facetTermFanoutSQL, []any{1, 1, "p", "s", 8}, "catalog_term_sources_term"},
		{facetAssetFanoutSQL, []any{1, 1, 8}, "catalog_asset_links_asset"},
		{`SELECT l.asset_id,a.height FROM catalog_asset_links l CROSS JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND l.asset_id>? ORDER BY l.asset_id LIMIT ?`, []any{1, 1, 8}, "PRIMARY KEY"},
		{`SELECT entity_id FROM catalog_episodes INDEXED BY catalog_episodes_show WHERE show_id=? AND season_id IS NULL AND (number,entity_id)>(?,?) ORDER BY number,entity_id LIMIT ?`, []any{1, 1, 1, 8}, "catalog_episodes_show"},
		{`SELECT entity_id FROM catalog_episodes INDEXED BY catalog_episodes_show WHERE show_id=? AND season_id IS NOT NULL ORDER BY season_id,number,entity_id LIMIT ?`, []any{1, 8}, "catalog_episodes_show"},
		{`SELECT entity_id FROM catalog_episodes INDEXED BY catalog_episodes_show WHERE show_id=? AND (season_id,number,entity_id)>(?,?,?) ORDER BY season_id,number,entity_id LIMIT ?`, []any{1, 1, 1, 1, 8}, "catalog_episodes_show"},
		{`SELECT field,value FROM catalog_show_facet_episodes WHERE episode_id=? AND (field,value)>(?,?) ORDER BY field,value LIMIT ?`, []any{1, "studio", "g", 8}, "PRIMARY KEY"},
	} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+q.sql, q.args...)
		if err != nil {
			t.Fatal(err)
		}
		var details string
		for rows.Next() {
			var a, b, c int
			var detail string
			if err = rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			details += detail
		}
		rows.Close()
		if !strings.Contains(details, "SEARCH") || !strings.Contains(details, q.index) || strings.Contains(details, "TEMP B-TREE") {
			t.Fatalf("unbounded seek plan: %s", details)
		}
	}
}

func TestFacetProducerOversizedScalarIsOneTransientRow(t *testing.T) {
	db := cc1OpenDB(t, "facet-scalar.sqlite")
	lib := cc1Library(t, db, "facet-scalar", "Facet scalar", "movie", "/facet-scalar")
	id := facetTestEntity(t, db, lib, "movie:scalar")
	cc1Drain(t, db)
	a := strings.Repeat("A", facetScalarBudget+1)
	b := strings.Repeat("B", facetScalarBudget+1)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, id, "studio", []string{a, b})
	})
	for i := 0; i < 100; i++ {
		old := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage`)
		if !facetTestTurn(t, db, 33, 8) {
			break
		}
		next := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage`)
		if next-old > 1 || old-next > 1 {
			t.Fatal("oversized rows combined in one stage/cleanup batch")
		}
		if i == 99 {
			t.Fatal("large legal scalar did not finish")
		}
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND field='studio' AND value IN(?,?)`, id, a, b) != 2 {
		t.Fatal("large legal scalar was truncated or lost")
	}
	// A legal oversized raw source PK must not be skipped on byte-stop or reset.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetTermsTx(ctx, tx, id, VocabGenre, "ordinary", []Term{{Name: "genre"}})
	})
	var term int64
	if err := db.QueryRow(`SELECT term_id FROM catalog_term_sources WHERE entity_id=?`, id).Scan(&term); err != nil {
		t.Fatal(err)
	}
	cc1Exec(t, db, `INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id) VALUES(?,?,?,'source')`, id, term, a)
	cc1Exec(t, db, `INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id) VALUES(?,?,?,'source')`, id, term, b)
	facetTestDrain(t, db, 8)
	cc1Exec(t, db, `UPDATE catalog_terms SET label='renamed' WHERE id=?`, term)
	cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=33`)
	for i := 0; i < 10; i++ {
		old := cc1Scalar(t, db, `SELECT COALESCE(sum(revision),0) FROM catalog_dirty WHERE domain=33`)
		if !facetTestTurn(t, db, 36, 64) {
			break
		}
		next := cc1Scalar(t, db, `SELECT COALESCE(sum(revision),0) FROM catalog_dirty WHERE domain=33`)
		if next-old > 1 {
			t.Fatal("oversized raw PKs combined in one accepting fanout quantum")
		}
	}
	if cc1Scalar(t, db, `SELECT revision FROM catalog_dirty WHERE domain=33 AND entity_id=?`, id) != 3 {
		t.Fatal("full oversized source cursor skipped a row")
	}
}

func TestFacetProducerRetainedGenerationDoesNotInvalidateOwnStage(t *testing.T) {
	db := cc1OpenDB(t, "facet-retained.sqlite")
	lib := cc1Library(t, db, "facet-retained", "Facet retained", "movie", "/facet-retained")
	id := facetTestEntity(t, db, lib, "movie:retained")
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, id, "studio", []string{"old"})
	})
	cc1Drain(t, db)
	cc1Exec(t, db, `UPDATE catalog_generations SET retaining=1,generation=generation+1 WHERE singleton=1`)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, id, "studio", []string{"new 1", "new 2", "new 3"})
	})
	var initialEpoch int64
	if err := db.QueryRow(`SELECT source_epoch FROM catalog_facet_source_state`).Scan(&initialEpoch); err != nil {
		t.Fatal(err)
	}
	facetTestDrain(t, db, 1)
	var finalEpoch int64
	if err := db.QueryRow(`SELECT source_epoch FROM catalog_facet_source_state`).Scan(&finalEpoch); err != nil {
		t.Fatal(err)
	}
	if initialEpoch != finalEpoch {
		t.Fatal("accepted output/retention invalidated its own source stage")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND value IN('new 1','new 2','new 3')`, id) != 3 {
		t.Fatal("new contribution missing")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members_serving WHERE entity_id=? AND value='old'`, id) != 1 {
		t.Fatal("retained basis did not keep old contribution")
	}
	if _, err := CaptureFacetProducerFence(context.Background(), db); !errors.Is(err, ErrBuilding) {
		t.Fatalf("retaining accepted as current exact source: %v", err)
	}
}

func TestFacetProducerPlacementChangesDuringMergeAndStagedDeletion(t *testing.T) {
	db := cc1OpenDB(t, "facet-placement.sqlite")
	lib := cc1Library(t, db, "facet-placement", "Facet placement", "movie", "/facet-placement")
	id := facetTestEntity(t, db, lib, "movie:placement")
	values := make([]string, 17)
	for i := range values {
		values[i] = fmt.Sprintf("value %02d", i)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SetAttributesTx(ctx, tx, id, "studio", values) })
	cc1Drain(t, db)
	cc1Exec(t, db, `UPDATE catalog_browse_rows SET rating_key='first|literal',label_key='first label|literal',recent_text='2026-01' WHERE entity_id=?`, id)
	entered := false
	for i := 0; i < 200; i++ {
		facetTestTurn(t, db, 33, 1)
		var phase int
		if err := db.QueryRow(`SELECT phase FROM catalog_facet_work WHERE entity_id=?`, id).Scan(&phase); err != nil {
			t.Fatal(err)
		}
		if phase == facetHolderAdd && cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND rating_key='first|literal'`, id) > 0 {
			entered = true
			break
		}
	}
	if !entered {
		t.Fatal("did not enter partially reconciled placement")
	}
	cc1Exec(t, db, `UPDATE catalog_browse_rows SET rating_key='second|literal',label_key='second label|literal',recent_text='2026-02' WHERE entity_id=?`, id)
	facetTestDrain(t, db, 1)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND rating_key='second|literal' AND label_key='second label|literal' AND recent_key='2026-02'`, id) != 17 {
		t.Fatal("mixed old/new placement or recent keys")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_groups WHERE library_id=? AND field='studio' AND total=1 AND recent_key='2026-02'`, lib) != 17 {
		t.Fatal("shared group totals/recent changed")
	}
	// New raw staging has no entity FK; deleting the owner cannot cascade-delete
	// its arbitrarily large stage. Cleanup must still run one full PK at a time.
	cc1Exec(t, db, `UPDATE catalog_item_attribute_edges SET source_value=source_value||' updated' WHERE item_id=?`, id)
	for i := 0; i < 100; i++ {
		facetTestTurn(t, db, 33, 1)
		if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id) >= 3 {
			break
		}
		if i == 99 {
			t.Fatal("did not stage deletion candidate")
		}
	}
	staged := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id)
	cc1Exec(t, db, `DELETE FROM catalog_entities WHERE id=?`, id)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id) != staged {
		t.Fatal("source delete cascaded stage")
	}
	facetTestDrain(t, db, 1)
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=?`, id) != 0 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_work WHERE entity_id=?`, id) != 0 || cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage WHERE entity_id=?`, id) != 0 {
		t.Fatal("deleted staged owner left accepted/staging rows")
	}
}

func TestFacetProducerLargeHolderRawLinksDeduplicateWithoutUnion(t *testing.T) {
	db := cc1OpenDB(t, "facet-links.sqlite")
	lib := cc1Library(t, db, "facet-links", "Facet links", "movie", "/facet-links")
	id := facetTestEntity(t, db, lib, "movie:links")
	cc1Drain(t, db)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 19; i++ {
			asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprintf("/facet-links/%d.mkv", i), Height: []int{480, 720, 1080, 2160}[i%4]})
			if err != nil {
				return err
			}
			if err = LinkAssetTx(ctx, tx, id, asset, Link{}); err != nil {
				return err
			}
		}
		return nil
	})
	for i := 0; i < 500; i++ {
		worked := false
		for _, domain := range []int{41, 42, 43, 44} {
			worked = facetTestTurn(t, db, domain, 8) || worked
		}
		if !worked {
			break
		}
		if i == 499 {
			t.Fatal("source fixtures did not settle")
		}
	}
	rawRows := 0
	var after int64
	for i := 0; i < 100; i++ {
		facetTestTurn(t, db, 33, 1)
		var phase int
		var cursor int64
		err := db.QueryRow(`SELECT phase,after_id FROM catalog_facet_work WHERE entity_id=?`, id).Scan(&phase, &cursor)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if phase == facetHolderAssets && cursor != after {
			rawRows++
			after = cursor
		}
		if i == 99 {
			t.Fatal("holder links did not complete")
		}
	}
	if rawRows != 19 {
		t.Fatalf("raw link subcursor visited %d rows, want 19", rawRows)
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND field='resolution'`, id) != 4 {
		t.Fatal("duplicate resolutions changed per-holder cardinality")
	}
	if err := CheckFacetCounts(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
