package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func browseRowWorkStep(t *testing.T, db *sql.DB, domain int, k Key, limit int) bool {
	t.Helper()
	var done bool
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		done, err = deriveBrowseRowBounded(ctx, tx, domain, k, limit)
		return err
	})
	return done
}

func browseRowWorkDrain(t *testing.T, db *sql.DB, domain int, k Key, limit int) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if browseRowWorkStep(t, db, domain, k, limit) {
			return
		}
	}
	t.Fatal("bounded browse-row work did not finish")
}

func browseRowWorkItem(t *testing.T, db *sql.DB, key string) (int64, Key) {
	t.Helper()
	lib := cc1Library(t, db, "m", "Movies", "movie", "/m")
	id := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: key, Title: "Alpha", Year: 2000}, nil)
	return id, Key{ID: id, Revision: 1}
}

func browseRowWorkSet(t *testing.T, db *sql.DB, id int64, field string, values ...string) {
	t.Helper()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetAttributesTx(ctx, tx, id, field, values)
	})
}

func browseRowWorkStaged(t *testing.T, db *sql.DB, domain int) int {
	t.Helper()
	return cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_row_keys WHERE domain=?`, domain)
}

func browseRowWorkState(t *testing.T, db *sql.DB, domain int) (int64, int64, int, int64, bool) {
	t.Helper()
	var id, revision, after int64
	var phase int
	err := db.QueryRow(`SELECT entity_id,revision,phase,after_term FROM catalog_browse_row_work WHERE domain=?`, domain).Scan(&id, &revision, &phase, &after)
	if err == sql.ErrNoRows {
		return 0, 0, 0, 0, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return id, revision, phase, after, true
}

func browseRowWorkRowKeys(t *testing.T, db *sql.DB, id int64) (string, string) {
	t.Helper()
	var rating, label string
	if err := db.QueryRow(`SELECT rating_key,label_key FROM catalog_browse_rows WHERE entity_id=?`, id).Scan(&rating, &label); err != nil {
		t.Fatal(err)
	}
	return rating, label
}

// Duplicate input values collapse to one staged key, an empty stored value_key
// is a real distinct key, and the final '|'-joined output is exactly the
// original row expression's (ordered by value_key with the empty key first).
func TestBrowseRowWorkDistinctScalarMatchesOriginalSQL(t *testing.T) {
	db := cc1OpenDB(t, "browse-row-distinct.sqlite")
	id, k := browseRowWorkItem(t, db, "film:distinct")
	browseRowWorkSet(t, db, id, "contentRating", "PG", "pg", "PG-13")
	// The write API drops empty values; the read expression still stages a
	// stored empty value_key.
	cc1Exec(t, db, `INSERT INTO catalog_attribute_terms(field_id,value_key) VALUES((SELECT id FROM catalog_attribute_fields WHERE field='contentRating'),'')`)
	cc1Exec(t, db, `INSERT OR IGNORE INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) SELECT ?,id,'',99 FROM catalog_attribute_terms WHERE field_id=(SELECT id FROM catalog_attribute_fields WHERE field='contentRating') AND value_key=''`, id)

	browseRowWorkDrain(t, db, DomainBrowseRows, k, 100)
	rating, label := browseRowWorkRowKeys(t, db, id)
	if rating != "|pg|pg-13" {
		t.Fatalf("rating_key=%q, want the distinct ordered original scalar %q", rating, "|pg|pg-13")
	}
	if label != "" {
		t.Fatalf("label_key=%q, want empty", label)
	}
	if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != 0 {
		t.Fatalf("acknowledged with %d staged keys", n)
	}
}

// A raw page visits at most eight edges even when the caller allows more, and
// rejected fields still advance the complete (item_id,term_id) cursor.
func TestBrowseRowWorkRejectedRawPageStopsAtEight(t *testing.T) {
	db := cc1OpenDB(t, "browse-row-reject.sqlite")
	id, k := browseRowWorkItem(t, db, "film:reject")
	var tags []string
	for i := 0; i < 12; i++ {
		tags = append(tags, fmt.Sprintf("tag-%02d", i))
	}
	browseRowWorkSet(t, db, id, "tag", tags...)
	browseRowWorkSet(t, db, id, "contentRating", "R")

	if browseRowWorkStep(t, db, DomainBrowseRows, k, 500) {
		t.Fatal("finished on the first raw page")
	}
	_, revision, phase, after, found := browseRowWorkState(t, db, DomainBrowseRows)
	if !found || revision != k.Revision || phase != browseRowWorkScan || after == 0 {
		t.Fatalf("work=%v revision=%d phase=%d after=%d", found, revision, phase, after)
	}
	var consumed int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_item_attribute_edges WHERE item_id=? AND term_id<=?`, id, after).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if consumed != browseRowWorkQuantum {
		t.Fatalf("first quantum consumed %d raw edges, want %d", consumed, browseRowWorkQuantum)
	}
	if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != 0 {
		t.Fatalf("rejected fields staged %d keys", n)
	}

	browseRowWorkDrain(t, db, DomainBrowseRows, k, 500)
	rating, label := browseRowWorkRowKeys(t, db, id)
	if rating != "r" || label != "" {
		t.Fatalf("rating=%q label=%q, want r and empty", rating, label)
	}
}

// A revision bump while keys are staged must reclaim those bounded pages and
// restart, not append the new revision's keys to the old ones.
func TestBrowseRowWorkRevisionDriftResetsStagedKeys(t *testing.T) {
	db := cc1OpenDB(t, "browse-row-drift.sqlite")
	id, k1 := browseRowWorkItem(t, db, "film:drift")
	browseRowWorkSet(t, db, id, "contentRating", "PG")
	browseRowWorkSet(t, db, id, "label", "Family")
	browseRowWorkDrain(t, db, DomainBrowseRows, k1, 100)
	if rating, label := browseRowWorkRowKeys(t, db, id); rating != "pg" || label != "family" {
		t.Fatalf("seed row rating=%q label=%q", rating, label)
	}

	browseRowWorkSet(t, db, id, "contentRating", "R")
	browseRowWorkSet(t, db, id, "label", "Drama")
	k2 := Key{ID: id, Revision: k1.Revision + 1}
	if browseRowWorkStep(t, db, DomainBrowseRows, k2, 100) {
		t.Fatal("revision 2 finished after one scan")
	}
	if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != 2 {
		t.Fatalf("revision 2 staged %d keys, want 2", n)
	}

	// Mutate again before revision 2 is complete.
	browseRowWorkSet(t, db, id, "contentRating", "NC-17")
	browseRowWorkSet(t, db, id, "label", "Drama", "Comedy")
	k3 := Key{ID: id, Revision: k1.Revision + 2}
	if browseRowWorkStep(t, db, DomainBrowseRows, k3, 100) {
		t.Fatal("drift cleanup acknowledged before the reset")
	}
	if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != 0 {
		t.Fatalf("drift left %d staged keys", n)
	}
	entity, revision, phase, after, found := browseRowWorkState(t, db, DomainBrowseRows)
	if !found || entity != k3.ID || revision != k3.Revision || phase != browseRowWorkScan || after != 0 {
		t.Fatalf("reset work=%v entity=%d revision=%d phase=%d after=%d", found, entity, revision, phase, after)
	}

	browseRowWorkDrain(t, db, DomainBrowseRows, k3, 100)
	rating, label := browseRowWorkRowKeys(t, db, id)
	if rating != "nc-17" || label != "comedy|drama" {
		t.Fatalf("rating=%q label=%q, want nc-17 and comedy|drama", rating, label)
	}
}

// Holder drift reclaims stale keys in PK pages. Twenty seeded keys take three
// calls (8, 8, 4), and only the last page resets the holder.
func TestBrowseRowWorkDriftCleanupIsBounded(t *testing.T) {
	db := cc1OpenDB(t, "browse-row-clean.sqlite")
	id, k := browseRowWorkItem(t, db, "film:clean")
	for i := 0; i < 20; i++ {
		cc1Exec(t, db, `INSERT INTO catalog_browse_row_keys(domain,field,value_key) VALUES(?,?,?)`, DomainBrowseRows, i%2, fmt.Sprintf("key-%02d", i))
	}
	cc1Exec(t, db, `INSERT INTO catalog_browse_row_work(domain,entity_id,revision,phase,after_term) VALUES(?,?,?,?,?)`, DomainBrowseRows, id+1000, 7, browseRowWorkScan, 999)

	for call, want := range []int{12, 4, 0} {
		if browseRowWorkStep(t, db, DomainBrowseRows, k, 500) {
			t.Fatalf("call %d acknowledged during drift cleanup", call+1)
		}
		if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != want {
			t.Fatalf("call %d left %d staged keys, want %d", call+1, n, want)
		}
	}
	entity, revision, phase, after, found := browseRowWorkState(t, db, DomainBrowseRows)
	if !found || entity != k.ID || revision != k.Revision || phase != browseRowWorkScan || after != 0 {
		t.Fatalf("reset work=%v entity=%d revision=%d phase=%d after=%d", found, entity, revision, phase, after)
	}
}

// The final scalar is computed once, and the unit is acknowledged only after
// every staged key has been reclaimed in bounded pages.
func TestBrowseRowWorkCleanupBeforeAcknowledgement(t *testing.T) {
	db := cc1OpenDB(t, "browse-row-ack.sqlite")
	id, k := browseRowWorkItem(t, db, "film:ack")
	var values []string
	for i := 0; i < 10; i++ {
		values = append(values, fmt.Sprintf("rating-%02d", i))
	}
	browseRowWorkSet(t, db, id, "contentRating", values...)

	for i := 0; i < 20; i++ {
		if browseRowWorkStep(t, db, DomainBrowseRows, k, 500) {
			t.Fatal("acknowledged before the final scalar and cleanup")
		}
		if _, _, phase, _, _ := browseRowWorkState(t, db, DomainBrowseRows); phase == browseRowWorkClear {
			break
		}
	}
	if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != 10 {
		t.Fatalf("finalized with %d staged keys, want 10", n)
	}

	if browseRowWorkStep(t, db, DomainBrowseRows, k, 500) {
		t.Fatal("acknowledged after only one bounded cleanup page")
	}
	if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != 2 {
		t.Fatalf("first cleanup page left %d keys, want 2", n)
	}
	if browseRowWorkStep(t, db, DomainBrowseRows, k, 500) != true {
		t.Fatal("last cleanup page did not acknowledge")
	}
	if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != 0 {
		t.Fatalf("acknowledged with %d staged keys", n)
	}
	if _, _, _, _, found := browseRowWorkState(t, db, DomainBrowseRows); found {
		t.Fatal("acknowledged work row still exists")
	}
}

// The ordinary scalar byte stop stages one legal oversized key alone instead of
// widening the raw quantum that contains it.
func TestBrowseRowWorkOversizedScalarStagedAlone(t *testing.T) {
	db := cc1OpenDB(t, "browse-row-bytes.sqlite")
	id, k := browseRowWorkItem(t, db, "film:bytes")
	huge := strings.Repeat("z", facetScalarBudget+1)
	browseRowWorkSet(t, db, id, "contentRating", huge, "pg")
	var hugeTerm int64
	if err := db.QueryRow(`SELECT id FROM catalog_attribute_terms WHERE field_id=(SELECT id FROM catalog_attribute_fields WHERE field='contentRating') AND value_key=?`, huge).Scan(&hugeTerm); err != nil {
		t.Fatal(err)
	}

	if browseRowWorkStep(t, db, DomainBrowseRows, k, 8) {
		t.Fatal("finished on the oversized raw page")
	}
	_, _, phase, after, found := browseRowWorkState(t, db, DomainBrowseRows)
	if !found || phase != browseRowWorkScan || after != hugeTerm {
		t.Fatalf("work=%v phase=%d after=%d, want scan cursor at the oversized term %d", found, phase, after, hugeTerm)
	}
	if n := browseRowWorkStaged(t, db, DomainBrowseRows); n != 1 {
		t.Fatalf("oversized quantum staged %d keys, want 1", n)
	}

	browseRowWorkDrain(t, db, DomainBrowseRows, k, 8)
	rating, _ := browseRowWorkRowKeys(t, db, id)
	if rating != "pg|"+huge {
		t.Fatalf("rating_key length=%d, want pg then the oversized key", len(rating))
	}
}
