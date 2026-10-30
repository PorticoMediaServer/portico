package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Drive the admitted command and actual removal batches; never manufacture
// terminal status, cursor, acknowledgement, or custody rows.
func TestSeparateFolderActiveRemovalLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 5.26s")
	}
	for _, library := range []bool{false, true} {
		t.Run(fmt.Sprintf("library=%v", library), func(t *testing.T) {
			f := newSeparateAsyncFixture(t, 20)
			keep := f.c.Library("keep", "Kept", "music", "/keep")
			artist := f.c.Artist(keep, "Kept artist")
			album := f.c.Album(artist, "Kept album", 2021)
			song := f.c.Song(album, 1, "/keep/one.flac", "Kept song")
			f.c.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms)
 VALUES('kept-job',?,?,'keep','profile','target','{}','revision','{}','queued','waiting',1,2)`, song.ID, song.Token)
			f.c.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,hash_state,created_ms,updated_ms)
 VALUES('kept-preparation','profile','local','owner','owner-profile',?,'keep','original','item','ready',X'00',1,2)`, song.ID)
			f.c.Exec(`INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms,revoked)
 VALUES('kept-grant','kept-preparation','profile',?,1,99,0)`, song.ID)
			keptClaims := retirementSnapshot(t, f.c)
			f.c.Drain()
			s := New(f.c.DB)
			id, err := s.SeparateFolder(WithSeparateFolderActor(context.Background(), f.principal), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
			if id != "" || !errors.Is(err, ErrSeparateFolderPending) {
				t.Fatalf("admission %q %v", id, err)
			}
			root := separateAsyncRoot(t, f.c)
			target := separateAsyncTarget(t, f)
			remaining := rows(t, f.c, `SELECT count(*) FROM catalog_songs s JOIN catalog_asset_links l ON l.entity_id=s.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE s.album_id=? AND a.path LIKE '/music/mp3/%'`, f.album)
			if remaining == 0 {
				t.Fatal("command has no remaining selected files")
			}
			revision := separateAsyncInt(t, f.c, `SELECT revision FROM library_configuration WHERE library_id='music'`)
			auth := func(*sql.Tx) error { return nil }
			enqueue := func(context.Context, *sql.Tx, string, string) error { return nil }
			if library {
				err = s.RemoveLibrary(context.Background(), "music", "Music", revision, auth, enqueue)
			} else {
				err = s.RemoveSource(context.Background(), "music", f.source, revision, auth, enqueue)
			}
			if err != nil {
				t.Fatalf("real removal admission: %T %v (root=%s target=%d remaining=%d)", err, err, root, target, remaining)
			}
			before := separateAsyncEffects(t, f)
			out := separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
			if out.State == "succeeded" {
				t.Fatalf("removed command succeeded: %+v", out)
			}
			if after := separateAsyncEffects(t, f); after != before {
				t.Fatalf("unauthorized moves: %+v -> %+v", before, after)
			}
			done := false
			backpressure := 0
			for turn := 0; turn < 400; turn++ {
				// The real scheduler retries a rolled-back Step on its next turn.
				// A held SeparateFolder family must refuse acquisition until its
				// own adapter releases custody; never bypass that fence here.
				batchErr := dbwork.WithWriteTx(MutationWriteContext(context.Background()), f.c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
					var err error
					if library {
						done, err = removeLibraryBatchTx(context.Background(), tx, "music")
					} else {
						done, err = removeFolderBatchTx(context.Background(), tx, f.source)
					}
					return err
				})
				if batchErr != nil {
					if !library || !errors.Is(batchErr, compactcatalog.ErrEntityMutating) {
						t.Fatalf("removal turn=%d: %T %v", turn, batchErr, batchErr)
					}
					backpressure++
				}
				if done {
					break
				}
				separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
				// Compact catalogue derivation is a separate production rail.
				// Let it settle the committed removal before the next batch.
				f.c.Drain()
			}
			if !done {
				t.Fatal("removal worker did not finish within 400 turns")
			}
			if library && backpressure == 0 {
				t.Fatal("library removal never observed the live custody fence")
			}
			f.c.Drain()
			f.c.Write(func(ctx context.Context, tx *sql.Tx) error { return EnqueueMutationCleanupTx(ctx, tx) })
			for turn := 0; turn < 400; turn++ {
				if rows(t, f.c, `SELECT count(*) FROM catalog_entity_mutations`) == 0 {
					break
				}
				if rows(t, f.c, `SELECT count(*) FROM catalog_entity_mutations WHERE mutation_id=?`, root) != 0 {
					separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, root)
				}
				if cleanup := separateAsyncAdapterStep(t, f.c.DB, SeparateFolderHooks{}, mutationCleanupResource); cleanup.State == "succeeded" {
					break
				}
			}
			for _, table := range []string{"catalog_entity_mutations", "catalog_purge_jobs", "catalog_removal_progress"} {
				if n := rows(t, f.c, `SELECT count(*) FROM `+table); n != 0 {
					t.Errorf("dangling %s: %d", table, n)
					if table == "catalog_entity_mutations" {
						rs, e := f.c.DB.Query(`SELECT mutation_id,owner_kind,status,outcome,acknowledged,child_count FROM catalog_entity_mutations`)
						if e != nil {
							t.Fatal(e)
						}
						for rs.Next() {
							var id, owner, status, outcome string
							var ack, children int
							if e := rs.Scan(&id, &owner, &status, &outcome, &ack, &children); e != nil {
								t.Fatal(e)
							}
							t.Logf("mutation %s owner=%s status=%s outcome=%s ack=%d children=%d", id, owner, status, outcome, ack, children)
						}
						rs.Close()
					}
				}
			}
			if n := rows(t, f.c, `SELECT count(*) FROM catalog_songs WHERE entity_id=?`, song.ID); n != 1 {
				t.Fatalf("sibling song changed: %d", n)
			}
			if after := retirementSnapshot(t, f.c); !reflect.DeepEqual(after, keptClaims) {
				t.Fatalf("sibling preparation/job/grant changed: %#v -> %#v", keptClaims, after)
			}
			if library {
				if n := rows(t, f.c, `SELECT count(*) FROM catalog_libraries WHERE library_id='music'`); n != 0 {
					t.Fatalf("library retained: %d", n)
				}
			} else {
				if n := rows(t, f.c, `SELECT count(*) FROM inventory_objects WHERE source_id=? AND state<>'missing' AND retired=0`, f.source); n != 0 {
					t.Fatalf("source files not missing: %d", n)
				}
				if n := rows(t, f.c, `SELECT count(*) FROM catalog_albums WHERE entity_id IN (?,?)`, f.album, target); n != 2 {
					t.Fatalf("folder removal lost retained albums: %d", n)
				}
			}
		})
	}
}
