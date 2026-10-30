package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestP252bMBAdoptionCommittedWithPublicationAndExactOwnFence(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	value := mbLaneReleaseFixture()
	if e := s.mbPublishAlbum(context.Background(), j, metadataprovider.ReleaseLookup{RequestedID: value.ID, Release: value}, "2026-10-28T00:00:00Z"); e != nil {
		t.Fatal(e)
	}
	var root string
	var count int
	if e := db.QueryRow(`SELECT m.mutation_id FROM catalog_entity_mutations m JOIN console_operations o ON o.kind='catalog-mutation' AND o.resource=m.mutation_id WHERE m.owner_kind='mb-adoption' AND m.cause_operation=? AND m.accepted_revision=(SELECT release_revision FROM mb_album_links WHERE album_id=m.entity_id)`, j.operation).Scan(&root); e != nil {
		t.Fatal("publication/adoption crash gap", e)
	}
	e := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if e := compactcatalog.RequireEntityAcquirableTx(context.Background(), tx, j.base.Entity); !errors.Is(e, compactcatalog.ErrEntityMutating) {
			return errors.New("generic acquisition bypassed own adoption")
		}
		if e := compactcatalog.RequireMBPublicationMutationCurrentTx(context.Background(), tx, j.base.Entity, j.operation); e != nil {
			return e
		}
		if e := compactcatalog.RequireMBPublicationMutationCurrentTx(context.Background(), tx, j.base.Entity, "another-cause"); !errors.Is(e, compactcatalog.ErrEntityMutating) {
			return errors.New("competing cause exempted")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 24; i++ {
		// No in-memory metadata continuation survives this boundary. The existing
		// operations adapter has everything it needs in the committed catalog fact.
		out, e := catalog.New(db).MutationAdapter().Step(context.Background(), root)
		if e != nil {
			t.Fatal(e)
		}
		if out.State == "succeeded" {
			if e = db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE family_id=? AND status<>'terminal'`, root).Scan(&count); e != nil || count != 0 {
				t.Fatal(count, e)
			}
			return
		}
	}
	t.Fatal("adoption did not finish after publication/restart")
}
func TestP252bMBFetchedBeforeIntentCannotPublish(t *testing.T) {
	db, _ := mbLaneDB(t)
	s := New(db, "")
	j := mbStagingJob(t, s, "album")
	e := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, e := compactcatalog.BeginEntityMutationTx(context.Background(), tx, compactcatalog.MutationIntent{Entity: j.base.Entity, OwnerKind: "title", OwnerID: "delete-after-fetch", OwnerGeneration: 1, Mode: "members"})
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	e = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error { return s.activeMB(context.Background(), tx, j) })
	if !errors.Is(e, errTVDBStale) {
		t.Fatalf("old fetched worker retained authority: %v", e)
	}
	picked, e := s.claimMB(context.Background())
	if e != nil || picked != nil {
		t.Fatalf("claim crossed mutation fence: %+v %v", picked, e)
	}
}
