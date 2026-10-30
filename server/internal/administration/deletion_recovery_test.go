package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestP252bInterruptedDeleteFreshApprovalResumesAfterRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, stop := range []string{"cancel", "policy"} {
		t.Run(stop, func(t *testing.T) {
			s, db, root := newService(t)
			c := catalogtest.New(t, db)
			lib := c.Library("music", "Music", "music", root)
			artist := c.Artist(lib, "Artist")
			album := c.Album(artist, "Album", 2000)
			for i := 0; i < 2; i++ {
				c.Song(album, i+1, filepath.Join(root, fmt.Sprintf("song-%d.flac", i)), fmt.Sprintf("Song %d", i))
			}
			c.Exec(`INSERT INTO mb_release_evidence(revision_id,provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,observed_at,payload) VALUES('r','r',printf('%064d',0),'','','','','','','','','','now','{}')`)
			c.Exec(`UPDATE mb_release_evidence SET sealed=1 WHERE revision_id='r'`)
			rows, e := db.Query(`SELECT entity_id FROM catalog_songs WHERE album_id=? ORDER BY entity_id`, album.ID)
			if e != nil {
				t.Fatal(e)
			}
			var pinSongs []int64
			for rows.Next() {
				var id int64
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					t.Fatal(e)
				}
				pinSongs = append(pinSongs, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				t.Fatal(e)
			}
			for _, id := range pinSongs {
				c.PinMBRelease(id, false, "r")
			}
			c.Drain()
			setDeletionPolicy := func(enabled bool, op string) {
				t.Helper()
				doc, e := s.LibrarySettingsFor(context.Background(), allow, "music")
				if e != nil {
					t.Fatal(e)
				}
				doc.Settings.AllowMediaDeletion = enabled
				if _, e = s.SaveLibrarySettings(context.Background(), allow, "music", Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: doc.Settings, OperationID: op}); e != nil {
					t.Fatal(e)
				}
			}
			setDeletionPolicy(true, "allow-old")
			preview, e := s.PreviewDelete(context.Background(), allow, []string{album.Public})
			if e != nil {
				t.Fatal(e)
			}
			old := DeleteRequest{ItemIDs: []string{album.Public}, ExpectedRevision: preview.Revision, Confirmation: preview.Confirmation, OperationID: "interrupted-old"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopped := false
			auth := func(ctx context.Context, tx *sql.Tx) error {
				var remaining, drained int
				if e := tx.QueryRow(`SELECT count(*) FROM catalog_songs WHERE album_id=?`, album.ID).Scan(&remaining); e != nil {
					return e
				}
				if e := tx.QueryRow(`SELECT count(*) FROM catalog_entity_mutations m WHERE m.owner_kind='administration' AND m.status='active' AND NOT EXISTS(SELECT 1 FROM mb_song_links p WHERE p.item_id=m.entity_id)`).Scan(&drained); e != nil {
					return e
				}
				if remaining == 1 && drained > 0 {
					stopped = true
					if stop == "cancel" {
						cancel()
						return context.Canceled
					}
					// Exercise the actual per-writer policy read in the same serialized
					// transaction. Its refusal rolls this fixture change back as well.
					_, e := tx.Exec(`UPDATE admin_documents SET body=json_set(body,'$.allowMediaDeletion',json('false')) WHERE scope=?`, libraryScope("music"))
					return e
				}
				return nil
			}
			_, e = s.Delete(ctx, auth, "owner", old)
			want := error(context.Canceled)
			if stop == "policy" {
				want = ErrDenied
			}
			if !stopped || !errors.Is(e, want) {
				t.Fatal("did not stop after physical progress", stopped, e)
			}
			var remaining, active, receipts int
			db.QueryRow(`SELECT count(*) FROM catalog_songs WHERE album_id=?`, album.ID).Scan(&remaining)
			db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='administration' AND status='active'`).Scan(&active)
			db.QueryRow(`SELECT count(*) FROM admin_receipts WHERE operation_id='interrupted-old'`).Scan(&receipts)
			if remaining != 1 || active != 1 || receipts != 0 {
				t.Fatal("partial command proof", remaining, active, receipts)
			}
			if stop == "policy" {
				setDeletionPolicy(false, "policy-off")
				if _, e = s.Delete(context.Background(), allow, "owner", old); !errors.Is(e, ErrDenied) {
					t.Fatal("policy-off replay", e)
				}
				setDeletionPolicy(true, "policy-on")
			}
			// A native DB reopen discards service state and callbacks. Only fresh
			// authority plus current approved files can consume the old custody.
			if e = db.Close(); e != nil {
				t.Fatal(e)
			}
			db, e = persistence.Open(filepath.Join(root, "server.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			s = New(db)
			if _, e = s.Delete(context.Background(), allow, "owner", old); !errors.Is(e, ErrConflict) {
				t.Fatal("old approval unexpectedly replayed", e)
			}
			preview, e = s.PreviewDelete(context.Background(), allow, []string{album.Public})
			if e != nil {
				t.Fatal(e)
			}
			fresh := DeleteRequest{ItemIDs: []string{album.Public}, ExpectedRevision: preview.Revision, Confirmation: preview.Confirmation, OperationID: "fresh-resume"}
			// Change actual file custody after the captured plan, before its first
			// supersession turn. The old family must not be acknowledged by this.
			calls := 0
			changed := func(ctx context.Context, tx *sql.Tx) error {
				calls++
				if calls == 2 {
					_, e := tx.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=(SELECT l.asset_id FROM catalog_asset_links l JOIN catalog_entity_mutations m ON m.entity_id=l.entity_id WHERE m.status='active' AND m.owner_kind='administration' LIMIT 1)`)
					return e
				}
				return nil
			}
			out, e := s.Delete(context.Background(), changed, "owner", fresh)
			if !errors.Is(e, compactcatalog.ErrEntityMutating) && (e != nil || out.Removed != 0 || out.Failed != 1) {
				t.Fatal("changed-file approval", out, e)
			}
			db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='administration' AND cause_operation='interrupted-old' AND status='active'`).Scan(&active)
			if active != 1 {
				t.Fatal("changed file consumed old custody", active)
			}
			preview, e = s.PreviewDelete(context.Background(), allow, []string{album.Public})
			if e != nil {
				t.Fatal(e)
			}
			fresh.OperationID, fresh.ExpectedRevision = "fresh-current-resume", preview.Revision
			out, e = s.Delete(context.Background(), allow, "owner", fresh)
			if e != nil || out.Removed != 1 || out.Failed != 0 {
				t.Fatal("authorized fresh resume", out, e)
			}
			db.QueryRow(`SELECT count(*) FROM catalog_songs`).Scan(&remaining)
			db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE status IN('active','blocked')`).Scan(&active)
			db.QueryRow(`SELECT count(*) FROM admin_receipts WHERE operation_id='interrupted-old'`).Scan(&receipts)
			if remaining != 0 || active != 0 || receipts != 0 {
				t.Fatal("resume left debt or invented old success", remaining, active, receipts)
			}
			replay, e := s.Delete(context.Background(), allow, "owner", fresh)
			if e != nil || replay.Removed != 1 {
				t.Fatal("durable fresh receipt replay", replay, e)
			}
		})
	}
}

func TestP252bDirectEpisodeAdministrationDefersBoundedEmptyShow(t *testing.T) {
	s, db, root := newService(t)
	c := catalogtest.New(t, db)
	library := c.Library("tv", "TV", "tv", root)
	show := c.Show(library, "Show", 2020)
	season := c.Season(show, 1)
	for n := 2; n <= 41; n++ {
		c.Season(show, n)
	}
	ep := c.Episode(show, season, 1, filepath.Join(root, "episode.mkv"))
	c.Drain()
	doc, e := s.LibrarySettingsFor(context.Background(), allow, "tv")
	if e != nil {
		t.Fatal(e)
	}
	doc.Settings.AllowMediaDeletion = true
	if _, e = s.SaveLibrarySettings(context.Background(), allow, "tv", Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: doc.Settings, OperationID: "episode-policy"}); e != nil {
		t.Fatal(e)
	}
	preview, e := s.PreviewDelete(context.Background(), allow, []string{ep.Public})
	if e != nil {
		t.Fatal(e)
	}
	request := DeleteRequest{ItemIDs: []string{ep.Public}, ExpectedRevision: preview.Revision, Confirmation: preview.Confirmation, OperationID: "episode-delete"}
	out, e := s.Delete(context.Background(), allow, "owner", request)
	if e != nil || out.Removed != 1 {
		t.Fatal(out, e)
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show.ID).Scan(&count)
	if count != 40 {
		t.Fatal("admin Episode writer deleted all empty Seasons", count)
	}
	var id string
	if e = db.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE owner_kind='empty-parent' AND subject_id=? AND status='active'`, show.ID).Scan(&id); e != nil {
		t.Fatal(e)
	}
	for turn := 0; turn < 44; turn++ {
		before := count
		job, e := catalog.New(db).MutationAdapter().Step(context.Background(), id)
		if e != nil {
			t.Fatal(job, e)
		}
		db.QueryRow(`SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show.ID).Scan(&count)
		if before-count > 1 {
			t.Fatal("admin parent continuation bulk deleted", before, count)
		}
		if job.State == "succeeded" {
			break
		}
	}
	db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE id=?`, show.ID).Scan(&count)
	if count != 0 {
		t.Fatal("admin empty Show consequence did not finish")
	}
	replay, e := s.Delete(context.Background(), allow, "owner", request)
	if e != nil || replay.Removed != 1 {
		t.Fatal("admin receipt depended on pending parent", replay, e)
	}
}
