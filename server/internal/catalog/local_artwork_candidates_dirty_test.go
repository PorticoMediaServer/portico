package catalog

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestLocalArtworkCandidateChangesDirtyOnlyAffectedHeads(t *testing.T) {
	ctx := context.Background()
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	first := c.Movie(lib, "/m/First.mkv", "First", 2000)
	second := c.Movie(lib, "/m/Second.mkv", "Second", 2001)
	s := New(c.DB)
	source, err := s.LibrarySource(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 VALUES('candidate-object',?,?,?,'First.mkv','r1','{}',1,1)`, source.ID, first.Token, source.Incarnation)
	a := assets.LocalArtworkCandidate{Slot: "item/poster", Key: strings.Repeat("a", 64)}
	b := assets.LocalArtworkCandidate{Slot: "item/backdrop", Key: strings.Repeat("b", 64)}
	write := func(revision string, values ...assets.LocalArtworkCandidate) error {
		tx, err := c.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err = s.commitInventoryArtworkCandidatesTx(ctx, tx, source, "candidate-object", revision, first.Token, "movie", values); err != nil {
			return err
		}
		return tx.Commit()
	}
	expect := func(t *testing.T, ids ...int64) {
		t.Helper()
		rows, err := c.DB.Query(`SELECT kind,entity_id FROM artwork_dirty ORDER BY entity_id`)
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for rows.Next() {
			var kind string
			var id int64
			if err = rows.Scan(&kind, &id); err != nil || kind != "item" {
				t.Fatal(kind, id, err)
			}
			got = append(got, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, ids) {
			t.Fatalf("dirty heads %v want %v", got, ids)
		}
		c.Exec(`DELETE FROM artwork_dirty`)
	}
	c.Exec(`DELETE FROM artwork_dirty`)
	for _, step := range []struct {
		name   string
		values []assets.LocalArtworkCandidate
		ids    []int64
	}{
		{"added", []assets.LocalArtworkCandidate{a}, []int64{first.ID}},
		{"unchanged duplicate", []assets.LocalArtworkCandidate{a, a}, nil},
		{"another role", []assets.LocalArtworkCandidate{a, b}, []int64{first.ID}},
		{"removed", nil, []int64{first.ID}},
		{"unchanged empty", nil, nil},
		{"restored", []assets.LocalArtworkCandidate{a}, []int64{first.ID}},
	} {
		t.Run(step.name, func(t *testing.T) {
			if err := write("r1", step.values...); err != nil {
				t.Fatal(err)
			}
			expect(t, step.ids...)
		})
	}
	// The same image now belongs to another genuine title: notify both the old
	// and new heads, even though its bytes and inventory observation are equal.
	c.Exec(`DELETE FROM catalog_asset_links WHERE entity_id=? AND asset_id=(SELECT id FROM catalog_assets WHERE token=?)`, first.ID, first.Token)
	c.Exec(`INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,edition) SELECT ?,id,0,'' FROM catalog_assets WHERE token=?`, second.ID, first.Token)
	c.Exec(`DELETE FROM artwork_dirty`)
	if err = write("r1", a); err != nil {
		t.Fatal(err)
	}
	expect(t, first.ID, second.ID)
	if err = write("stale", b); !errors.Is(err, storage.ErrInventoryChanged) {
		t.Fatal("stale publication accepted", err)
	}
	expect(t)
	c.Exec(`UPDATE inventory_objects SET revision='r2' WHERE id='candidate-object'`)
	if err = write("r2", a); err != nil {
		t.Fatal(err)
	}
	expect(t, second.ID)
	if err = write("r2", a); err != nil {
		t.Fatal(err)
	}
	expect(t)
	// A notification is part of the publication transaction, never a separate
	// wake that can escape a failed or rolled-back accepted candidate update.
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitInventoryArtworkCandidatesTx(ctx, tx, source, "candidate-object", "r2", first.Token, "movie", []assets.LocalArtworkCandidate{b}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var inside int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM artwork_dirty WHERE kind='item' AND entity_id=?`, second.ID).Scan(&inside); err != nil || inside != 1 {
		tx.Rollback()
		t.Fatal("missing same-transaction dirty head", inside, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	expect(t)
	var key string
	if err = c.DB.QueryRow(`SELECT art_key FROM inventory_artwork_candidates WHERE object_id='candidate-object'`).Scan(&key); err != nil || key != a.Key {
		t.Fatal("rolled-back replacement changed accepted candidates", key, err)
	}
}
