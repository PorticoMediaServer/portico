package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// The exact-occurrence split must use the link named by the proof, never the
// first title that happens to share the asset.
func TestFileInfoSeparateUsesExactSelectedSharedHolder(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("movies", "Movies", "movie", "/movies")
	first := c.Movie(films, "/movies/first.mkv", "First", 2000)
	second := c.Movie(films, "/movies/second.mkv", "Second", 2001)

	// One asset is shared by both titles. Both also have their own file, so a
	// split is meaningful for either title. The old inventory split picked the
	// lowest entity id (First); this action must unlink Second.
	sharedAsset, sharedToken := c.File(first.ID, "/movies/shared.mkv", 5400)
	c.File(second.ID, "/movies/second-own.mkv", 5400)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, err := compactcatalog.AssetByTokenTx(ctx, tx, sharedToken)
		if err != nil {
			return err
		}
		if asset != sharedAsset {
			return errors.New("shared asset changed")
		}
		return compactcatalog.LinkAssetTx(ctx, tx, second.ID, asset, compactcatalog.Link{})
	})
	c.Drain()

	svc := New(c.DB)
	target := FileInfoActionTarget{
		Item:      second.ID,
		Asset:     sharedAsset,
		ItemID:    second.Public,
		AssetID:   sharedToken,
		LibraryID: "movies",
		Kind:      "movie",
	}
	public, err := svc.SeparateFileInfoVersion(context.Background(), target, func(context.Context, *sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if public == "" || public == second.Public {
		t.Fatalf("separate public %q", public)
	}
	var linked int
	if err = c.DB.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, second.ID, sharedAsset).Scan(&linked); err != nil || linked != 0 {
		t.Fatalf("selected holder still linked: %d %v", linked, err)
	}
	if err = c.DB.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, first.ID, sharedAsset).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("unselected holder lost its link: %d %v", linked, err)
	}
	var separate int64
	if err = c.DB.QueryRow(`SELECT e.id FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id AND l.asset_id=? WHERE e.public_id=pid_blob(?)`, sharedAsset, public).Scan(&separate); err != nil || separate <= 0 {
		t.Fatalf("new title link: %v", err)
	}
	if err = c.DB.QueryRow(`SELECT item_id FROM inventory_version_links WHERE library_id='movies' AND asset_id=?`, sharedToken).Scan(&separate); err != nil || separate != c.ID(public) {
		t.Fatalf("kept-apart marker: %d %v", separate, err)
	}
}

func TestFileInfoSeparateRefusesAChangedOrUnlinkedTarget(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(films, "/movies/film.mkv", "Film", 2000)
	other, otherToken := c.File(movie.ID, "/movies/other.mkv", 5400)
	otherAsset, err := compactcatalog.AssetByTokenTx(context.Background(), c.DB, otherToken)
	if err != nil || otherAsset != other {
		t.Fatalf("asset: %d %v", otherAsset, err)
	}
	c.Drain()
	svc := New(c.DB)
	// The public token is right for a file of this title, but the numeric asset
	// belongs to a different link: that is not a valid exact target.
	target := FileInfoActionTarget{Item: movie.ID, Asset: other + 1, ItemID: movie.Public, AssetID: otherToken, LibraryID: "movies", Kind: "movie"}
	if _, err = svc.SeparateFileInfoVersion(context.Background(), target, func(context.Context, *sql.Tx) error { return nil }); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("mismatched numeric asset: %v", err)
	}
}

func TestFileInfoSeparateKeepsSourcePlaybackBusy(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(films, "/movies/film.mkv", "Film", 2000)
	other, otherToken := c.File(movie.ID, "/movies/other.mkv", 5400)
	c.Drain()

	// A library is created with its default source; reuse that exact source
	// lifetime rather than adding a second root for the same library.
	var source, incarnation string
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='movies'`).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object',?,?,?,'other.mkv','revision','{}',1000,1)`, source, otherToken, incarnation); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('session','hash','account','profile',?,?,1,'playing','grant-hash','grant-token',?,'request',5400)`,
		movie.ID, otherToken, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	target := FileInfoActionTarget{Item: movie.ID, Asset: other, ItemID: movie.Public, AssetID: otherToken, LibraryID: "movies", Kind: "movie"}
	svc := New(c.DB)
	if _, err := svc.SeparateFileInfoVersion(context.Background(), target, func(context.Context, *sql.Tx) error { return nil }); !errors.Is(err, ErrSourceBusy) {
		t.Fatalf("active source playback: %v", err)
	}
	var links int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, movie.ID, other).Scan(&links); err != nil || links != 1 {
		t.Fatalf("busy split changed links: %d %v", links, err)
	}
}

type fileInfoAlbumFixture struct {
	c       *catalogtest.Catalog
	s       *Service
	r       *FileInfoRuntime
	album   catalogtest.Item
	song    catalogtest.Item
	opening string
}

func newFileInfoAlbumFixture(t *testing.T) *fileInfoAlbumFixture {
	t.Helper()
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	song := c.Song(album, 1, "/music/one.mp4", "Song")
	c.Drain()

	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &fileInfoAlbumFixture{c: c, s: New(c.DB), album: album, song: song}
	viewer := Viewer{Profile: "p", Fence: "authority", Libraries: []string{"music"}}
	guard := func(ctx context.Context, caller readprep.Caller, id FileInfoIdentity) (FileInfoAdmission, error) {
		if caller != "family" {
			return FileInfoAdmission{}, readprep.ErrStale
		}
		ad, err := f.s.CaptureFileInfoAdmission(ctx, viewer, "server", album.Public, true, "actor", false)
		if err == nil && ad.Identity != id {
			return ad, readprep.ErrStale
		}
		return ad, err
	}
	f.r, err = NewFileInfoRuntime(c.DB, store, guard)
	if err != nil {
		t.Fatal(err)
	}
	ad, err := f.s.CaptureFileInfoAdmission(context.Background(), viewer, "server", album.Public, true, "actor", false)
	if err != nil {
		t.Fatal(err)
	}
	err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		var err error
		f.opening, err = f.r.AcceptDetailOpening(ctx, "family", ad.Identity, ContentRevision{Catalog: 41, Viewer: 7})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fileInfoAlbumFixture) songOccurrence(t *testing.T) string {
	t.Helper()
	out, err := f.r.Read(context.Background(), "family", FileInfoReadRequest{Kind: "files", Opening: f.opening})
	out = fileInfoAwait(t, f.r, out, err)
	defer out.Release()
	if out.Files == nil {
		t.Fatal("no files page")
	}
	for _, file := range out.Files.Files {
		if file.ID == f.song.Token {
			return file.Occurrence
		}
	}
	t.Fatalf("song %s not in files page", f.song.Token)
	return ""
}

func TestFileInfoResolveActionAllowsAlbumChildButRejectsForeignCaller(t *testing.T) {
	f := newFileInfoAlbumFixture(t)
	occurrence := f.songOccurrence(t)
	target, err := f.r.ResolveAction(context.Background(), "family", FileInfoActionProof{Opening: f.opening, Occurrence: occurrence})
	if err != nil {
		t.Fatal(err)
	}
	if target.ItemID != f.song.Public || target.AssetID != f.song.Token || target.LibraryID != "music" {
		t.Fatalf("target %+v", target)
	}
	if target.ItemID == f.album.Public {
		t.Fatal("child file was reported as the album")
	}
	if _, err = f.r.ResolveAction(context.Background(), "foreign", FileInfoActionProof{Opening: f.opening, Occurrence: occurrence}); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("foreign caller: %v", err)
	}
}

func TestFileInfoResolveActionRejectsReparentedChild(t *testing.T) {
	f := newFileInfoAlbumFixture(t)
	occurrence := f.songOccurrence(t)
	// Issue an occurrence for the song under Album, then move the song to a
	// different album. The old opening's parent-child proof must not still
	// authorize the child.
	other := f.c.Entity(compactcatalog.Entity{Library: f.c.Handle("music"), Kind: compactcatalog.Album, Key: "other-album", Title: "Other", Year: 2024}, nil)
	if _, err := f.c.DB.Exec(`UPDATE catalog_entities SET parent_id=? WHERE id=?`, other.ID, f.song.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.ResolveAction(context.Background(), "family", FileInfoActionProof{Opening: f.opening, Occurrence: occurrence}); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("reparented child: %v", err)
	}
}
