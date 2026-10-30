package playback

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/testauth"
)

func insertFixtureSession(t testing.TB, db *sql.DB, hash, account, profile, role, expiry string) {
	t.Helper()
	testauth.InsertSession(t, db, hash, account, profile, "local", role, expiry)
}

// catalogFixture creates a library, one entity and one linked asset through
// catalogtest and the catalogue write API. It returns the entity's integer
// id, its public id and the asset's token.
func catalogFixture(t testing.TB, db *sql.DB, library, root string, kind compactcatalog.Kind, key, title string, asset compactcatalog.Asset) (id int64, item, token string) {
	t.Helper()
	c := catalogtest.New(t, db)
	libraryKind := "movie"
	switch kind {
	case compactcatalog.Show, compactcatalog.Season, compactcatalog.Episode:
		libraryKind = "tv"
	case compactcatalog.Artist, compactcatalog.Album, compactcatalog.Track:
		libraryKind = "music"
	case compactcatalog.Book, compactcatalog.Part:
		libraryKind = "audiobook"
	}
	handle := ensureCatalogLibrary(c, library, library, libraryKind, root)
	entity := c.Entity(compactcatalog.Entity{Library: handle, Kind: kind, Key: key, Title: title}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var assetID int64
		var err error
		assetID, token, err = compactcatalog.UpsertAssetTx(ctx, tx, asset)
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, entity.ID, assetID, compactcatalog.Link{})
	})
	c.Drain()
	return entity.ID, entity.Public, token
}

func ensureCatalogLibrary(c *catalogtest.Catalog, id, name, kind, root string) int64 {
	c.T.Helper()
	var existing string
	err := c.DB.QueryRow(`SELECT id FROM libraries WHERE id=?`, id).Scan(&existing)
	if err == sql.ErrNoRows {
		return c.Library(id, name, kind, root)
	}
	if err != nil {
		c.T.Fatal(err)
	}
	return c.Handle(id)
}

func catalogLibrary(t testing.TB, db *sql.DB, id, name, kind, root string) int64 {
	t.Helper()
	return ensureCatalogLibrary(catalogtest.New(t, db), id, name, kind, root)
}

func linkExistingCatalogAsset(t testing.TB, db *sql.DB, library string, kind compactcatalog.Kind, key, title, token string) (id int64, item string) {
	t.Helper()
	c := catalogtest.New(t, db)
	entity := c.Entity(compactcatalog.Entity{Library: c.Handle(library), Kind: kind, Key: key, Title: title}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		assetID, err := compactcatalog.AssetByTokenTx(ctx, tx, token)
		if err != nil {
			return fmt.Errorf("asset %s: %w", token, err)
		}
		if assetID == 0 {
			return fmt.Errorf("asset %s: not found", token)
		}
		return compactcatalog.LinkAssetTx(ctx, tx, entity.ID, assetID, compactcatalog.Link{})
	})
	c.Drain()
	return entity.ID, entity.Public
}

func deleteCatalogEntity(t testing.TB, db *sql.DB, item string) {
	t.Helper()
	c := catalogtest.New(t, db)
	c.Delete(c.ID(item))
	c.Drain()
}

func addCatalogAsset(t testing.TB, db *sql.DB, asset compactcatalog.Asset) string {
	t.Helper()
	c := catalogtest.New(t, db)
	var token string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		_, token, err = compactcatalog.UpsertAssetTx(ctx, tx, asset)
		return err
	})
	c.Drain()
	return token
}

func updateCatalogAssetTx(t testing.TB, ctx context.Context, tx *sql.Tx, token string, update func(*compactcatalog.Asset)) {
	t.Helper()
	var asset compactcatalog.Asset
	if err := tx.QueryRowContext(ctx, `SELECT path,size,modified_ns,container,video_codec,audio_codec,width,height,duration FROM catalog_assets WHERE token=?`, token).Scan(
		&asset.Path, &asset.Size, &asset.ModifiedNS, &asset.Container, &asset.VideoCodec, &asset.AudioCodec, &asset.Width, &asset.Height, &asset.Duration,
	); err != nil {
		t.Fatal(err)
	}
	update(&asset)
	_, updatedToken, err := compactcatalog.UpsertAssetTx(ctx, tx, asset)
	if err != nil {
		t.Fatal(err)
	}
	if updatedToken != token {
		t.Fatalf("asset token changed from %q to %q", token, updatedToken)
	}
}

func updateCatalogAsset(t testing.TB, db *sql.DB, token string, update func(*compactcatalog.Asset)) {
	t.Helper()
	c := catalogtest.New(t, db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		updateCatalogAssetTx(t, ctx, tx, token, update)
		return nil
	})
	c.Drain()
}

func firstCatalogAssetToken(t testing.TB, db *sql.DB) string {
	t.Helper()
	var token string
	if err := db.QueryRow(`SELECT token FROM catalog_assets ORDER BY id LIMIT 1`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

// linkCatalogAsset records one more file of an entity and links it,
// returning the asset's token.
func linkCatalogAsset(t testing.TB, db *sql.DB, entity int64, asset compactcatalog.Asset) string {
	t.Helper()
	c := catalogtest.New(t, db)
	var token string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		assetID, assetToken, err := compactcatalog.UpsertAssetTx(ctx, tx, asset)
		if err != nil {
			return err
		}
		token = assetToken
		return compactcatalog.LinkAssetTx(ctx, tx, entity, assetID, compactcatalog.Link{})
	})
	c.Drain()
	return token
}

// Media-only fixtures still need the actual account stream-limit owner. This
// does not bypass admission or synthesize an authorization fallback.
func insertFixtureAccount(t testing.TB, db *sql.DB, account, profile string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,X'00',?,1)`, account, account, profile); err != nil {
		t.Fatal(err)
	}
}
