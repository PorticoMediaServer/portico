package metadata

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type publicationTransport func(*http.Request) (*http.Response, error)

func (f publicationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func publicationFixture(t *testing.T) (*Service, *sql.DB, *time.Time) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// The independent assembled gate must include root's real installer wiring.
	var installed int
	if err = db.QueryRow(`SELECT count(*) FROM metadata_publication_heads`).Scan(&installed); err != nil {
		t.Fatalf("publication migration is not wired: %v", err)
	}
	c := catalogtest.New(t, db)
	library := c.Library("lib", "Movies", "movie", "/owned")
	item := c.Movie(library, "/owned/source.mp4", "Film", 2008)
	c.Fields(item.ID, map[string]any{"overview": "Before", "backdrop_url": "local:backdrop"})
	c.Drain()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	service := New(db, "in-process-fixture")
	service.now = func() time.Time { return now }
	service.client = &http.Client{Transport: publicationTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("unexpected provider call") })}
	return service, db, &now
}
func publicationExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
}
func publicationScalar(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var v string
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
func publicationManual(t *testing.T, db *sql.DB, title *string) {
	t.Helper()
	settleMetadataCatalogue(t, db)
	cat := catalog.New(db)
	allow := func(*sql.Tx) error { return nil }
	var item string
	if err := db.QueryRow(`SELECT pid(e.public_id) FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.kind=? AND l.library_id='lib' ORDER BY e.id LIMIT 1`, int(compactcatalog.Movie)).Scan(&item); err != nil {
		t.Fatal(err)
	}
	v, err := cat.ManualMetadata(context.Background(), "server", "viewer", item, allow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cat.SaveManualMetadata(context.Background(), "server", "viewer", item, "owner", catalog.ManualMetadataMutation{ExpectedRevision: v.Revision, Title: catalog.MetadataEditValue{Present: true, Value: title}}, allow); err != nil {
		t.Fatal(err)
	}
}

func publicationFixtureMovie(t *testing.T, db *sql.DB) catalogtest.Item {
	t.Helper()
	var item catalogtest.Item
	if err := db.QueryRow(`SELECT id,pid(public_id) FROM catalog_entities WHERE kind=? AND title=? ORDER BY id LIMIT 1`, int(compactcatalog.Movie), "Film").Scan(&item.ID, &item.Public); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT a.id,a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? ORDER BY a.id LIMIT 1`, item.ID).Scan(&item.Asset, &item.Token); err != nil {
		t.Fatal(err)
	}
	return item
}

func publicationAssetStat(t *testing.T, db *sql.DB, item catalogtest.Item) (int64, int64) {
	t.Helper()
	var size, modified int64
	if err := db.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, item.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	return size, modified
}
