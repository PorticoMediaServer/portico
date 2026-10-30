package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func publishArtistHandoffFixture(t *testing.T, db *sql.DB, album int64, side int) string {
	t.Helper()
	if _, e := db.Exec(`UPDATE mb_jobs SET status='needs_selection' WHERE kind<>'album' OR entity_id<>?`, album); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`UPDATE mb_jobs SET status='pending' WHERE kind='album' AND entity_id=?`, album); e != nil {
		t.Fatal(e)
	}
	s := New(db, "")
	j, e := s.claimMB(context.Background())
	if e != nil || j == nil || j.base.Entity != album {
		t.Fatal(j, e)
	}
	value := mbLaneReleaseFixture()
	value.ID = fmt.Sprintf("%08x-2222-2222-2222-222222222222", side)
	value.ArtistCredit = mbLaneRecordingFixture().ArtistCredit
	if e = s.mbPublishAlbum(context.Background(), *j, metadataprovider.ReleaseLookup{RequestedID: value.ID, Release: value}, "2026-10-28T00:00:00Z"); e != nil {
		t.Fatal(e)
	}
	var root string
	if e = db.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE owner_kind='mb-adoption' AND cause_operation=?`, j.operation).Scan(&root); e != nil {
		t.Fatal(e)
	}
	return root
}
func reachArtistWaiting(t *testing.T, db *sql.DB, root string) {
	t.Helper()
	for i := 0; i < 12; i++ {
		var status string
		if e := db.QueryRow(`SELECT status FROM catalog_entity_mutations WHERE mutation_id=?`, root).Scan(&status); e != nil {
			t.Fatal(e)
		}
		if status == "waiting" {
			return
		}
		if _, e := catalog.New(db).MutationAdapter().Step(context.Background(), root); e != nil {
			t.Fatal(e)
		}
	}
	t.Fatal("album did not reach artist waiting cause")
}
func driveArtistHandoffOperations(t *testing.T, db *sql.DB) {
	t.Helper()
	for turn := 0; turn < 120; turn++ {
		rows, e := db.Query(`SELECT resource FROM console_operations WHERE kind='catalog-mutation' AND state IN('queued','running','reconciling') ORDER BY sequence LIMIT 16`)
		if e != nil {
			t.Fatal(e)
		}
		var resources []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				t.Fatal(e)
			}
			resources = append(resources, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
		if len(resources) == 0 {
			return
		}
		for _, id := range resources {
			if _, e = catalog.New(db).MutationAdapter().Step(context.Background(), id); e != nil {
				t.Fatal("resource", id, e)
			}
		}
	}
	t.Fatal("artist handoff operations deadlocked")
}
func TestP252bArtistHandoffTwoAlbumsAndTwoArtists(t *testing.T) {
	for _, identity := range []string{"sameArtist", "knownOtherArtist", "newOtherArtist"} {
		t.Run(identity, func(t *testing.T) {
			db, names := mbLaneDB(t)
			c := catalogtest.New(t, db)
			artist := names["artist"]
			if identity != "sameArtist" {
				c.Write(func(ctx context.Context, tx *sql.Tx) error {
					id, _, e := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: 1, Kind: compactcatalog.Artist, Key: "separate-artist", Title: "Artist"})
					if e != nil {
						return e
					}
					artist.ID = id
					if e = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"local_key": "separate"}); e != nil {
						return e
					}
					if identity == "knownOtherArtist" {
						return compactcatalog.RecordProviderIdentityTx(ctx, tx, id, compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "artist", ID: mbLaneArtistID})
					}
					return nil
				})
			}
			second := c.Album(artist, "Second Album", 2020)
			c.Song(second, 3, "/music/second.flac", "Local Song")
			c.Drain()
			a := publishArtistHandoffFixture(t, db, names["album"].ID, 51)
			b := publishArtistHandoffFixture(t, db, second.ID, 52)
			reachArtistWaiting(t, db, a)
			reachArtistWaiting(t, db, b)
			// Waiting roots retain exact lifetime/ancestor holds; outgoing Artist
			// deletion/merge still cannot steal their read-only cause custody.
			e := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				_, e := compactcatalog.BeginEntityMutationTx(context.Background(), tx, compactcatalog.MutationIntent{Entity: names["artist"].ID, OwnerKind: "merge", OwnerID: "steal", OwnerGeneration: 1, Mode: "remove"})
				return e
			})
			if !errors.Is(e, compactcatalog.ErrEntityMutating) {
				t.Fatal("waiting outgoing fence bypassed", e)
			}
			out, e := catalog.New(db).MutationAdapter().Step(context.Background(), a)
			if e != nil || out.State != "succeeded" {
				t.Fatal("album cause did not hand off", out, e)
			}
			var independent int
			if e = db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations m JOIN console_operations o ON o.kind='catalog-mutation' AND o.resource=m.mutation_id WHERE m.owner_kind='identity' AND m.entity_kind=5 AND m.status='active' AND m.parent_mutation_id IS NULL`).Scan(&independent); e != nil || independent != 1 {
				t.Fatal("cause completion lost independent Artist root", independent, e)
			}
			if identity == "newOtherArtist" {
				out, e = catalog.New(db).MutationAdapter().Step(context.Background(), b)
				if e != nil || out.State != "running" {
					t.Fatal("new identity entered old frozen root", out, e)
				}
			}
			// Real restart reconciliation state, not just a fresh Service pointer.
			if _, e = db.Exec(`UPDATE console_operations SET state='reconciling',revision=revision+1 WHERE kind='catalog-mutation' AND state IN('queued','running')`); e != nil {
				t.Fatal(e)
			}
			driveArtistHandoffOperations(t, db)
			var active, artists int
			db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE status<>'terminal'`).Scan(&active)
			db.QueryRow(`SELECT count(*) FROM catalog_artists`).Scan(&artists)
			if active != 0 || artists != 1 {
				t.Fatal("artist identity continuation incomplete", active, artists)
			}
		})
	}
}
func TestP252bWaitingArtistCauseStalesOnPolicyChange(t *testing.T) {
	db, names := mbLaneDB(t)
	root := publishArtistHandoffFixture(t, db, names["album"].ID, 53)
	reachArtistWaiting(t, db, root)
	if _, e := db.Exec(`UPDATE mb_provider_policies SET enabled=0,revision=revision+1 WHERE library_id='lib'`); e != nil {
		t.Fatal(e)
	}
	out, e := catalog.New(db).MutationAdapter().Step(context.Background(), root)
	if e != nil || out.State != "succeeded" || out.Phase != "stale" {
		t.Fatal(out, e)
	}
	var artistRoots int
	db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='identity' AND entity_kind=5`).Scan(&artistRoots)
	if artistRoots != 0 {
		t.Fatal("stale waiting cause admitted artist")
	}
}

func TestP252bWaitingArtistBindingGuardsAndFreshness(t *testing.T) {
	for _, change := range []string{"binding", "source", "rematch"} {
		t.Run(change, func(t *testing.T) {
			db, names := mbLaneDB(t)
			root := publishArtistHandoffFixture(t, db, names["album"].ID, 54)
			reachArtistWaiting(t, db, root)
			if change == "binding" {
				for _, set := range []string{"public_id='forged'", "entity_incarnation='forged'", "subject_ancestor_1=" + fmt.Sprint(names["song"].ID), "mode='remove'"} {
					if _, e := db.Exec(`UPDATE catalog_entity_mutations SET `+set+` WHERE mutation_id=?`, root); e == nil {
						t.Fatal("malformed waiting update accepted", set)
					}
				}
				rows, e := db.Query(`PRAGMA table_info(catalog_entity_mutations)`)
				if e != nil {
					t.Fatal(e)
				}
				var columns, values []string
				for rows.Next() {
					var cid, nn, pk int
					var name, typ string
					var def any
					if e = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
						t.Fatal(e)
					}
					columns = append(columns, name)
					switch name {
					case "mutation_id":
						values = append(values, "'forged-insert'")
					case "owner_id":
						values = append(values, "'forged-owner'")
					case "public_id":
						values = append(values, "'forged'")
					default:
						values = append(values, name)
					}
				}
				if e = rows.Err(); e != nil {
					t.Fatal(e)
				}
				rows.Close()
				if _, e = db.Exec(`INSERT INTO catalog_entity_mutations(`+strings.Join(columns, ",")+`) SELECT `+strings.Join(values, ",")+` FROM catalog_entity_mutations WHERE mutation_id=?`, root); e == nil {
					t.Fatal("malformed direct waiting INSERT accepted")
				}
				return
			}
			if change == "source" {
				if _, e := db.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=?`, names["song"].Asset); e != nil {
					t.Fatal(e)
				}
				// Album custody includes its own files, not descendant files. Change
				// its own lifetime to exercise an actual waiting source fence.
				if _, e := db.Exec(`UPDATE mb_album_heads SET incarnation='replaced-incarnation' WHERE album_id=?`, names["album"].ID); e != nil {
					t.Fatal(e)
				}
			} else {
				mbFixturePin(t, db, mbFixturePinIntent{Link: "album", Entity: names["album"].ID}, func(tx *sql.Tx) error {
					_, err := tx.Exec(`DELETE FROM mb_album_links WHERE album_id=?`, names["album"].ID)
					return err
				})
			}
			out, e := catalog.New(db).MutationAdapter().Step(context.Background(), root)
			if e != nil || out.State != "succeeded" || out.Phase != "stale" {
				t.Fatal(out, e)
			}
			var artists int
			db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='identity' AND entity_kind=5`).Scan(&artists)
			if artists != 0 {
				t.Fatal("stale waiting binding admitted artist", artists)
			}
		})
	}
}

func TestP252bNestedAlbumAdoptionRecordingConflictUnwindsEveryParent(t *testing.T) {
	db, names := mbLaneDB(t)
	c := catalogtest.New(t, db)
	other := c.Album(names["artist"], "Duplicate Album", 2020)
	song := c.Song(other, 3, "/music/duplicate.flac", "Local Song")
	c.Fields(song.ID, map[string]any{"album_id": other.ID, "disc_number": 2, "track_number": 3})
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "release", ID: fmt.Sprintf("%08x-2222-2222-2222-222222222222", 55)}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if e := compactcatalog.RecordProviderIdentityTx(ctx, tx, other.ID, p); e != nil {
			return e
		}
		if e := compactcatalog.RecordProviderIdentityTx(ctx, tx, song.ID, compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "recording", ID: "conflicting-recording"}); e != nil {
			return e
		}
		return compactcatalog.RecordProviderIdentityTx(ctx, tx, names["song"].ID, compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "recording", ID: mbLaneRecord})
	})
	c.Drain()
	root := publishArtistHandoffFixture(t, db, names["album"].ID, 55)
	var revision, release string
	if e := db.QueryRow(`SELECT release_revision,release_id FROM mb_album_links WHERE album_id=?`, names["album"].ID).Scan(&revision, &release); e != nil {
		t.Fatal(e)
	}
	c.Exec(`INSERT INTO mb_song_links(item_id,release_revision,release_id,observed_at) VALUES(?,?,?,'now')`, song.ID, revision, release)
	done := false
	for turn := 0; turn < 24; turn++ {
		out, e := catalog.New(db).MutationAdapter().Step(context.Background(), root)
		if e != nil {
			t.Fatal("nested permanent conflict retried", out, e)
		}
		if out.State == "succeeded" {
			if out.Phase != "stale" {
				t.Fatal(out)
			}
			done = true
			break
		}
	}
	if !done {
		t.Fatal("nested adoption did not unwind")
	}
	var active, pins, entities int
	db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE family_id=? AND status<>'terminal'`, root).Scan(&active)
	db.QueryRow(`SELECT count(*) FROM mb_song_links WHERE item_id=?`, song.ID).Scan(&pins)
	db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE id IN(?,?,?,?)`, other.ID, song.ID, names["album"].ID, names["song"].ID).Scan(&entities)
	if active != 0 || pins != 1 || entities != 4 {
		t.Fatal("nested conflict lost pin/source or left parent fence", active, pins, entities)
	}
}
