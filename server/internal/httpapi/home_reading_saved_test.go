package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestHomeReadingSavedDeferredHTTP(t *testing.T) {
	for _, kind := range []string{"book", "comic"} {
		t.Run(kind, func(t *testing.T) {
			c := catalogtest.Open(t)
			root := t.TempDir()
			ident, err := identity.New(c.DB, root)
			if err != nil {
				t.Fatal(err)
			}
			c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`)
			library := c.Library("reading", "Reading", kind, root)
			leaf := compactcatalog.Ebook
			pivot := "books"
			if kind == "comic" {
				leaf = compactcatalog.Comic
				pivot = "comics"
			}
			for i := 0; i < 4; i++ {
				path := filepath.Join(root, fmt.Sprintf("%d.pdf", i))
				if err = os.WriteFile(path, readingtest.PDF(), 0600); err != nil {
					t.Fatal(err)
				}
				info, e := os.Stat(path)
				if e != nil {
					t.Fatal(e)
				}
				item := c.Entity(compactcatalog.Entity{Library: library, Kind: leaf, Key: path, Title: fmt.Sprintf("Title %d", i)}, nil)
				c.Write(func(ctx context.Context, tx *sql.Tx) error {
					asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "pdf"})
					if e != nil {
						return e
					}
					if e = compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{}); e != nil {
						return e
					}
					_, e = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, item.ID, asset)
					return e
				})
			}
			c.Drain()
			owner, err := ident.Issue("owner", "profile", "local", "owner", 1)
			if err != nil {
				t.Fatal(err)
			}
			h := New(Dependencies{DB: c.DB, Identity: ident, Catalog: catalog.New(c.DB), Console: operations.New(c.DB)})
			call := func(method, path string, body any, out any) {
				t.Helper()
				raw := ""
				if body != nil {
					b, e := json.Marshal(body)
					if e != nil {
						t.Fatal(e)
					}
					raw = string(b)
				}
				w := homeAPIRequest(t, h, owner.AccessToken, method, path, raw)
				if w.Code != 200 {
					t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
				}
				if out != nil {
					if e := json.Unmarshal(w.Body.Bytes(), out); e != nil {
						t.Fatal(e)
					}
				}
			}
			var made struct{ Receipt catalog.SavedResourceReceipt }
			call("POST", "/v1/saved-resources", map[string]any{"operationId": "reading-home-create", "expectedRevision": 0, "kind": "view", "name": "Reading view", "definition": catalog.SavedViewDefinition{LibraryID: "reading", Pivot: pivot, Sort: []catalog.BrowseSortSelection{{Field: "title", Direction: "asc"}}, Presentation: "grid"}}, &made)
			rowID := "view:" + made.Receipt.ResourceID
			call("PUT", "/v1/home/layout", map[string]any{"expectedRevision": 1, "idempotencyKey": "reading-home-layout", "rowOrder": []string{rowID}, "hiddenRowIds": []string{}}, nil)
			var manifest catalog.HomeDocument
			call("GET", "/v1/home", nil, &manifest)
			endpoint := ""
			for _, row := range manifest.Rows {
				if row.ID == rowID {
					endpoint = row.Endpoint
				}
			}
			if endpoint == "" {
				t.Fatal("missing Reading row")
			}
			var page catalog.HomeRow
			call("GET", endpoint+"?limit=2", nil, &page)
			if len(page.Entries) != 2 || page.NextCursor == "" {
				t.Fatalf("page %+v", page)
			}
			// The final batch can retain the real response for the JS consumer.
			// Ordinary Go CI still executes every producer assertion without Node.
			if directory := os.Getenv("PORTICO_READING_HOME_FIXTURE_DIR"); directory != "" {
				response := homeAPIRequest(t, h, owner.AccessToken, "GET", endpoint+"?limit=2", "")
				if response.Code != 200 {
					t.Fatalf("fixture response %d %s", response.Code, response.Body.String())
				}
				if err := os.WriteFile(filepath.Join(directory, kind+".json"), response.Body.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, entry := range page.Entries {
				expected := "ebook"
				if kind == "comic" {
					expected = "comic"
				}
				if entry.Kind != expected || entry.Playback != nil || entry.Duration != nil {
					t.Fatalf("invalid Reading entry %+v", entry)
				}
			}
		})
	}
}
