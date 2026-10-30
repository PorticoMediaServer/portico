package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestLibraryMissingManagementFiltersBeforeCountPageAndRemoval(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	allowed := c.Library("allowed", "Allowed", "movie", "/allowed")
	denied := c.Library("denied", "Denied", "movie", "/denied")
	hidden := c.Movie(allowed, "/allowed/hidden.mkv", "Secret title", 2000)
	outside := c.Movie(denied, "/denied/outside.mkv", "Outside library", 2000)
	visible := c.Movie(allowed, "/allowed/visible.mkv", "Visible title", 2000)
	c.Attributes(hidden.ID, "label", "secret")
	c.Drain()
	c.Exec(`UPDATE direct_profiles SET allowed_libraries='["allowed"]',revision=revision+1 WHERE id='admin-profile'`)
	c.Exec(`INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('admin-profile','["secret"]')`)
	for _, item := range []catalogtest.Item{hidden, outside, visible} {
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT ?,s.id,?,s.incarnation,?,'r','{}',1,1 FROM library_sources s JOIN catalog_libraries l ON l.library_id=s.library_id WHERE l.id=(SELECT library_id FROM catalog_entities WHERE id=?)`, "missing:"+item.Token, item.Token, item.Token+".mkv", item.ID)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var source string
			if err := tx.QueryRowContext(ctx, `SELECT source_id FROM inventory_objects WHERE asset_id=?`, item.Token).Scan(&source); err != nil {
				return err
			}
			return catalog.MarkInventoryMissingTx(ctx, tx, source, "missing-job", "missing:"+item.Token, time.Now().Add(-time.Hour))
		})
	}
	// Missing evidence is published by the real source41→44 worker.
	c.Drain()
	read := func(token string, expected int) catalog.MissingTitlesPage {
		t.Helper()
		w := f.call("GET", "/v1/admin/missing-titles?limit=1", token, "")
		var out catalog.MissingTitlesPage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Total != int64(expected) {
			t.Fatal(w.Code, w.Body.String())
		}
		return out
	}
	page := read(f.admin, 1)
	if len(page.Items) != 1 || page.Items[0].ID != visible.Public || page.NextCursor != "" {
		t.Fatal("hidden rows affected page", page)
	}
	read(f.owner, 3)
	if w := f.call("GET", "/v1/admin/missing-titles?libraryId=denied", f.admin, ""); w.Code != 404 {
		t.Fatal("ungranted library was addressable", w.Code, w.Body.String())
	}
	if w := f.call("POST", "/v1/admin/missing-titles/empty", f.admin, `{"libraryId":"denied"}`); w.Code != 404 {
		t.Fatal("ungranted removal was admitted", w.Code, w.Body.String())
	}
	w := f.call("POST", "/v1/admin/missing-titles/empty", f.admin, `{}`)
	var removed catalog.MissingEmptied
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &removed) != nil || removed.Titles != 1 || removed.Files != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	read(f.admin, 0)
	ownerPage := read(f.owner, 2)
	if strings.Contains(w.Body.String(), "Secret") || len(ownerPage.Items) != 1 {
		t.Fatal("hidden removal or disclosure", ownerPage)
	}
	for _, item := range []catalogtest.Item{hidden, outside} {
		var exists bool
		if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE id=?) AND EXISTS(SELECT 1 FROM inventory_objects WHERE asset_id=?)`, item.ID, item.Token).Scan(&exists); err != nil || !exists {
			t.Fatal("hidden catalogue facts removed", item.ID, err)
		}
	}
}

func TestLibraryMissingManagementRechecksDemotionScopeAndMainProfile(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	c.Library("library", "Films", "movie", "/library")
	p, err := f.deps.Identity.AuthenticateContext(context.Background(), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	authority := f.deps.missingLibraryAuthority(p, "library")
	c.Exec(`UPDATE direct_profiles SET allowed_libraries='[]',revision=revision+1 WHERE id='admin-profile'`)
	if _, err = f.deps.Catalog.MissingTitlesAuthorized(context.Background(), "library", "", 1, authority); !errors.Is(err, identity.ErrNotVisible) {
		t.Fatal("captured scope survived narrowing", err)
	}
	c.Exec(`UPDATE direct_profiles SET allowed_libraries=NULL,revision=revision+1 WHERE id='admin-profile'`)
	c.Exec(`UPDATE direct_memberships SET role='member',revision=revision+1 WHERE account_id='admin'`)
	if _, err = f.deps.Catalog.MissingTitlesAuthorized(context.Background(), "library", "", 1, authority); err == nil {
		t.Fatal("captured read authority survived demotion")
	}
	if _, err = f.deps.Catalog.EmptyMissingAuthorized(context.Background(), "library", time.Now(), authority); err == nil {
		t.Fatal("captured write authority survived demotion")
	}
	c.Exec(`UPDATE direct_memberships SET role='admin',revision=revision+1 WHERE account_id='admin'`)
	c.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position) VALUES('secondary','admin','Secondary',0,1)`)
	secondary, err := f.deps.Identity.Issue("admin", "secondary", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct{ method, path, body string }{{"GET", "/v1/admin/missing-titles", ""}, {"POST", "/v1/admin/missing-titles/empty", `{}`}} {
		w := f.call(probe.method, probe.path, secondary.AccessToken, probe.body)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("secondary profile borrowed management", w.Code, w.Body.String())
		}
	}
	// The same captured principal is also rejected if its former main profile
	// is retired before the actual read/write transaction starts.
	c.Exec(`UPDATE direct_profiles SET deleted=1,revision=revision+1 WHERE id='admin-profile'`)
	if _, err = f.deps.Catalog.EmptyMissingAuthorized(context.Background(), "library", time.Now(), authority); err == nil {
		t.Fatal("retired main profile retained management")
	}
}

func TestLibraryMissingManagementKeepsAssetsLinkedToHiddenTitles(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	library := c.Library("library", "Films", "movie", "/library")
	visible := c.Movie(library, "/library/visible.mkv", "Visible", 2000)
	hidden := c.Movie(library, "/library/hidden.mkv", "Secret", 2000)
	c.Attributes(hidden.ID, "label", "secret")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, hidden.ID, visible.Asset, compactcatalog.Link{})
	})
	c.Drain()
	c.Exec(`INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('admin-profile','["secret"]')`)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'shared',id,?,incarnation,'visible.mkv','r','{}',1,1 FROM library_sources WHERE library_id='library'`, visible.Token)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return catalog.MarkInventoryMissingTx(ctx, tx, "library", "missing-job", "shared", time.Now().Add(-time.Hour))
	})
	c.Drain()
	w := f.call("POST", "/v1/admin/missing-titles/empty", f.admin, `{}`)
	var removed catalog.MissingEmptied
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &removed) != nil || removed.Titles != 1 || removed.Files != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	var preserved bool
	if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE id='shared') AND EXISTS(SELECT 1 FROM catalog_asset_links WHERE entity_id=? AND asset_id=?)`, hidden.ID, visible.Asset).Scan(&preserved); err != nil || !preserved {
		t.Fatal("hidden title's source facts removed", err)
	}
}
