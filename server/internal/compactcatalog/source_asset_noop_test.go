package compactcatalog

import (
	"context"
	"testing"
)

func TestSourceAssetNoOpUpdateKeepsAcceptedSourceEpoch(t *testing.T) {
	db := cc1OpenDB(t, "asset-noop.sqlite")
	lib := cc1Library(t, db, "asset-noop", "No-op", "movie", "/noop")
	item := facetTestEntity(t, db, lib, "noop-film")
	asset := sourceTestAsset(t, db, []int64{item}, "/noop/movie.mp4")
	cc1Drain(t, db)
	before := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`)
	cc1Exec(t, db, `UPDATE catalog_assets SET id=id,token=token,path=path,size=size,modified_ns=modified_ns,container=container,video_codec=video_codec,audio_codec=audio_codec,width=width,height=height,duration=duration,available=available WHERE id=?`, asset)
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_upstream_sources WHERE domain=41`); n != 0 {
		t.Fatal("unchanged asset queued source invalidation", n)
	}
	if after := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`); after != before {
		t.Fatal("unchanged asset revoked accepted source", before, after)
	}
	cc1Exec(t, db, `UPDATE catalog_assets SET size=size+1 WHERE id=?`, asset)
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_upstream_sources WHERE domain=41 AND source_id=? AND (flags&12)=12`, asset); n != 1 {
		t.Fatal("changed media lost metadata/revision invalidation", n)
	}
	if after := cc1Scalar(t, db, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`); after <= before {
		t.Fatal("changed media did not fence accepted source")
	}
	cc1Drain(t, db)
	if _, err := CaptureFacetProducerFence(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
