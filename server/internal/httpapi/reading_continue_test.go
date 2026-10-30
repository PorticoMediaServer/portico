package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestReadingContinueRegistered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	for _, name := range []string{"Second publication.pdf", "Not started.pdf"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(r.path), name), readingtest.PDF(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	readingScan(t, r.f, r.library)
	h := New(r.d)
	token := r.f.owner.AccessToken
	list := func(handler http.Handler, bearer, query string) ReadingContinueResponse {
		t.Helper()
		w := readingHTTP(t, handler, "GET", "/v1/reading/continue"+query, bearer, "", nil)
		var out ReadingContinueResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Items == nil {
			t.Fatal("continue", w.Code, w.Body.String())
		}
		return out
	}
	if out := list(h, token, ""); len(out.Items) != 0 {
		t.Fatal("unread publications fabricated history", out)
	}
	if w := readingHTTP(t, h, "GET", "/v1/reading/continue", "", "", nil); w.Code != 401 {
		t.Fatal("anonymous continue", w.Code, w.Body.String())
	}
	for _, query := range []string{"?limit=101", "?limit=1&limit=2", "?cursor=invalid", "?profileId=another"} {
		if w := readingHTTP(t, h, "GET", "/v1/reading/continue"+query, token, "", nil); w.Code != 400 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	publications, err := r.s.List(context.Background(), r.p, r.library, "", 100)
	if err != nil || len(publications.Items) != 3 {
		t.Fatal(publications, err)
	}
	prepare := func(item, source, bearer string) ReadingSessionResponse {
		t.Helper()
		w := readingHTTP(t, h, "POST", "/v1/reading/sessions", bearer, "", ReadingSessionRequest{item, source})
		var out ReadingSessionResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.SessionID == "" {
			t.Fatal(w.Code, w.Body.String())
		}
		return out
	}
	save := func(m ReadingSessionResponse, bearer string, expected int64, progression float64) {
		t.Helper()
		page := int64(0)
		w := readingHTTP(t, h, "PUT", "/v1/reading/sessions/"+m.SessionID+"/progress", bearer, m.Revision, ReadingProgressRequest{ExpectedRevision: expected, Location: ReadingLocation{Format: "pdf", PageIndex: &page, Progression: progression}})
		var saved ReadingProgressSavedResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &saved) != nil || !saved.Saved || saved.Progress == nil || saved.Progress.Revision != expected+1 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	first := prepare(r.item, r.source, token)
	var other ReadingItem
	for _, p := range publications.Items {
		if p.Title == "Second publication" {
			other = ReadingItem{ID: p.ID, SourceID: p.SourceID}
		}
	}
	if other.ID == "" {
		t.Fatal("actual second scanned publication is missing")
	}
	second := prepare(other.ID, other.SourceID, token)
	save(first, token, 0, .25)
	save(second, token, 0, .5)
	// No Storage client is available to this independent, real-authority
	// composition. Resume discovery must use one catalogue snapshot, not
	// Prepare/Open per item or a provider walk to manufacture a row.
	d := r.d
	d.Storage = nil
	d.Reading = NewReadingService(d)
	t.Cleanup(func() { _ = d.Reading.Close() })
	metadataOnly := New(d)
	page := list(metadataOnly, token, "?libraryId="+url.QueryEscape(r.library)+"&limit=1")
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("bounded first page", page)
	}
	next := list(metadataOnly, token, "?libraryId="+url.QueryEscape(r.library)+"&limit=1&cursor="+url.QueryEscape(page.NextCursor))
	if len(next.Items) != 1 || next.NextCursor != "" || next.Items[0].Item.ID == page.Items[0].Item.ID {
		t.Fatal("bounded second page", next)
	}
	seen := map[string]bool{}
	for _, item := range []ReadingContinueItem{page.Items[0], next.Items[0]} {
		if item.Item.ID != first.ItemID && item.Item.ID != second.ItemID || item.Progress.SourceID != item.Item.SourceID || item.Progress.Stale || item.Progress.Revision != 1 || item.Progress.ObservedAt <= 0 || item.Progress.Location.PageIndex == nil || *item.Progress.Location.PageIndex != 0 {
			t.Fatal("resume must project actual version/location/CAS", item)
		}
		seen[item.Item.ID] = true
	}
	if len(seen) != 2 {
		t.Fatal("pagination repeated a source", seen)
	}
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(context.Background(), owner.AccountToken, "Continue reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, p := range profiles.Profiles {
		if p.Name == "Continue reader" {
			child = p
		}
	}
	selected, err := r.f.id.SelectDirectProfile(context.Background(), owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	childToken := selected.Session.AccessToken
	if out := list(metadataOnly, childToken, ""); len(out.Items) != 0 {
		t.Fatal("sibling inherited another profile's resume", out)
	}
	childSession := prepare(r.item, r.source, childToken)
	save(childSession, childToken, 0, .75)
	out := list(metadataOnly, childToken, "")
	if len(out.Items) != 1 || out.Items[0].Progress.Location.Progression != .75 {
		t.Fatal("actual child resume", out)
	}
	if _, err = r.f.id.SetProfileRestrictions(context.Background(), owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
		t.Fatal(err)
	}
	if out = list(metadataOnly, childToken, "?limit=1"); len(out.Items) != 0 || out.NextCursor != "" {
		t.Fatal("hidden resume leaked before LIMIT", out)
	}
	save(first, token, 1, 1)
	out = list(metadataOnly, token, "")
	if len(out.Items) != 1 || out.Items[0].Item.ID != second.ItemID || out.Items[0].Progress.Location.Progression != .5 {
		t.Fatal("finished book or sibling state leaked into continue", out)
	}
	file := filepath.Join(filepath.Dir(r.path), "Second publication.pdf")
	changed := append(readingtest.PDF(), []byte("\n% actual source revision changed\n")...)
	if err = os.WriteFile(file, changed, 0600); err != nil {
		t.Fatal(err)
	}
	readingScan(t, r.f, r.library)
	out = list(metadataOnly, token, "")
	if len(out.Items) != 1 || out.Items[0].Item.SourceID != second.SourceID || !out.Items[0].Progress.Stale || out.Items[0].Progress.SourceRevision != second.Revision {
		t.Fatal("changed source lost its saved revision/stale fence", out)
	}
}

func TestReadingContinueFormatLocations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct{ kind, format string }{{"book", "epub"}, {"comic", "cbz"}} {
		t.Run(tc.format, func(t *testing.T) {
			r := newReadingFixture(t, tc.kind, tc.format)
			h := New(r.d)
			m := readingOpened(t, r, h)
			location := ReadingLocation{Format: ReadingFormat(tc.format), Progression: .4}
			if tc.format == "epub" {
				location.CFI, location.Section = "epubcfi(/6/4!/4/2:0)", m.Spine[0]
			} else {
				page := int64(0)
				location.PageIndex = &page
			}
			w := readingHTTP(t, h, "PUT", "/v1/reading/sessions/"+m.SessionID+"/progress", r.f.owner.AccessToken, m.Revision, ReadingProgressRequest{Location: location})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			w = readingHTTP(t, h, "GET", "/v1/reading/continue", r.f.owner.AccessToken, "", nil)
			var out ReadingContinueResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Items) != 1 {
				t.Fatal(w.Code, w.Body.String())
			}
			got := out.Items[0]
			want, _ := json.Marshal(location)
			actual, _ := json.Marshal(got.Progress.Location)
			// The item reports the real original file, while the session reports the
			// engine format its location is expressed in. This case opens a direct
			// source, so both name the same format and the row must carry it.
			if string(actual) != string(want) || got.Progress.SourceRevision != m.Revision || got.Progress.Stale || got.Item.Format != ReadingSourceFormat(m.Format) || got.Item.SourceID != m.SourceID {
				t.Fatal("format-specific saved location changed", got)
			}
		})
	}
}
