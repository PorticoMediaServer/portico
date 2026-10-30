package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestContainerRefreshDoesNotWaitForItsProjectionConsumers(t *testing.T) {
	db := cc1OpenDB(t, "refresh-consumers")
	now := time.Now().UnixMilli()
	for _, domain := range []int{DomainBrowseRows, DomainFacetCounts, DomainContainerRefresh, DomainMissingSources} {
		cc1Exec(t, db, `INSERT INTO catalog_derivations(domain,version,served_ms) VALUES(?,1,?) ON CONFLICT(domain) DO UPDATE SET rebuilding=0,served_ms=excluded.served_ms`, domain, now)
	}
	for _, domain := range []int{DomainFacetCounts, DomainContainerRefresh, DomainMissingSources} {
		cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,1,1)`, domain)
	}
	eligibleDomain := func(domain int, waited int64) bool {
		t.Helper()
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM (`+nextDomainSQL+`) WHERE domain=?)`, waited, domain).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}
	eligible := func(waited int64) bool { return eligibleDomain(DomainContainerRefresh, waited) }
	cutoff := now - deferredWait.Milliseconds()
	if !eligible(cutoff) {
		t.Fatal("facet/missing consumers deferred the refresh they depend on")
	}
	for _, domain := range []int{DomainFacetCounts, DomainMissingSources} {
		if eligibleDomain(domain, cutoff) {
			t.Fatalf("consumer %d started before refresh source epochs settled", domain)
		}
	}
	// Real producer work still coalesces refreshes until it settles or the
	// existing starvation deadline expires. Neither the wait nor a budget changes.
	cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,1,1)`, DomainBrowseRows)
	if eligible(cutoff) {
		t.Fatal("refresh skipped coalescing while browse input is pending")
	}
	if !eligible(now) {
		t.Fatal("continuous producer work starved the overdue refresh")
	}
	cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=?`, DomainBrowseRows)
	if !eligible(cutoff) {
		t.Fatal("settled input did not release refresh with consumers still pending")
	}
	cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=?`, DomainContainerRefresh)
	for _, domain := range []int{DomainFacetCounts, DomainMissingSources} {
		if !eligibleDomain(domain, cutoff) {
			t.Fatalf("consumer %d did not resume after refresh completion", domain)
		}
	}
}

func TestContainerRefreshConsidersOnlyItsProducerClosure(t *testing.T) {
	db := cc1OpenDB(t, "refresh-producer-closure")
	now := time.Now().UnixMilli()
	// A fresh DB starts served_ms=0 and already satisfies the starvation
	// branch, so seed a just-served refresh to exercise coalescing itself.
	cc1Exec(t, db, `INSERT INTO catalog_derivations(domain,version,served_ms) VALUES(?,1,?) ON CONFLICT(domain) DO UPDATE SET rebuilding=0,served_ms=excluded.served_ms`, DomainContainerRefresh, now)
	cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,1,1)`, DomainContainerRefresh)
	eligible := func(waited int64) bool {
		t.Helper()
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM (`+nextDomainSQL+`) WHERE domain=?)`, waited, DomainContainerRefresh).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		return exists
	}
	cutoff := now - deferredWait.Milliseconds()
	if !eligible(cutoff) {
		t.Fatal("unrelated work deferred the refresh")
	}
	// 36-40 feed only the facet consumers and are not producers of the
	// refresh, so their pending work must not coalesce it.
	for _, domain := range []int{DomainSearch, DomainRelated, DomainCategories, DomainAssetIssues, DomainBookContext, DomainRecFacets, DomainBrowseSortParents, DomainFacetTermFanout, DomainFacetAssetFanout, DomainFacetAttributeTermFanout, DomainFacetAttributeFieldFanout, DomainFacetShowGenreFanout, DomainMissingSources} {
		// Scheduler eligibility reads registered derivations, as in a prepared worker.
		cc1Exec(t, db, `INSERT INTO catalog_derivations(domain,version,served_ms) VALUES(?,1,?) ON CONFLICT(domain) DO UPDATE SET rebuilding=0,served_ms=excluded.served_ms`, domain, now)
		cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,1,1)`, domain)
		if !eligible(cutoff) {
			t.Fatalf("unrelated domain %d coalesced the refresh", domain)
		}
		cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=?`, domain)
	}
	// The closed producer set still coalesces it, and the existing served
	// deadline still releases it.
	for _, domain := range []int{DomainBrowseRows, DomainBrowseEdges, DomainAvailability, DomainItemMetrics, DomainAssetMetrics, DomainLibraryAvailability, DomainInheritedAttributes, DomainAssetSources, DomainEntitySources, DomainLibrarySources} {
		// Scheduler eligibility reads registered derivations, as in a prepared worker.
		cc1Exec(t, db, `INSERT INTO catalog_derivations(domain,version,served_ms) VALUES(?,1,?) ON CONFLICT(domain) DO UPDATE SET rebuilding=0,served_ms=excluded.served_ms`, domain, now)
		cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,1,1)`, domain)
		if eligible(cutoff) {
			t.Fatalf("refresh skipped coalescing while producer %d is pending", domain)
		}
		if !eligible(now) {
			t.Fatalf("served deadline did not release the refresh while producer %d is pending", domain)
		}
		cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=?`, domain)
	}
	// A rebuilding producer is pending work too.
	cc1Exec(t, db, `INSERT INTO catalog_derivations(domain,version,rebuilding,served_ms) VALUES(?,1,1,?) ON CONFLICT(domain) DO UPDATE SET rebuilding=1,served_ms=excluded.served_ms`, DomainLibrarySources, now)
	if eligible(cutoff) {
		t.Fatal("rebuilding producer did not coalesce the refresh")
	}
	cc1Exec(t, db, `UPDATE catalog_derivations SET rebuilding=0 WHERE domain=?`, DomainLibrarySources)
	if !eligible(cutoff) {
		t.Fatal("settled producer did not release the refresh")
	}
}

func TestContainerRefreshAndWideFacetConsumersFinish(t *testing.T) {
	db := cc1OpenDB(t, "refresh-wide-facets")
	lib := cc1Library(t, db, "wide", "Wide", "movie", "/wide")
	const holders, values = 4, 19
	for i := 0; i < holders; i++ {
		id := facetTestEntity(t, db, lib, fmt.Sprintf("movie:%d", i))
		studios := make([]string, values)
		for j := range studios {
			studios[j] = fmt.Sprintf("studio %02d", j)
		}
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			if err := SetAttributesTx(ctx, tx, id, "studio", studios); err != nil {
				return err
			}
			return TouchTx(ctx, tx, DomainContainerRefresh, id)
		})
	}
	// Use the ordinary scheduler rather than directly stepping the facet domain:
	// each holder needs several raw quanta and must survive refresh epoch bumps.
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE field='studio'`); got != holders*values {
		t.Fatalf("studio members %d, want %d", got, holders*values)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_work`); got != 0 {
		t.Fatalf("%d private facet holders never finished", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty`); got != 0 {
		t.Fatalf("%d dirty holders remain", got)
	}
	if err := CheckFacetCounts(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestSingleHeadDrainDoesNotFetchOrReportTheRestOfItsQueue(t *testing.T) {
	db := cc1OpenDB(t, "single-head")
	w := NewWorker(db)
	if err := w.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Isolate an ordinary retired-source tail. Missing source rows are legal:
	// the exact old queue key is acknowledged without touching sibling keys.
	cc1Exec(t, db, `UPDATE catalog_derivations SET rebuilding=0,first_build=0`)
	cc1Exec(t, db, `DELETE FROM catalog_dirty`)
	for i := 1; i <= 130; i++ {
		cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,?,1)`, DomainMissingSources, i)
	}
	n, err := w.Step(context.Background(), 500)
	if err != nil {
		t.Fatal(err)
	}
	domain, units, _ := w.LastBatch()
	if domain != DomainMissingSources || n != 1 || units != 1 {
		t.Fatalf("one source turn reported untouched sibling keys: domain=%d n=%d units=%d", domain, n, units)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=44`); got != 129 {
		t.Fatalf("source head changed %d sibling keys", 130-got)
	}
}
