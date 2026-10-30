package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

func readingSeriesOwnerField(t *testing.T, r *readingFixture, item, field, value string) {
	t.Helper()
	if _, err := r.f.db.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) SELECT 'item',id,?,?,'',1,?,datetime('now') FROM catalog_entities WHERE public_id=pid_blob(?) ON CONFLICT(kind,entity_id,field) DO UPDATE SET value=excluded.value,locked=1`, field, value, identity.PersonalKey(r.p.Viewer), item); err != nil {
		t.Fatal(err)
	}
}
func readingSeriesDrain(t *testing.T, r *readingFixture) {
	t.Helper()
	if err := compactcatalog.Drain(context.Background(), r.f.db); err != nil {
		t.Fatal(err)
	}
}

// readingPublishFileFacts runs the real reading producer that adopts a
// publication's own author/series facts. Without it those facts are genuinely
// absent until someone opens the book; a Saved read opens no source itself.
func readingPublishFileFacts(t *testing.T, r *readingFixture) {
	t.Helper()
	session, _, err := r.s.Prepare(context.Background(), r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.s.CloseSession(r.p, session.ID); err != nil {
		t.Fatal(err)
	}
	readingSeriesDrain(t, r)
}

func TestReadingSeriesNumberedOrderingSignedPagingAndInputDrift(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	paths := []string{readingTreeFile(t, r, "Author/Series/Alpha.epub"), readingTreeFile(t, r, "Author/Series/Beta.epub"), readingTreeFile(t, r, "Author/Series/Gamma.epub")}
	readingScan(t, r.f, r.library)
	items := []string{readingPathItem(t, r, paths[0]), readingPathItem(t, r, paths[1]), readingPathItem(t, r, paths[2])}
	for _, item := range items {
		readingSeriesOwnerField(t, r, item, "series", "Saga")
	}
	readingSeriesOwnerField(t, r, items[0], "seriesNumber", "2")
	readingSeriesOwnerField(t, r, items[1], "seriesNumber", "1.5")
	if _, err := r.f.db.Exec(`UPDATE catalog_entities SET sort_key=? WHERE public_id=pid_blob(?)`, strings.Repeat("日本", 10000), items[1]); err != nil {
		t.Fatal(err)
	}
	// Source availability cannot silently remove a member from the series.
	if _, err := r.f.db.Exec(`UPDATE library_sources SET enabled=0 WHERE library_id=?`, r.library); err != nil {
		t.Fatal(err)
	}
	readingSeriesDrain(t, r)
	series := compactcatalog.ReadingSeriesID(r.library, "saga")
	first, err := r.s.Series(ctx, r.p, r.library, series, "", 1)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != items[1] || first.Items[0].InventoryAvailable || len(first.NextCursor) != 215 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := r.s.Series(ctx, r.p, r.library, series, first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != items[0] || len(second.NextCursor) != 215 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	third, err := r.s.Series(ctx, r.p, r.library, series, second.NextCursor, 1)
	if err != nil || len(third.Items) != 1 || third.Items[0].ID != items[2] || third.NextCursor != "" {
		t.Fatalf("third=%+v err=%v", third, err)
	}
	if _, err = r.s.Series(ctx, r.p, r.library, series, first.NextCursor, 2); err != reading.ErrChanged {
		t.Fatal("cursor crossed its captured limit", err)
	}
	readingSeriesOwnerField(t, r, items[0], "series", "Renamed")
	if _, err = r.s.Series(ctx, r.p, r.library, series, "", 1); err != reading.ErrChanged {
		t.Fatal("partial projection served", err)
	}
	readingSeriesDrain(t, r)
	if _, err = r.s.Series(ctx, r.p, r.library, series, first.NextCursor, 1); err != reading.ErrChanged {
		t.Fatal("cursor survived input drift", err)
	}
	// A complete rename changes the derived series ID; no alias is invented.
	for _, item := range items {
		readingSeriesOwnerField(t, r, item, "series", "Renamed")
	}
	readingSeriesDrain(t, r)
	if _, err = r.s.Series(ctx, r.p, r.library, series, "", 1); err != reading.ErrUnavailable {
		t.Fatal("old series ID still resolves", err)
	}
}

func readingSeriesRestrictionCheck(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	readingSeriesOwnerField(t, r, r.item, "series", "Private series")
	readingSeriesDrain(t, r)
	h := New(r.d)
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Series reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, profile := range profiles.Profiles {
		if !profile.Primary && profile.Name == "Series reader" {
			child = profile
		}
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	token := selected.Session.AccessToken
	path := "/v1/libraries/" + r.library + "/reading/series/" + compactcatalog.ReadingSeriesID(r.library, "Private series")
	if w := readingHTTP(t, h, "GET", path, token, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), r.item) {
		t.Fatal("visible series", w.Code, w.Body.String())
	}
	if w := readingHTTP(t, h, "GET", path, "", "", nil); w.Code != 401 {
		t.Fatal("anonymous series", w.Code)
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=1&limit=2", "?unknown=1"} {
		if w := readingHTTP(t, h, "GET", path+query, token, "", nil); w.Code != 400 {
			t.Fatal("invalid series query", query, w.Code)
		}
	}
	if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, "/v1/libraries/" + r.library + "/reading/series/" + strings.Repeat("a", 43)} {
		w := readingHTTP(t, h, "GET", target, token, "", nil)
		if w.Code != 404 || bodyErrorCode(w.Body.String()) != "not_found" {
			t.Fatal("hidden/absent series", w.Code, w.Body.String())
		}
		for _, hidden := range []string{r.item, r.source, r.library, "Private series"} {
			if strings.Contains(w.Body.String(), hidden) {
				t.Fatal("hidden series identity escaped")
			}
		}
	}
	if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 2, AllowUnrated: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.f.id.EditDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
		t.Fatal(err)
	}
	if w := readingHTTP(t, h, "GET", path, token, "", nil); w.Code != 404 {
		t.Fatal("revoked library series", w.Code, w.Body.String())
	}
}

func TestReadingSeriesHTTPUsesTheExistingItemsProjection(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	readingSeriesOwnerField(t, r, r.item, "series", "Saga")
	readingSeriesDrain(t, r)
	h := New(r.d)
	path := "/v1/libraries/" + r.library + "/reading/series/" + compactcatalog.ReadingSeriesID(r.library, "Saga") + "?limit=1"
	w := readingHTTP(t, h, http.MethodGet, path, r.f.owner.AccessToken, "", nil)
	var out ReadingItemsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Items) != 1 || out.Items[0].ID != r.item || out.Items[0].Series == nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = readingHTTP(t, h, http.MethodGet, path+"&cursor="+url.QueryEscape(strings.Repeat("a", 215)), r.f.owner.AccessToken, "", nil); w.Code != http.StatusPreconditionFailed || bodyErrorCode(w.Body.String()) != "revision_mismatch" {
		t.Fatal("invalid signed cursor", w.Code, w.Body.String())
	}
}
