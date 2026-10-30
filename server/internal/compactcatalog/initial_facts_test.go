package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestEnsureEntityInitialFactsPreserveCreationAndExistingData(t *testing.T) {
	db := cc1OpenDB(t, "initial-facts.sqlite")
	library := cc1Library(t, db, "tv", "TV", "tv", "/tv")
	show := cc1Entity(t, db, Entity{Library: library, Kind: Show, Key: "show", Title: "Show"}, map[string]any{"local_key": "show"})
	var season, episode int64
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var created bool
		var err error
		season, created, err = EnsureEntityWithFactsTx(ctx, tx, Entity{Library: library, Kind: Season, Parent: show, Key: "season", Title: "Season 1"}, map[string]any{"show_id": show, "number": 1})
		if err != nil {
			return err
		}
		if !created {
			t.Fatal("new season reused")
		}
		episode, created, err = EnsureEntityWithFactsTx(ctx, tx, Entity{Library: library, Kind: Episode, Parent: season, Key: "episode", Title: "Episode 1"}, map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": 1})
		if err != nil {
			return err
		}
		if !created {
			t.Fatal("new episode reused")
		}
		for _, id := range []int64{season, episode} {
			for _, domain := range []int{DomainSearch, DomainBrowseRows, DomainBrowseEdges} {
				var revision int
				if err = tx.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, domain, id).Scan(&revision); err != nil || revision < 1 {
					t.Fatalf("creation work missing %d/%d: %d %v", id, domain, revision, err)
				}
			}
		}
		return nil
	})
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_episodes WHERE entity_id=? AND show_id=? AND season_id=? AND number=1`, episode, show, season); got != 1 {
		t.Fatal("initial facts lost", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows WHERE entity_id=?`, episode); got != 1 {
		t.Fatal("initial episode never became browseable", got)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		id, created, err := EnsureEntityWithFactsTx(ctx, tx, Entity{Library: library, Kind: Episode, Parent: season, Key: "episode", Title: "Replacement"}, map[string]any{"number": 99})
		if id != episode || created {
			t.Fatal("existing identity replaced", id, created)
		}
		return err
	})
	if got := cc1Scalar(t, db, `SELECT number FROM catalog_episodes WHERE entity_id=?`, episode); got != 1 {
		t.Fatal("existing facts overwritten", got)
	}
}

func TestEnsureEntityInitialFactsRollbackAndParentFence(t *testing.T) {
	db := cc1OpenDB(t, "initial-facts-fences.sqlite")
	library := cc1Library(t, db, "tv", "TV", "tv", "/tv")
	show := cc1Entity(t, db, Entity{Library: library, Kind: Show, Key: "show", Title: "Show"}, map[string]any{"local_key": "show"})
	cc1Drain(t, db)
	beforeEntities := cc1Scalar(t, db, `SELECT count(*) FROM catalog_entities`)
	beforeIdentities := cc1Scalar(t, db, `SELECT count(*) FROM catalog_identities`)
	stopped := errors.New("rollback after initialization")
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, created, err := EnsureEntityWithFactsTx(context.Background(), tx, Entity{Library: library, Kind: Season, Parent: show, Key: "rollback", Title: "Season 1"}, map[string]any{"show_id": show, "number": 1})
		if err != nil {
			return err
		}
		if !created {
			t.Fatal("expected new entity")
		}
		return stopped
	})
	if !errors.Is(err, stopped) {
		t.Fatal(err)
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_entities`) != beforeEntities || cc1Scalar(t, db, `SELECT count(*) FROM catalog_identities`) != beforeIdentities || cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty`) != 0 {
		t.Fatal("rollback leaked entity, identity or dirty work")
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := BeginEntityMutationTx(ctx, tx, MutationIntent{OwnerKind: "title", OwnerID: "fixture", OwnerGeneration: 1, Entity: show, Mode: "remove"})
		return err
	})
	// The fact-parent guard must also run when the backbone parent is absent.
	err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, _, err := EnsureEntityWithFactsTx(context.Background(), tx, Entity{Library: library, Kind: Season, Key: "blocked", Title: "Blocked"}, map[string]any{"show_id": show, "number": 2})
		return err
	})
	if !errors.Is(err, ErrEntityMutating) {
		t.Fatal("initial fact parent bypassed mutation fence", err)
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_entities`) != beforeEntities || cc1Scalar(t, db, `SELECT count(*) FROM catalog_identities`) != beforeIdentities {
		t.Fatal("rejected parent leaked initialized identity")
	}
}
