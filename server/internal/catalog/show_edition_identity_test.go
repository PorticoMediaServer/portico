package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Catalogue model/command test only. The scanner conformance lane owns the
// separate real Babylon folder/watch-state fixture, without a second copy here.
func TestShowSourceEditionScopesAutomaticIdentityAndKeepsOwnerMerge(t *testing.T) {
	ctx := context.Background()
	c := catalogtest.Open(t)
	library := c.Library("tv", "TV", "tv", "/tv")
	s := New(c.DB)
	provider := compactcatalog.ProviderIdentity{Provider: "tvdb", Kind: "series", ID: "70327"}
	resolve := func(edition, folder string) int64 {
		t.Helper()
		var id int64
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var created bool
			var err error
			id, created, err = compactcatalog.ResolveTitleTx(ctx, tx, compactcatalog.TitleClaim{Library: library, Kind: compactcatalog.Show, SourceEdition: edition, Provider: []compactcatalog.ProviderIdentity{provider}, Keys: []string{compactcatalog.ShowKey("folder:source:" + folder)}, Title: "Scoped Series", Year: 1994})
			if err != nil {
				return err
			}
			if created {
				return compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"local_key": "folder:source:" + folder})
			}
			return nil
		})
		return id
	}
	original := resolve("", "Original/Scoped Series")
	remaster := resolve("Bluray Remaster", "Remaster/Scoped Series {edition-Bluray Remaster}")
	if original == remaster {
		t.Fatal("different source editions coalesced by provider identity")
	}
	if got := resolve("", "Copy/Scoped Series"); got != original {
		t.Fatal("normal provider coalescing changed", got, original)
	}
	if got := resolve("Bluray Remaster", "Copy/Scoped Series {edition-Bluray Remaster}"); got != remaster {
		t.Fatal("same-edition provider coalescing failed", got, remaster)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_external_ids WHERE provider='tvdb' AND provider_kind='series' AND provider_id='70327' AND entity_id IN(?,?)`, original, remaster); n != 2 {
		t.Fatal("edition namespace modified actual provider ids", n)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		n, err := AdoptProviderIdentityTx(ctx, tx, original, provider)
		if n != 0 {
			t.Fatalf("automatic adoption merged another scope: %d", n)
		}
		return err
	})
	var from, into string
	if err := c.DB.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, remaster).Scan(&from); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, original).Scan(&into); err != nil {
		t.Fatal(err)
	}
	denied := errors.New("owner authorization refused")
	if err := s.MergeTitles(ctx, from, into, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("merge skipped actual authorization", err)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_shows WHERE entity_id IN(?,?)`, original, remaster); n != 2 {
		t.Fatal("refused owner merge changed scopes", n)
	}
	authorizations := 0
	if err := s.MergeTitles(ctx, from, into, func(*sql.Tx) error { authorizations++; return nil }); err != nil {
		t.Fatal(err)
	}
	if authorizations == 0 {
		t.Fatal("owner merge never rechecked its authority")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_shows WHERE entity_id IN(?,?)`, original, remaster); n != 1 {
		t.Fatal("explicit owner merge did not override editions", n)
	}
	// The source-folder aliases and existing redirects preserve the command's
	// result, without rewriting the surviving show's folder fact on rescan.
	if got := resolve("Bluray Remaster", "Remaster/Scoped Series {edition-Bluray Remaster}"); got != original {
		t.Fatal("rescan reversed owner merge", got, original)
	}
	if got := resolve("", "Original/Scoped Series"); got != original {
		t.Fatal("normal source moved after merge", got, original)
	}
	var edition string
	if err := c.DB.QueryRow(`SELECT source_edition FROM catalog_shows WHERE entity_id=?`, original).Scan(&edition); err != nil || edition != "" {
		t.Fatal("incoming alias rewrote survivor edition", edition, err)
	}
}
