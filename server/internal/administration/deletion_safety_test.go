package administration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

func TestDeleteRejectsParentSymlinkEscape(t *testing.T) {
	s, _, root, item, paths := deletionFixture(t)
	media := filepath.Join(root, "media")
	outside := t.TempDir()
	if e := os.Rename(media, filepath.Join(outside, "outside-media")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(outside, "outside-media"), media); e != nil {
		t.Fatal(e)
	}
	out, e := deleteItem(t, s, item, "delete-escaped")
	if e != nil {
		t.Fatal(e)
	}
	victim := filepath.Join(outside, "outside-media", filepath.Base(paths[0]))
	if _, e := os.Stat(victim); os.IsNotExist(e) {
		t.Fatalf("file outside library root deleted: %+v", out)
	}
}

func TestDeleteReportsNonRegularReplacement(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	if e := os.Remove(paths[0]); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(paths[0], 0700); e != nil {
		t.Fatal(e)
	}
	out, e := deleteItem(t, s, item, "delete-directory")
	if e != nil {
		t.Fatal(e)
	}
	if out.Removed != 0 || out.Receipts[0].Code != "file_changed" {
		var n int
		db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&n)
		t.Fatalf("non-regular replacement was treated as gone: %+v; titles remaining=%d", out, n)
	}
}

func TestDeleteDoesNotRemoveNewUnpreviewedVersion(t *testing.T) {
	s, db, root, item, _ := deletionFixture(t)
	p, e := s.PreviewDelete(context.Background(), allow, []string{item})
	if e != nil {
		t.Fatal(e)
	}
	newPath := filepath.Join(root, "media", "new-version.mkv")
	if e := os.WriteFile(newPath, []byte("new version"), 0600); e != nil {
		t.Fatal(e)
	}
	calls := 0
	auth := func(ctx context.Context, tx *sql.Tx) error {
		calls++
		if calls != 2 {
			return nil
		}
		var entity int64
		if e := tx.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&entity); e != nil {
			return e
		}
		a, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: newPath, Size: 11})
		if e != nil {
			return e
		}
		return compactcatalog.LinkAssetTx(ctx, tx, entity, a, compactcatalog.Link{})
	}
	out, e := s.Delete(context.Background(), auth, "owner", DeleteRequest{ItemIDs: []string{item}, DeleteFiles: true, Confirmation: p.Confirmation, ExpectedRevision: p.Revision, OperationID: "delete-new-version"})
	if e != nil {
		t.Fatal(e)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM catalog_assets WHERE path=?`, newPath).Scan(&n)
	if n != 1 {
		t.Fatalf("unpreviewed version lost from catalogue while its disk file remains: %+v", out)
	}
	if out.Removed != 0 || out.Receipts[0].Code != "file_changed" {
		t.Fatalf("changed title reported as removed: %+v", out)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog_assets WHERE path<>? AND available=1`, newPath).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("deleted old versions still offered: %d", n)
	}
}

func TestDeleteRejectsNestedSymlinkEscape(t *testing.T) {
	s, db, root, item, paths := deletionFixture(t)
	sub := filepath.Join(root, "media", "nested")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(sub, filepath.Base(paths[0]))
	if err := os.Rename(paths[0], nested); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_assets SET path=? WHERE path=?`, nested, paths[0]); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Rename(sub, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, sub); err != nil {
		t.Fatal(err)
	}
	out, err := deleteItem(t, s, item, "delete-nested-escape")
	if err != nil {
		t.Fatal(err)
	}
	if out.Removed != 0 {
		t.Fatalf("redirected delete succeeded: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(outside, filepath.Base(paths[0]))); err != nil {
		t.Fatalf("outside file was touched: %v", err)
	}
}

func TestDeleteRejectsFinalSymlinkReplacement(t *testing.T) {
	s, _, _, item, paths := deletionFixture(t)
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(paths[1], paths[0]); err != nil {
		t.Fatal(err)
	}
	out, err := deleteItem(t, s, item, "delete-final-symlink")
	if err != nil {
		t.Fatal(err)
	}
	if out.Removed != 0 || out.Receipts[0].Code != "file_changed" {
		t.Fatalf("symlink replacement accepted: %+v", out)
	}
	if _, err := os.Stat(paths[1]); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteDoesNotRemoveNewUnpreviewedEpisode(t *testing.T) {
	s, db, root, _, _ := deletionFixture(t)
	c := catalogtest.New(t, db)
	show := c.Show(c.Handle("lib-1"), "Series", 2001)
	season := c.Season(show, 1)
	oldPath := filepath.Join(root, "media", "episode1.mkv")
	if err := os.WriteFile(oldPath, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Episode(show, season, 1, oldPath)
	info, err := os.Stat(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE catalog_assets SET size=?,modified_ns=? WHERE path=?`, info.Size(), info.ModTime().UnixNano(), oldPath)
	preview, err := s.PreviewDelete(context.Background(), allow, []string{show.Public})
	if err != nil {
		t.Fatal(err)
	}
	handle := c.Handle("lib-1")
	calls := 0
	auth := func(ctx context.Context, tx *sql.Tx) error {
		calls++
		if calls != 2 {
			return nil
		}
		episode, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Episode, Parent: season.ID, Key: "episode:new", Title: "Episode 2"})
		if err != nil {
			return err
		}
		if err := compactcatalog.SetFactsTx(ctx, tx, episode, map[string]any{"show_id": show.ID, "season_id": season.ID, "numbering": "seasonal", "number": 2}); err != nil {
			return err
		}
		return nil
	}
	out, err := s.Delete(context.Background(), auth, "owner", DeleteRequest{ItemIDs: []string{show.Public}, DeleteFiles: true, Confirmation: preview.Confirmation, ExpectedRevision: preview.Revision, OperationID: "delete-new-episode"})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_episodes WHERE show_id=? AND number=2`, show.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 || out.Removed != 0 {
		t.Fatalf("new episode or its show removed: episode=%d result=%+v", n, out)
	}
}

func TestDeleteRejectsReplacementRootWithMatchingFiles(t *testing.T) {
	s, db, root, item, paths := deletionFixture(t)
	media := filepath.Join(root, "media")
	observed, err := storage.LocalInventoryDirectory(storage.InventoryRequest{Root: media, RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE library_sources SET root_identity=? WHERE library_id='lib-1'`, storage.RootIdentity(observed)); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, "old-media")
	if err := os.Rename(media, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(media, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		original := filepath.Join(old, filepath.Base(path))
		body, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(original)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	out, err := deleteItem(t, s, item, "delete-replacement-root")
	if err != nil {
		t.Fatal(err)
	}
	if out.Removed != 0 {
		t.Fatalf("replacement root was accepted: %+v", out)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("replacement file removed: %v", err)
		}
	}
}

func TestDeleteAcceptsUnchangedAdmittedRoot(t *testing.T) {
	s, db, root, item, _ := deletionFixture(t)
	observed, err := storage.LocalInventoryDirectory(storage.InventoryRequest{Root: filepath.Join(root, "media"), RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE library_sources SET root_identity=? WHERE library_id='lib-1'`, storage.RootIdentity(observed)); err != nil {
		t.Fatal(err)
	}
	out, err := deleteItem(t, s, item, "delete-admitted-root")
	if err != nil || out.Removed != 1 {
		t.Fatalf("admitted root failed: %+v %v", out, err)
	}
}
