package compactcatalog

import "testing"

func TestVisibilitySortBucketsTrackClassRowsAndMetrics(t *testing.T) {
	db := cc1OpenDB(t, "visibility-sort.sqlite")
	lib := cc1Library(t, db, "films", "Films", "movie", "/films")
	movie := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "file:a.mkv", Title: "Alpha", Year: 1999, Added: "2026-09-24T12:00:00Z"}, nil)
	cc1Drain(t, db)
	class := cc1Class(t, db, lib, "policy")
	cc1Visible(t, db, class, movie, 1)
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_visibility_sort_buckets WHERE axis=1 AND value='1999'`); got != 1 {
		t.Fatalf("class year bucket missing: %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_visibility_sort_buckets WHERE axis=3 AND value='2026-09'`); got != 1 {
		t.Fatalf("class month bucket missing: %d", got)
	}
	cc1Exec(t, db, `INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at) VALUES(?,'tmdb',8.5,10,1,'','')`, movie)
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_visibility_sort_buckets WHERE axis=5 AND value='8'`); got != 1 {
		t.Fatalf("class metric change not queued: %d", got)
	}
	cc1Exec(t, db, `DELETE FROM compact_visibility_rows WHERE entity_id=?`, movie)
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_visibility_sort_buckets`); got != 0 {
		t.Fatalf("class deletion retained sort buckets: %d", got)
	}
}
