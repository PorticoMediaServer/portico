package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestSourceShowEpisodeFullTupleRollbackResetAndReopen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, domain := range []int{DomainFacetShowGenreFanout, DomainEntitySources} {
		t.Run(strconv.Itoa(domain), func(t *testing.T) {
			db := cc1OpenDB(t, "show-cursor.sqlite")
			lib := cc1Library(t, db, "show-cursor", "Shows", "tv", "/shows")
			show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show", Title: "Show"}, map[string]any{"local_key": "show"})
			seasons := make([]int64, 2)
			for i := range seasons {
				seasons[i] = cc1Entity(t, db, Entity{Library: lib, Kind: Season, Key: fmt.Sprint("season", i), Title: "Season"}, map[string]any{"show_id": show, "number": i + 1})
			}
			for i, position := range []struct {
				season any
				number int
			}{{seasons[1], 4}, {nil, 3}, {seasons[0], 2}, {nil, 1}, {seasons[0], 1}, {seasons[1], 1}, {nil, 2}} {
				cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Key: fmt.Sprint("episode", i), Title: "Episode"}, map[string]any{"show_id": show, "season_id": position.season, "numbering": "seasonal", "number": position.number})
			}

			// Equal season/number with distinct numbering still needs entity_id
			// as the final continuation component.
			cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Key: "episode-tie", Title: "Episode tie"}, map[string]any{"show_id": show, "season_id": seasons[0], "numbering": "absolute", "number": 1})
			cc1Drain(t, db)
			var want []int64
			rows, err := db.Query(`SELECT entity_id FROM catalog_episodes WHERE show_id=? ORDER BY season_id,number,entity_id`, show)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				want = append(want, id)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			queue := func() {
				cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
					if domain == DomainFacetShowGenreFanout {
						return TouchTx(ctx, tx, domain, show)
					}
					var token string
					if err := tx.QueryRowContext(ctx, `SELECT lower(hex(public_id)) FROM catalog_entities WHERE id=?`, show).Scan(&token); err != nil {
						return err
					}
					return queueSource(ctx, tx, domain, show, token, 1<<uint(Show))
				})
			}
			target := DomainFacetCounts
			if domain == DomainEntitySources {
				target = DomainSearch
			}
			queue()
			checkpointQuery := `SELECT * FROM catalog_facet_source_fanouts ORDER BY domain,source_id`
			if domain == DomainEntitySources {
				checkpointQuery = `SELECT * FROM catalog_upstream_sources ORDER BY domain,source_id`
			}
			checkpoint := groupRows(t, db, checkpointQuery)
			// The outputs and the full raw checkpoint roll back together.
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			keys, err := readKeys(tx.QueryContext(context.Background(), `SELECT entity_id,revision FROM catalog_dirty WHERE domain=?`, domain))
			if err != nil {
				t.Fatal(err)
			}
			d, _ := derivation(domain)
			if _, err = d.Drain(context.Background(), tx, keys, 1); err != nil {
				t.Fatal(err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if groupRows(t, db, checkpointQuery) != checkpoint {
				t.Fatal("rollback advanced full raw checkpoint")
			}
			if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, target) != 0 {
				t.Fatal("rollback retained downstream output")
			}
			walk := func(max int) []int64 {
				var got []int64
				for i := 0; i < 40; i++ {
					if !facetTestTurn(t, db, domain, 1) {
						break
					}
					var id int64
					var has bool
					var phase int
					if domain == DomainFacetShowGenreFanout {
						err = db.QueryRow(`SELECT after_entity,has_after FROM catalog_facet_source_fanouts WHERE domain=? AND source_id=?`, domain, show).Scan(&id, &has)
					} else {
						err = db.QueryRow(`SELECT after_id,has_after,phase FROM catalog_upstream_sources WHERE domain=? AND source_id=?`, domain, show).Scan(&id, &has, &phase)
						has = has && phase == 4
					}
					if err != nil && err != sql.ErrNoRows {
						t.Fatal(err)
					}
					if err == nil && has {
						got = append(got, id)
					}
					if len(got) == max {
						break
					}
				}
				return got
			}
			if got := walk(2); !reflect.DeepEqual(got, want[:2]) {
				t.Fatalf("first NULL range got %v want %v", got, want[:2])
			}
			// A new revision resets behind the cursor, including the nullable tuple.
			queue()
			cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=?`, target)
			if got := walk(1); !reflect.DeepEqual(got, want[:1]) {
				t.Fatalf("revision reset got %v want %v", got, want[:1])
			}
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
			if got := walk(100); !reflect.DeepEqual(got, want[1:]) {
				t.Fatalf("resumed NULL/full tuple got %v want %v", got, want[1:])
			}
			if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, target) != len(want) {
				t.Fatal("full tuple skipped or duplicated raw episode")
			}
		})
	}
}

func TestFacetProducerVersionSevenRejectsInterruptedGenreStage(t *testing.T) {
	db := cc1OpenDB(t, "facet-version-seven.sqlite")
	lib := cc1Library(t, db, "facet-version-seven", "Shows", "tv", "/shows")
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show", Title: "Show"}, map[string]any{"local_key": "show"})
	ep := cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Key: "episode", Title: "Episode"}, map[string]any{"show_id": show, "numbering": "seasonal", "number": 1})
	cc1Drain(t, db)
	cc1Exec(t, db, `UPDATE catalog_derivations SET version=6 WHERE domain=33`)
	cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(33,?,1)`, ep)
	cc1Exec(t, db, `INSERT INTO catalog_facet_work(entity_id,revision,source_epoch,phase,show_id) SELECT ?,1,source_epoch,4,? FROM catalog_facet_source_state`, ep, show)
	cc1Exec(t, db, `INSERT INTO catalog_facet_stage(entity_id,scope,field,value) VALUES(?,0,'genre','old interrupted genre')`, ep)
	cc1Exec(t, db, `INSERT INTO catalog_show_facet_episodes(episode_id,show_id,field,value) VALUES(?,?,'genre','old accepted genre')`, ep, show)
	cc1Exec(t, db, `INSERT INTO catalog_show_facet_values(show_id,field,value,total) VALUES(?,'genre','old accepted genre',1)`, show)
	cc1Drain(t, db)
	if cc1Scalar(t, db, `SELECT version FROM catalog_derivations WHERE domain=33`) != 7 {
		t.Fatal("domain33 version not rebuilt")
	}
	if cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_stage`)+cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_episodes WHERE field='genre'`)+cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_values WHERE field='genre'`) != 0 {
		t.Fatal("interrupted old genre stage survived upgrade")
	}
	if _, err := CaptureFacetProducerFence(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
