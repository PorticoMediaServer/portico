package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// deletionFixture is a film with two files in a library that allows deleting.
// The catalogue's size and time for each file are what is on disk, as a scan
// would have left them.
func deletionFixture(t *testing.T) (*Service, *sql.DB, string, string, []string) {
	t.Helper()
	s, db, root := newService(t)
	library, item, files := seedLibrary(t, db, root)
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE catalog_assets SET size=?,modified_ns=? WHERE path=?`, info.Size(), info.ModTime().UnixNano(), path); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := s.LibrarySettingsFor(context.Background(), allow, library)
	if err != nil {
		t.Fatal(err)
	}
	doc.Settings.AllowMediaDeletion = true
	if _, err = s.SaveLibrarySettings(context.Background(), allow, library, Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: doc.Settings, OperationID: "test-policy-key-1"}); err != nil {
		t.Fatal(err)
	}
	return s, db, root, item, files
}

func deleteItem(t *testing.T, s *Service, item, operation string) (DeleteResult, error) {
	t.Helper()
	p, err := s.PreviewDelete(context.Background(), allow, []string{item})
	if err != nil {
		return DeleteResult{}, err
	}
	return s.Delete(context.Background(), allow, "test-owner", DeleteRequest{ItemIDs: []string{item}, DeleteFiles: true, Confirmation: p.Confirmation, ExpectedRevision: p.Revision, OperationID: operation})
}

func TestDeleteIsRefusedUntilTheLibraryAllowsItAndTheOwnerConfirms(t *testing.T) {
	service, db, root := newService(t)
	library, item, paths := seedLibrary(t, db, root)
	ctx := context.Background()
	preview, err := service.PreviewDelete(ctx, allow, []string{item})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Blocked) != 1 || preview.Confirmation != "The Lighthouse" || preview.ConfirmationKind != "title" {
		t.Fatalf("preview %+v", preview)
	}
	if len(preview.Targets[0].Files) != 2 || !preview.Targets[0].Files[0].Present || preview.Targets[0].Titles != 1 {
		t.Fatalf("preview must list both present files: %+v", preview.Targets[0])
	}
	request := DeleteRequest{ItemIDs: []string{item}, DeleteFiles: true, Confirmation: "The Lighthouse", ExpectedRevision: preview.Revision, OperationID: "test-policy-key-8"}
	if _, err = service.Delete(ctx, allow, "local:a:b", request); !errors.Is(err, ErrDenied) {
		t.Fatalf("delete error %v, want denied while the library forbids it", err)
	}
	settings := DefaultLibrarySettings()
	settings.AllowMediaDeletion = true
	if _, err = service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: 1, Settings: settings, OperationID: "test-policy-key-9"}); err != nil {
		t.Fatal(err)
	}
	preview, err = service.PreviewDelete(ctx, allow, []string{item})
	if err != nil {
		t.Fatal(err)
	}
	wrong := DeleteRequest{ItemIDs: []string{item}, DeleteFiles: true, Confirmation: "the lighthouse", ExpectedRevision: preview.Revision, OperationID: "test-policy-key-10"}
	if _, err = service.Delete(ctx, allow, "local:a:b", wrong); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("delete error %v, want a confirmation mismatch", err)
	}
	stale := DeleteRequest{ItemIDs: []string{item}, DeleteFiles: true, Confirmation: "The Lighthouse", ExpectedRevision: preview.Revision + 99, OperationID: "test-policy-key-11"}
	if _, err = service.Delete(ctx, allow, "local:a:b", stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete error %v, want a revision conflict", err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("a refused delete touched %s: %v", path, err)
		}
	}
}

func TestDeleteRemovesTheFilesFromDiskAndKeepsNoCopy(t *testing.T) {
	s, db, root, item, paths := deletionFixture(t)
	ctx := context.Background()
	result, err := deleteItem(t, s, item, "delete-one")
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 || result.Failed != 0 || result.Receipts[0].FilesDeleted != 2 || result.BytesDeleted != 22 {
		t.Fatalf("result %+v", result)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s is still on disk: %v", path, err)
		}
	}
	// Nothing was put aside anywhere under the server's own folder.
	if _, err := os.Stat(filepath.Join(root, "trash")); !os.IsNotExist(err) {
		t.Fatalf("a trash folder exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "file-operations")); !os.IsNotExist(err) {
		t.Fatalf("a file-operation journal exists: %v", err)
	}
	var titles, files int
	if err = db.QueryRow(`SELECT (SELECT COUNT(*) FROM catalog_entities WHERE public_id=pid_blob(?)),(SELECT COUNT(*) FROM catalog_assets)`, item).Scan(&titles, &files); err != nil || titles != 0 || files != 0 {
		t.Fatalf("catalogue still holds the title or its files: %d %d %v", titles, files, err)
	}
	// Replaying the same operation identifier returns the first outcome.
	replay, err := s.Delete(ctx, allow, "test-owner", DeleteRequest{ItemIDs: []string{item}, DeleteFiles: true, Confirmation: "The Lighthouse", ExpectedRevision: 1, OperationID: "delete-one"})
	if err == nil && replay.Removed != 1 {
		t.Fatalf("replay %+v", replay)
	}
}

func TestDeleteLeavesAFileThatIsNotTheOneInTheLibrary(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	// Something else has taken the first file's place since the last scan.
	if err := os.WriteFile(paths[0], []byte("a different, longer file"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := deleteItem(t, s, item, "delete-changed")
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 || result.Failed != 1 || result.Receipts[0].Code != "file_changed" {
		t.Fatalf("result %+v", result)
	}
	if _, err = os.Stat(paths[0]); err != nil {
		t.Fatalf("the replacement file was removed: %v", err)
	}
	var titles int
	if err = db.QueryRow(`SELECT COUNT(*) FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&titles); err != nil || titles != 1 {
		t.Fatalf("the title must stay when its file could not be removed: %d %v", titles, err)
	}
}

func TestDeleteWithoutFilesLeavesThemAndKeepsEachProfilesState(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, db)
	if _, err := db.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision,watched,last_played_at,not_interested) VALUES('p1',?,0,1,9,1,1,'2026-10-01T00:00:00Z',0)`, c.ID(item)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('p1',?,1,'2026-10-01T00:00:00.000000000Z')`, c.ID(item)); err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewDelete(ctx, allow, []string{item})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Delete(ctx, allow, "test-owner", DeleteRequest{ItemIDs: []string{item}, DeleteFiles: false, Confirmation: p.Confirmation, ExpectedRevision: p.Revision, OperationID: "record-only-1"})
	if err != nil || result.Removed != 1 || !result.FilesRetained || result.Receipts[0].FilesDeleted != 0 {
		t.Fatalf("result %+v %v", result, err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s should still be on disk: %v", path, err)
		}
	}
	var favorite, watched bool
	var rating float64
	if err = db.QueryRow(`SELECT favorite,watched,rating FROM retained_title_state WHERE profile_id='p1'`).Scan(&favorite, &watched, &rating); err != nil || !favorite || !watched || rating != 9 {
		t.Fatalf("kept state: favorite=%v watched=%v rating=%v err=%v", favorite, watched, rating, err)
	}
}

func TestP252bDeleteAllFortyThreePinnedMembers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, root := newService(t)
	c := catalogtest.New(t, db)
	library := c.Library("music", "Music", "music", root)
	artist := c.Artist(library, "Artist")
	album := c.Album(artist, "Album", 2000)
	for i := 0; i < 43; i++ {
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
	doc, e := s.LibrarySettingsFor(context.Background(), allow, "music")
	if e != nil {
		t.Fatal(e)
	}
	doc.Settings.AllowMediaDeletion = true
	if _, e = s.SaveLibrarySettings(context.Background(), allow, "music", Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: doc.Settings, OperationID: "music-delete-policy"}); e != nil {
		t.Fatal(e)
	}
	preview, e := s.PreviewDelete(context.Background(), allow, []string{album.Public})
	if e != nil {
		t.Fatal(e)
	}
	request := DeleteRequest{ItemIDs: []string{album.Public}, ExpectedRevision: preview.Revision, OperationID: "delete-forty-three", Confirmation: preview.Confirmation, DeleteFiles: false}
	out, e := s.Delete(context.Background(), allow, "test-owner", request)
	if e != nil || out.Removed != 1 || out.Failed != 0 {
		t.Fatal(out, e)
	}
	var remaining int
	if e = db.QueryRow(`SELECT count(*) FROM catalog_songs`).Scan(&remaining); e != nil || remaining != 0 {
		t.Fatal("skipped remaining member", remaining, e)
	}
	var anchor string
	if e = db.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE owner_kind='administration' AND parent_mutation_id IS NULL AND acknowledged=1 AND child_count=42`).Scan(&anchor); e != nil {
		t.Fatal("final receipt did not acknowledge actual command anchor", e)
	}
	for i := 0; i < 180; i++ {
		dbwork.ResetPlans()
		job, e := catalog.New(db).MutationAdapter().Step(context.Background(), "cleanup")
		if e != nil {
			t.Fatal(e)
		}
		if changed := dbwork.Plans().Largest.Rows; changed > 24 {
			t.Fatal("cleanup exceeded24 changed rows", changed)
		}
		var debt int
		err := db.QueryRow(`SELECT child_count FROM catalog_entity_mutations WHERE mutation_id=?`, anchor).Scan(&debt)
		if err == sql.ErrNoRows {
			var children int
			db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE parent_mutation_id=?`, anchor).Scan(&children)
			if children != 0 {
				t.Fatal("anchor reaped before children")
			}
		}
		if job.State == "succeeded" {
			break
		}
		if i == 179 {
			t.Fatal("administration cleanup did not finish")
		}
	}
	if n := rowsAdminMutationCount(t, db); n != 0 {
		t.Fatal("administration terminal history leaked", n)
	}
	replay, e := s.Delete(context.Background(), allow, "test-owner", request)
	if e != nil || replay.Removed != 1 {
		t.Fatal("receipt after all terminal outcomes", replay, e)
	}
}

func rowsAdminMutationCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='administration'`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
