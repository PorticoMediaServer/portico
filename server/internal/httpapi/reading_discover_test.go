package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// Discover publishes the recently-added row from the catalogue's own added_ms
// under List's snapshot and predicate. Series rows are not fabricated from a
// capped scan: the approved model is the durable ready projection.
func TestReadingDiscoverRecentlyAddedFromCatalogueFacts(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	onePath := readingTreeFile(t, r, "Author/Series/One.epub")
	twoPath := readingTreeFile(t, r, "Author/Series/Two.epub")
	standalonePath := readingTreeFile(t, r, "Author/Standalone.epub")
	readingScan(t, r.f, r.library)
	one, two := readingPathItem(t, r, onePath), readingPathItem(t, r, twoPath)
	standalone := readingPathItem(t, r, standalonePath)
	out, err := r.s.Discover(ctx, r.p, r.library, reading.DiscoverLimits{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range out.Recent {
		seen[row.ID] = true
	}
	for _, id := range []string{one, two, standalone, r.item} {
		if !seen[id] {
			t.Fatalf("recently added lost %s: %+v", id, out.Recent)
		}
	}
	// Order and membership are the catalogue's added_ms, never a made-up date.
	previous, first := int64(0), true
	for _, row := range out.Recent {
		var added int64
		if err := r.f.db.QueryRow(`SELECT added_ms FROM catalog_entities WHERE public_id=pid_blob(?)`, row.ID).Scan(&added); err != nil {
			t.Fatal(err)
		}
		if !first && added > previous {
			t.Fatalf("recent order ignores added_ms at %s", row.ID)
		}
		previous, first = added, false
		if row.ID == one || row.ID == two {
			if row.Author == nil || row.Author.Value != "Author" || row.Series == nil || row.Series.Name != "Series" {
				t.Fatalf("attribution=%+v", row)
			}
		}
	}
	// A bounded request returns at most that many rows and never pads.
	small, err := r.s.Discover(ctx, r.p, r.library, reading.DiscoverLimits{Recent: 2})
	if err != nil || len(small.Recent) != 2 {
		t.Fatalf("capped discover=%+v %v", small, err)
	}
	// An all-library request is allowed and stays inside the same predicate.
	if all, err := r.s.Discover(ctx, r.p, "", reading.DiscoverLimits{}); err != nil || len(all.Recent) == 0 {
		t.Fatalf("all libraries=%+v %v", all, err)
	}
	// An owner edit changes the row's own attribution, never its order.
	if _, err = r.f.db.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at)
 SELECT 'item',id,'series','Dune','',1,?,datetime('now') FROM catalog_entities WHERE public_id=pid_blob(?)`, identity.PersonalKey(r.p.Viewer), one); err != nil {
		t.Fatal(err)
	}
	out, err = r.s.Discover(ctx, r.p, r.library, reading.DiscoverLimits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range out.Recent {
		if row.ID == one && (row.Series == nil || row.Series.Name != "Dune") {
			t.Fatalf("owner attribution=%+v", row)
		}
	}
}

// The producer shares List's profile/library/restriction predicate.
func TestReadingDiscoverHonorsCurrentProfileRestrictions(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	readingTreeFile(t, r, "Author/Series/One.epub")
	readingScan(t, r.f, r.library)
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Discover reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, p := range profiles.Profiles {
		if !p.Primary && p.Name == "Discover reader" {
			child = p
		}
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.f.id.Authenticate(selected.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
		t.Fatal(err)
	}
	p, err = r.f.id.Authenticate(selected.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := r.s.Discover(ctx, p, r.library, reading.DiscoverLimits{})
	if err != nil || len(restricted.Recent) != 0 {
		t.Fatalf("restricted discover=%+v %v", restricted, err)
	}
	if owner, err := r.s.Discover(ctx, r.p, r.library, reading.DiscoverLimits{}); err != nil || len(owner.Recent) == 0 {
		t.Fatalf("owner discover=%+v %v", owner, err)
	}
}

// The additive operation is a strict bounded page over the same projection: an
// invalid or out-of-range limit is rejected, the default is 20, and the payload
// carries rows of the existing public item shape only.
func TestReadingDiscoverOperationStrictAndBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	onePath := readingTreeFile(t, r, "Author/Series/One.epub")
	readingTreeFile(t, r, "Author/Series/Two.epub")
	readingScan(t, r.f, r.library)
	one := readingPathItem(t, r, onePath)
	h := New(r.d)
	token := r.f.owner.AccessToken
	get := func(query string) *ReadingDiscoverResponse {
		t.Helper()
		w := readingHTTP(t, h, "GET", "/v1/reading/discover"+query, token, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %d %s", query, w.Code, w.Body.String())
		}
		var out ReadingDiscoverResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Rows) != 1 || out.Rows[0].Key != "recent" || out.Rows[0].Items == nil {
			t.Fatalf("%s rows=%+v %v", query, out, err)
		}
		return &out
	}
	def := get("")
	if len(def.Rows[0].Items) == 0 || len(def.Rows[0].Items) > 20 {
		t.Fatalf("default page=%+v", def.Rows[0].Items)
	}
	limited := get("?limit=1&libraryId=" + r.library)
	if len(limited.Rows[0].Items) != 1 {
		t.Fatalf("bounded page=%+v", limited.Rows[0].Items)
	}
	found := false
	for _, item := range def.Rows[0].Items {
		if item.ID == one {
			found = item.Author != nil && item.Author.Value == "Author" && item.Series != nil && item.Series.Name == "Series"
		}
	}
	if !found {
		t.Fatalf("recent row does not reuse the item projection: %+v", def.Rows[0].Items)
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=x", "?cursor=1", "?limit=1&limit=2", "?limit=1&unknown=2"} {
		if w := readingHTTP(t, h, "GET", "/v1/reading/discover"+query, token, "", nil); w.Code != http.StatusBadRequest {
			t.Fatalf("%s %d %s", query, w.Code, w.Body.String())
		}
	}
	if w := readingHTTP(t, h, "GET", "/v1/reading/discover", "", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous %d", w.Code)
	}
}
