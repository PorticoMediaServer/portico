package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Reading sessions are personal lifecycles, not bearer grants. Unlike the
// generic path probes, these requests must carry the real captured revision,
// range/resource and actor to reach the current library/title checks. Each
// matrix entry runs this check; none is exempted as an opaque grant.
func readingRestrictionCheck(pattern string) func(*testing.T) {
	return func(t *testing.T) {
		kind, format := "book", "pdf"
		if strings.HasSuffix(pattern, "/assets") {
			format = "epub"
		} else if strings.Contains(pattern, "/pages/") {
			kind, format = "comic", "cbz"
		}
		r := newReadingFixture(t, kind, format)
		h := New(r.d)
		ctx := context.Background()
		owner := readingOwnerLogin(t, r)
		profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Matrix reader", "blue")
		if err != nil {
			t.Fatal(err)
		}
		var child identity.DirectProfile
		for _, profile := range profiles.Profiles {
			if !profile.Primary && profile.Name == "Matrix reader" {
				child = profile
			}
		}
		selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
		if err != nil || selected.Session.AccessToken == "" {
			t.Fatal("select actual non-primary reader", err)
		}
		token := selected.Session.AccessToken
		open := func() ReadingSessionResponse {
			w := readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{r.item, r.source})
			var manifest ReadingSessionResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &manifest) != nil || manifest.SessionID == "" || manifest.ItemID != r.item || manifest.SourceID != r.source {
				t.Fatalf("real non-primary reader open: %d %s", w.Code, w.Body.String())
			}
			return manifest
		}
		manifest := open()
		const annotationID, annotationNote = "matrix-annotation", "Private matrix annotation note"
		annotationBody := func(revision int64) map[string]any {
			return map[string]any{"expectedRevision": revision, "operation": "upsert", "id": annotationID,
				"kind": "bookmark", "location": map[string]any{"format": "pdf", "pageIndex": 0, "progression": .25}, "note": annotationNote}
		}
		if pattern == "GET /v1/reading/sessions/{sessionId}/annotations" {
			w := readingHTTP(t, h, "PUT", "/v1/reading/sessions/"+manifest.SessionID+"/annotations", token, manifest.Revision, annotationBody(0))
			if w.Code != 200 {
				t.Fatal("seed actual private annotation", w.Code, w.Body.String())
			}
		}
		// The prepare route is also exercised as an ordinary member, without
		// an Owner/main-profile or Hosted grant. Its lifecycle is not shared
		// with the restricted profile used for the rest of this probe.
		if pattern == "POST /v1/reading/sessions" {
			if _, err = r.f.id.CreateDirectMember(ctx, owner.AccountToken, identity.DirectMemberCreate{
				Username: "matrix-reader", Password: "different-test-password", Name: "Reader", AllowedLibraries: []string{r.library},
			}); err != nil {
				t.Fatal(err)
			}
			member, loginErr := r.f.id.DirectLogin(ctx, "matrix-reader", "different-test-password")
			if loginErr != nil || member.Session == nil {
				t.Fatal("actual member login", loginErr)
			}
			w := readingHTTP(t, h, "POST", "/v1/reading/sessions", member.Session.AccessToken, "", ReadingSessionRequest{r.item, r.source})
			var own ReadingSessionResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &own) != nil || own.SessionID == "" {
				t.Fatal("ordinary member prepare", w.Code, w.Body.String())
			}
			if w = readingHTTP(t, h, "GET", "/v1/reading/sessions/"+own.SessionID+"/progress", token, own.Revision, nil); w.Code != 403 {
				t.Fatal("cross-account member session admitted", w.Code, w.Body.String())
			}
			if w = readingHTTP(t, h, "DELETE", "/v1/reading/sessions/"+own.SessionID, member.Session.AccessToken, "", nil); w.Code != 204 {
				t.Fatal("ordinary member cleanup", w.Code, w.Body.String())
			}
		}
		method, template, _ := strings.Cut(pattern, " ")
		isList := template == "/v1/reading/items" || template == "/v1/reading/continue" || template == "/v1/reading/discover"
		if template == "/v1/reading/continue" {
			page := int64(0)
			w := readingHTTP(t, h, "PUT", "/v1/reading/sessions/"+manifest.SessionID+"/progress", token, manifest.Revision, ReadingProgressRequest{Location: ReadingLocation{Format: "pdf", PageIndex: &page, Progression: .25}})
			if w.Code != 200 {
				t.Fatal("actual continue position", w.Code, w.Body.String())
			}
		}
		expectedRevision := int64(0)
		request := func(bearer string, m ReadingSessionResponse) *httptest.ResponseRecorder {
			path := strings.NewReplacer("{itemId}", r.item, "{sessionId}", m.SessionID, "{pageIndex}", "0").Replace(template)
			var body any
			switch {
			case strings.HasSuffix(template, "/annotations") && method == "PUT":
				body = annotationBody(expectedRevision)
			case strings.HasSuffix(template, "/file-save"):
				body = ReadingFileSaveRequest{OperationID: "3f5a1f1c-1cb4-4c72-a063-bd00dc16b36e"}
			case strings.HasSuffix(template, "/assets"):
				if len(m.Spine) == 0 {
					t.Fatal("actual EPUB spine is absent")
				}
				path += "?path=" + url.QueryEscape(m.Spine[0])
			case strings.Contains(template, "/pages/"):
				path += "?width=32"
			case isList:
				path += "?limit=1"
			case method == "POST":
				body = ReadingSessionRequest{r.item, r.source}
			case method == "PUT":
				page := int64(0)
				body = ReadingProgressRequest{ExpectedRevision: expectedRevision, Location: ReadingLocation{Format: "pdf", PageIndex: &page, Progression: .25}}
			}
			var raw []byte
			if body != nil {
				raw, err = json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(method, path, bytes.NewReader(raw))
			if bearer != "" {
				req.Header.Set("Authorization", "Bearer "+bearer)
			}
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("If-Match", `"`+m.Revision+`"`)
			if method == "GET" && strings.HasSuffix(template, "/source") {
				req.Header.Set("Range", "bytes=0-31")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			return w
		}
		assertPrivate := func(w *httptest.ResponseRecorder) {
			t.Helper()
			for _, hidden := range []string{r.item, r.source, r.library, manifest.SessionID, r.path, manifest.Metadata.Title, annotationID, annotationNote} {
				if hidden != "" && strings.Contains(w.Body.String(), hidden) {
					t.Errorf("refusal leaked hidden Reading identity %q: %s", hidden, w.Body.String())
				}
			}
			if w.Code == 401 || w.Code >= 500 {
				t.Errorf("valid reader answered %d: %s", w.Code, w.Body.String())
			}
		}
		assertSameRefusal := func(hidden, absent *httptest.ResponseRecorder) {
			t.Helper()
			assertPrivate(hidden)
			assertPrivate(absent)
			if hidden.Code < 400 || hidden.Code != absent.Code || bodyErrorCode(hidden.Body.String()) != bodyErrorCode(absent.Body.String()) {
				t.Errorf("hidden/absent differ: %d %s / %d %s", hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())
			}
		}
		// A session id is private even from the primary profile of its owner.
		// The foreign-profile request cannot close or write the real session.
		if strings.Contains(template, "{sessionId}") {
			before := readingRestrictionProgress(t, r)
			absent := manifest
			absent.SessionID = identity.Token()
			assertSameRefusal(request(r.f.owner.AccessToken, manifest), request(r.f.owner.AccessToken, absent))
			if after := readingRestrictionProgress(t, r); after != before {
				t.Fatal("foreign session write changed progress")
			}
			w := readingHTTP(t, h, "GET", "/v1/reading/sessions/"+manifest.SessionID+"/progress", token, manifest.Revision, nil)
			if w.Code != 200 {
				t.Fatal("foreign request retired the owning reader", w.Code, w.Body.String())
			}
		}
		if w := request("", manifest); w.Code != 401 {
			t.Fatalf("anonymous Reading request: %d %s", w.Code, w.Body.String())
		}
		// Successful controls prove the route, session and format-specific input
		// are real. DELETE's control is last so earlier probes retain authority.
		if method != "DELETE" {
			w := request(token, manifest)
			want := 200
			if method == "GET" && strings.HasSuffix(template, "/source") {
				want = 206
			}
			if w.Code != want {
				t.Fatalf("visible Reading control: %d %s", w.Code, w.Body.String())
			}
			if template == "/v1/reading/sessions/{sessionId}/annotations" && method == "GET" && !strings.Contains(w.Body.String(), annotationNote) {
				t.Fatal("visible annotation control omitted its actual private note")
			}
			if strings.HasSuffix(template, "/file-save") && (w.Body.Len() == 0 || w.Header().Get("X-Reading-Item") != r.item || w.Header().Get("X-Reading-Source") != r.source) {
				t.Fatal("visible file-save control omitted the original publication")
			}
			if isList || strings.Contains(template, "{itemId}") {
				if !strings.Contains(w.Body.String(), r.source) {
					t.Fatal("visible list/version omitted actual source")
				}
			} else if strings.HasSuffix(template, "/assets") || strings.Contains(template, "/pages/") || want == 206 {
				if w.Body.Len() == 0 {
					t.Fatal("visible byte control returned no bytes")
				}
			}
		}
		if method == "PUT" {
			expectedRevision = 1
		}
		before := readingRestrictionProgress(t, r)
		if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
			t.Fatal(err)
		}
		if method != "DELETE" {
			w := request(token, manifest)
			assertPrivate(w)
			if template == "/v1/reading/discover" {
				var page ReadingDiscoverResponse
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Rows) != 1 || page.Rows[0].Key != "recent" || len(page.Rows[0].Items) != 0 {
					t.Fatal("restricted discover rows", w.Code, w.Body.String())
				}
			} else if isList {
				var list ReadingItemsResponse
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 0 || list.NextCursor != "" {
					t.Fatal("restricted bounded list", w.Code, w.Body.String())
				}
			} else if w.Code != 404 || bodyErrorCode(w.Body.String()) != "not_found" {
				t.Fatal("new title restriction did not fence route", w.Code, w.Body.String())
			}
		}
		// Item/source identifiers hidden by title policy answer exactly like
		// absent ones. Session ids instead use the foreign-profile comparison
		// above: the owning actor already knows its captured session exists.
		compareItems := func() {
			t.Helper()
			switch {
			case strings.Contains(template, "{itemId}"):
				path := "/v1/items/" + r.item + "/reading/versions"
				assertSameRefusal(readingHTTP(t, h, method, path, token, "", nil), readingHTTP(t, h, method, strings.Replace(path, r.item, absentTwin(r.item), 1), token, "", nil))
			case method == "POST" && template == "/v1/reading/sessions":
				for _, absent := range []ReadingSessionRequest{{absentTwin(r.item), r.source}, {r.item, absentTwin(r.source)}} {
					assertSameRefusal(request(token, manifest), readingHTTP(t, h, method, template, token, "", absent))
				}
			}
		}
		compareItems()
		if after := readingRestrictionProgress(t, r); after != before {
			t.Fatal("title-restricted requests changed persisted progress")
		}
		// Restore the title policy, capture a fresh lifecycle, then revoke the
		// library at the profile writer. A prior successful open is no grant.
		if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 2, AllowUnrated: true}); err != nil {
			t.Fatal(err)
		}
		manifest = open()
		if _, err = r.f.id.EditDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
			t.Fatal(err)
		}
		w := request(token, manifest)
		if method == "DELETE" {
			// Teardown of one's own lifecycle remains allowed after hiding its
			// title; it returns no catalogue data and cannot address another actor.
			if w.Code != 204 || w.Body.Len() != 0 {
				t.Fatal("own hidden session cleanup", w.Code, w.Body.String())
			}
		} else if isList {
			assertPrivate(w)
			var list ReadingItemsResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 0 {
				t.Fatal("revoked library listed", w.Code, w.Body.String())
			}
			// A bounded list filters both hidden and absent libraries to the
			// same empty result, rather than performing an item lookup.
			path := template + "?libraryId="
			hidden := readingHTTP(t, h, "GET", path+url.QueryEscape(r.library), token, "", nil)
			absent := readingHTTP(t, h, "GET", path+url.QueryEscape(absentTwin(r.library)), token, "", nil)
			assertPrivate(hidden)
			assertPrivate(absent)
			if hidden.Code != 200 || absent.Code != 200 || normalizedAnswer(hidden.Body.String(), r.library) != normalizedAnswer(absent.Body.String(), absentTwin(r.library)) {
				t.Fatal("hidden/absent library lists differ", hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())
			}
		} else {
			assertPrivate(w)
			if w.Code < 400 {
				t.Fatal("revoked library still authorized", w.Code, w.Body.String())
			}
			compareItems()
		}
		if after := readingRestrictionProgress(t, r); after != before {
			t.Fatal("library-restricted requests changed persisted progress")
		}
	}
}

func readingRestrictionProgress(t *testing.T, r *readingFixture) string {
	t.Helper()
	// Keep the original progress oracle and also prove that refused annotation
	// requests cannot change a private set, entry, or PDF geometry.
	queries := []string{
		`SELECT owner_key,item_id,asset_id,source_revision,format,cfi,section,page_index,progression,revision,observed_at FROM reading_progress ORDER BY owner_key,item_id,asset_id`,
		`SELECT * FROM reading_annotation_sets ORDER BY owner_key,item_id,asset_id,source_revision`,
		`SELECT * FROM reading_annotations ORDER BY owner_key,item_id,asset_id,source_revision,id`,
		`SELECT * FROM reading_annotation_rects ORDER BY owner_key,item_id,asset_id,source_revision,annotation_id,ordinal`,
	}
	var state [][][]any
	for _, query := range queries {
		rows, err := r.f.db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		var table [][]any
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(values))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err = rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			table = append(table, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		state = append(state, table)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
