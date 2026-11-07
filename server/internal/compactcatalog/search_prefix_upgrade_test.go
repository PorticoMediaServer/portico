package compactcatalog

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestSearchNormalizedPrefixLiveConfirmation(t *testing.T) {
	db, entity := failingFixture(t, "normalized-prefix.sqlite")
	value := strings.Repeat("e\u0301 ", 22000) + "東京"
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET title=? WHERE id=?`, value, entity); err != nil {
			return err
		}
		return deriveSearch(ctx, tx, mustJSON([]int64{entity}))
	})
	slot := cc1Scalar(t, db, `SELECT active_slot FROM catalog_derivations WHERE domain=17`)
	var normalized string
	if err := db.QueryRow(searchTables(`SELECT normalized FROM catalog_search_documents WHERE entity_id=?`, slot), entity).Scan(&normalized); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(normalized, "東京") {
		t.Fatal("raw scalar substr disagreed with normalized discovery")
	}
	if n := cc1Scalar(t, db, searchTables(`SELECT count(*) FROM catalog_search_titles WHERE catalog_search_titles MATCH '東京'`, slot)); n != 1 {
		t.Fatal("live native discovery", n)
	}
}

func TestSearchVersion11RebuildsLiveVersion10(t *testing.T) {
	db, entity := failingFixture(t, "normalized-upgrade.sqlite")
	value := strings.Repeat("e\u0301 ", 22000) + "東京"
	slot := cc1Scalar(t, db, `SELECT active_slot FROM catalog_derivations WHERE domain=17`)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET title=? WHERE id=?`, value, entity); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, searchTables(`UPDATE catalog_search_documents SET search='legacy',normalized='legacy',title_fold='legacy' WHERE entity_id=?`, slot), entity); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE catalog_derivations SET version=10,rebuilding=0,first_build=0,rebuild_cursor=0,cleaning=0 WHERE domain=17`)
		return err
	})
	worker := NewWorker(db)
	if err := worker.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := cc1Scalar(t, db, `SELECT version FROM catalog_derivations WHERE domain=17`); v != 11 {
		t.Fatal("live derivation version was not upgraded", v)
	}
	if n := cc1Scalar(t, db, `SELECT rebuilding FROM catalog_derivations WHERE domain=17`); n != 1 {
		t.Fatal("old tokenization silently retained", n)
	}
	if n := cc1Scalar(t, db, searchTables(`SELECT count(*) FROM catalog_search_titles WHERE catalog_search_titles MATCH '東京'`, slot)); n != 0 {
		t.Fatal("upgrade fixture did not retain old serving data", n)
	}
	cc1Drain(t, db)
	current := cc1Scalar(t, db, `SELECT active_slot FROM catalog_derivations WHERE domain=17`)
	if current == slot || cc1Scalar(t, db, `SELECT rebuilding FROM catalog_derivations WHERE domain=17`) != 0 {
		t.Fatal("replacement search generation did not publish")
	}
	if n := cc1Scalar(t, db, searchTables(`SELECT count(*) FROM catalog_search_titles WHERE catalog_search_titles MATCH '東京'`, current)); n != 1 {
		t.Fatal("version rebuild kept obsolete discovery", n)
	}
	var normalized string
	if err := db.QueryRow(searchTables(`SELECT normalized FROM catalog_search_documents WHERE entity_id=?`, current), entity).Scan(&normalized); err != nil || !strings.Contains(normalized, "東京") {
		t.Fatal("rebuilt confirmation", err)
	}
}
