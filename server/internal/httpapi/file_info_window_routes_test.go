package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// The FileInfo lanes are exercised through the real router, the real identity
// family and the real shared read-preparation runtime. Only the source facts are
// fixture rows; no lane plan, window, cursor or ETag is stubbed.
type fileInfoWindowsFixture struct {
	base      *tl11AdmissionFixture
	d         Dependencies
	h         http.Handler
	runtime   *catalog.FileInfoRuntime
	opening   string
	path      string
	identity  catalog.FileInfoIdentity
	principal identity.Principal
	caller    readprep.Caller
}

func newFileInfoWindowsFixture(t *testing.T) *fileInfoWindowsFixture {
	t.Helper()
	base := newTL11AdmissionFixture(t, "movie")
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runtime, err := catalog.NewFileInfoRuntime(base.db, store, catalog.CheckFileInfoAuthority)
	if err != nil {
		t.Fatal(err)
	}
	base.d.FileInfoRuntime = runtime
	f := &fileInfoWindowsFixture{base: base, d: base.d, runtime: runtime}
	f.h = New(base.d)
	base.c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) SELECT token,1,size,modified_ns,'http' FROM catalog_assets WHERE id=?`, base.item.Asset)
	base.c.Drain()
	if err = base.db.QueryRow(`SELECT path FROM catalog_assets WHERE id=?`, base.item.Asset).Scan(&f.path); err != nil {
		t.Fatal(err)
	}
	f.mint(t)
	return f
}

func (f *fileInfoWindowsFixture) mint(t *testing.T) {
	t.Helper()
	request := httptest.NewRequest("GET", "/v1/items/"+f.base.item.Public+"/file-info", nil)
	request.Header.Set("Authorization", "Bearer "+f.base.token)
	ctx, caller, err := f.d.fileInfoContext(request)
	if err != nil {
		t.Fatal(err)
	}
	if f.principal, err = f.d.principal(request); err != nil {
		t.Fatal(err)
	}
	f.caller = caller
	var public []byte
	if err = f.base.db.QueryRow(`SELECT id,library_id,public_id FROM catalog_entities WHERE id=?`, f.base.item.ID).Scan(&f.identity.Item, &f.identity.Library, &public); err != nil {
		t.Fatal(err)
	}
	copy(f.identity.PublicItem[:], public)
	err = dbwork.WithReadSnapshot(ctx, f.base.db, func(inner context.Context) error {
		var err error
		f.opening, err = f.runtime.AcceptDetailOpening(inner, caller, f.identity, catalog.ContentRevision{Catalog: 3, Viewer: 7})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fileInfoWindowsFixture) request(method, path string, headers map[string]string) *httptest.ResponseRecorder {
	f.base.t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+f.base.token)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

// lane follows the documented continuation: a 202 polls the private handle until
// a ready page arrives.
func (f *fileInfoWindowsFixture) lane(t *testing.T, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	w := f.request("GET", path, headers)
	deadline := time.Now().Add(8 * time.Second)
	for w.Code == http.StatusAccepted && time.Now().Before(deadline) {
		location := w.Header().Get("Location")
		if location == "" {
			t.Fatalf("preparation without Location: %s", w.Body.String())
		}
		if w.Header().Get("ETag") != "" {
			t.Fatalf("preparation carried an ETag: %v", w.Header())
		}
		time.Sleep(time.Millisecond)
		w = f.request("GET", location, nil)
	}
	return w
}

func fileInfoBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %s: %v", w.Body.String(), err)
	}
	return out
}

func fileInfoLanePath(id, suffix, opening string) string {
	separator := "?"
	if strings.Contains(suffix, "?") {
		separator = "&"
	}
	return "/v1/items/" + id + suffix + separator + "opening=" + opening
}

func TestFileInfoWindowsRealLanesHeadersAndETags(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	id := f.base.item.Public

	files := f.lane(t, fileInfoLanePath(id, "/file-info", f.opening), nil)
	if files.Code != 200 {
		t.Fatalf("files %d %s", files.Code, files.Body.String())
	}
	if files.Header().Get("ETag") == "" || files.Header().Get("Cache-Control") != revalidate {
		t.Fatalf("ready headers %v", files.Header())
	}
	if issues := (apispec.Document{}).ValidateJSON(fileInfoFilesResponse{}.JSONSchemaForStatus(200), files.Body.Bytes()); len(issues) != 0 {
		t.Fatal(issues)
	}
	page := fileInfoBody[struct {
		Kind   string `json:"kind"`
		Detail struct {
			Revision catalog.ContentRevision `json:"revision"`
		} `json:"detail"`
		Basis  string `json:"basis"`
		Window struct {
			QueryID string `json:"queryId"`
			Limit   int    `json:"limit"`
		} `json:"window"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}](t, files)
	if page.Kind != "file-info-files" || len(page.Files) != 1 || page.Files[0].Path != f.path {
		t.Fatalf("files page %s", files.Body.String())
	}
	if page.Detail.Revision != (catalog.ContentRevision{Catalog: 3, Viewer: 7}) || len(page.Basis) != 43 || !validFileInfoQueryID(page.Window.QueryID) || page.Window.Limit != catalog.FileInfoDefaultLimit {
		t.Fatalf("basis lost: %s", files.Body.String())
	}

	// The exact ready body is its own validator on the retained private handle:
	// polling it is a 304 and carries no body. A fresh lane read is a new window
	// with its own query id, so 304 is proved against the handle the body named.
	conditional := f.request("GET", "/v1/file-info-queries/"+page.Window.QueryID, map[string]string{"If-None-Match": files.Header().Get("ETag")})
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 || conditional.Header().Get("ETag") != files.Header().Get("ETag") {
		t.Fatalf("conditional %d %v %s", conditional.Code, conditional.Header(), conditional.Body.String())
	}

	// ETags never cross lanes, even for the same opening and item.
	groups := f.lane(t, fileInfoLanePath(id, "/file-info/groups", f.opening), map[string]string{"If-None-Match": files.Header().Get("ETag")})
	if groups.Code != 200 {
		t.Fatalf("groups %d %s", groups.Code, groups.Body.String())
	}
	group := fileInfoBody[struct {
		Kind    string `json:"kind"`
		Surface struct {
			Manage *catalog.FileInfoManage `json:"manage"`
		} `json:"surface"`
		Groups []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}](t, groups)
	if group.Kind != "file-info-groups" || len(group.Groups) != 1 || !validFileInfoQueryID(fileInfoBody[struct {
		Window struct {
			QueryID string `json:"queryId"`
		} `json:"window"`
	}](t, groups).Window.QueryID) {
		t.Fatalf("groups page %s", groups.Body.String())
	}
	if group.Surface.Manage == nil || !group.Surface.Manage.Delete || !group.Surface.Manage.Split || !group.Surface.Manage.Merge {
		t.Fatalf("owner manage %s", groups.Body.String())
	}
	groupsQuery := fileInfoBody[struct {
		Window struct {
			QueryID string `json:"queryId"`
		} `json:"window"`
	}](t, groups).Window.QueryID
	if crossed := f.request("GET", "/v1/file-info-queries/"+groupsQuery, map[string]string{"If-None-Match": files.Header().Get("ETag")}); crossed.Code != 200 {
		t.Fatalf("files validator answered the groups window: %d", crossed.Code)
	}

	members := f.lane(t, fileInfoLanePath(id, "/file-info/groups/members?group="+group.Groups[0].ID, f.opening), nil)
	if members.Code != 200 || !strings.Contains(members.Body.String(), f.path) {
		t.Fatalf("members %d %s", members.Code, members.Body.String())
	}
	sources := f.lane(t, fileInfoLanePath(id, "/file-info/sources", f.opening), nil)
	if sources.Code != 200 {
		t.Fatalf("sources %d %s", sources.Code, sources.Body.String())
	}
	// Independent lanes keep independent handles.
	if members.Header().Get("ETag") == sources.Header().Get("ETag") {
		t.Fatal("lanes shared a validator")
	}
}

func TestFileInfoWindowsFiniteInputs(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	id := f.base.item.Public
	cases := []string{
		"/v1/items/" + id + "/file-info",
		fileInfoLanePath(id, "/file-info", f.opening+"a"),
		fileInfoLanePath(id, "/file-info", f.opening) + "&limit=0",
		fileInfoLanePath(id, "/file-info", f.opening) + "&limit=101",
		fileInfoLanePath(id, "/file-info", f.opening) + "&limit=nan",
		fileInfoLanePath(id, "/file-info", f.opening) + "&cursor=" + strings.Repeat("a", fileInfoCursorText) + "&around=" + strings.Repeat("b", fileInfoOccurrenceText),
		"/v1/items/" + id + "/file-info/groups/members?opening=" + f.opening,
		fileInfoLanePath(id, "/file-info/tracks?occurrence="+strings.Repeat("c", fileInfoOccurrenceText)+"&lane=video", f.opening),
		fileInfoLanePath(id, "/file-info", f.opening) + "&unknown=1",
	}
	for _, path := range cases {
		w := f.request("GET", path, nil)
		if w.Code != 400 {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// An empty limit is the default, and an explicit maximum is legal.
	for _, path := range []string{
		fileInfoLanePath(id, "/file-info", f.opening) + "&limit=",
		fileInfoLanePath(id, "/file-info", f.opening) + "&limit=100",
	} {
		if w := f.lane(t, path, nil); w.Code != 200 {
			t.Errorf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// A hidden or malformed item id never becomes a 500.
	if w := f.request("GET", "/v1/items/not-an-id/file-info?opening="+f.opening, nil); w.Code != 404 {
		t.Errorf("malformed item: %d %s", w.Code, w.Body.String())
	}
}

func TestFileInfoWindowsPrivatePollAndCancel(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	id := f.base.item.Public
	files := f.lane(t, fileInfoLanePath(id, "/file-info", f.opening), nil)
	if files.Code != 200 {
		t.Fatalf("files %d %s", files.Code, files.Body.String())
	}
	queryID := fileInfoBody[struct {
		Window struct {
			QueryID        string `json:"queryId"`
			ReloadCursor   string `json:"reloadCursor"`
			NextCursor     string `json:"nextCursor"`
			PreviousCursor string `json:"previousCursor"`
		} `json:"window"`
	}](t, files).Window.QueryID
	if !validFileInfoQueryID(queryID) {
		t.Fatalf("query id %q", queryID)
	}

	// The same family polls its own ready window.
	polled := f.request("GET", "/v1/file-info-queries/"+queryID, nil)
	if polled.Code != 200 || !strings.Contains(polled.Body.String(), `"file-info-files"`) {
		t.Fatalf("poll %d %s", polled.Code, polled.Body.String())
	}
	if issues := (apispec.Document{}).ValidateJSON(fileInfoQueryResponse{}.JSONSchemaForStatus(200), polled.Body.Bytes()); len(issues) != 0 {
		t.Fatal(issues)
	}

	// Another caller cannot poll it, and cancelling it is a no-op there.
	if _, err := f.runtime.Poll(context.Background(), "foreign", queryID); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("foreign poll = %v", err)
	}
	if err := f.runtime.Cancel(context.Background(), "foreign", queryID); err != nil {
		t.Fatalf("foreign cancel = %v", err)
	}
	if again := f.request("GET", "/v1/file-info-queries/"+queryID, nil); again.Code != 200 {
		t.Fatalf("foreign cancel retired the owner's handle: %d %s", again.Code, again.Body.String())
	}

	// Cancel is idempotent and private.
	if done := f.request("DELETE", "/v1/file-info-queries/"+queryID, nil); done.Code != 204 {
		t.Fatalf("cancel %d %s", done.Code, done.Body.String())
	}
	if done := f.request("DELETE", "/v1/file-info-queries/"+queryID, nil); done.Code != 204 {
		t.Fatalf("repeat cancel %d %s", done.Code, done.Body.String())
	}
	if retired := f.request("GET", "/v1/file-info-queries/"+queryID, nil); retired.Code != 409 || strings.Contains(retired.Body.String(), f.path) {
		t.Fatalf("retired poll %d %s", retired.Code, retired.Body.String())
	}
	// A tampered handle is stale, never another caller's content.
	tampered := queryID[:len(queryID)-1] + "A"
	if tampered == queryID {
		tampered = queryID[:len(queryID)-1] + "B"
	}
	if w := f.request("GET", "/v1/file-info-queries/"+tampered, nil); w.Code != 409 {
		t.Fatalf("tampered handle %d %s", w.Code, w.Body.String())
	}
	// A malformed handle is a client error.
	if w := f.request("GET", "/v1/file-info-queries/not-a-handle", nil); w.Code != 400 {
		t.Fatalf("malformed handle %d %s", w.Code, w.Body.String())
	}
	if w := f.request("DELETE", "/v1/file-info-queries/not-a-handle", nil); w.Code != 400 {
		t.Fatalf("malformed cancel %d %s", w.Code, w.Body.String())
	}
}

func TestFileInfoWindowsPreparingHeadersAndSchema(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	queryID := strings.Repeat("a", fileInfoOpeningText) + "." + strings.Repeat("b", fileInfoCursorText)
	detail := &catalog.FileInfoDetailCapture{
		Scope:    catalog.DetailScope{ServerID: "server", LibraryID: "movies", ItemID: f.base.item.Public, ViewerFence: "fence"},
		Revision: catalog.ContentRevision{Catalog: 3, Viewer: 7},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/file-info-queries/"+queryID, nil)
	// A preparation must beat a conditional request and never carry an ETag.
	r.Header.Set("If-None-Match", "*")
	f.d.writeFileInfoWindow(w, r, catalog.FileInfoWindowResult{Preparing: &catalog.FileInfoPreparing{
		Kind: "file-info-preparing", State: "preparing", QueryID: queryID, RetryAfterMs: 1501, Detail: detail,
	}})
	if w.Code != http.StatusAccepted || w.Header().Get("Location") != "/v1/file-info-queries/"+queryID || w.Header().Get("Retry-After") != "2" || w.Header().Get("ETag") != "" {
		t.Fatalf("preparing %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("preparing cache %q", w.Header().Get("Cache-Control"))
	}
	if issues := (apispec.Document{}).ValidateJSON(fileInfoFilesResponse{}.JSONSchemaForStatus(202), w.Body.Bytes()); len(issues) != 0 {
		t.Fatal(issues)
	}
	// The generic poll carrier leaves detail optional for opening preparation.
	if issues := (apispec.Document{}).ValidateJSON(fileInfoQueryResponse{}.JSONSchemaForStatus(202), w.Body.Bytes()); len(issues) != 0 {
		t.Fatal(issues)
	}
	// A lane carrier always echoes the original Detail; the schema says so.
	withoutDetail, _ := json.Marshal(catalog.FileInfoPreparing{Kind: "file-info-preparing", State: "preparing", QueryID: queryID, RetryAfterMs: 1501})
	if issues := (apispec.Document{}).ValidateJSON(fileInfoFilesResponse{}.JSONSchemaForStatus(202), withoutDetail); len(issues) == 0 {
		t.Fatal("lane preparing schema accepted a missing original Detail")
	}
	if issues := (apispec.Document{}).ValidateJSON(fileInfoQueryResponse{}.JSONSchemaForStatus(202), withoutDetail); len(issues) != 0 {
		t.Fatalf("opening preparing schema required detail: %v", issues)
	}
	if !validFileInfoQueryID("open." + strings.Repeat("a", fileInfoCursorText)) {
		t.Fatal("opening preparation handle refused")
	}
	if validFileInfoQueryID("open.") || validFileInfoQueryID("open.../../x") {
		t.Fatal("unbounded opening handle accepted")
	}
}

func TestFileInfoWindowsGuardRebuildsLiveAuthority(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	request := httptest.NewRequest("GET", "/v1/items/"+f.base.item.Public+"/file-info", nil)
	request.Header.Set("Authorization", "Bearer "+f.base.token)
	_, caller, err := f.d.fileInfoContext(request)
	if err != nil {
		t.Fatal(err)
	}
	guard := f.d.fileInfoAuthorityCheck(f.principal, caller)
	if _, err = catalog.CheckFileInfoAuthority(catalog.WithFileInfoAuthority(context.Background(), guard), "another-caller", f.identity); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("foreign caller = %v", err)
	}
	admission, err := guard(context.Background(), caller, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	if admission.Identity != f.identity || !admission.Owner {
		t.Fatalf("owner admission %+v", admission)
	}
	// The Detail scope carries the Detail page's own fence, not the home
	// audience fence the FileInfo caller is derived from.
	_, detailFence, err := f.d.detailLibraryFenceFor(request, f.principal, "movies")
	if err != nil {
		t.Fatal(err)
	}
	if admission.DetailScope.ViewerFence != detailFence {
		t.Fatalf("detail fence %q, want %q", admission.DetailScope.ViewerFence, detailFence)
	}
	tampered := f.identity
	tampered.Item++
	if _, err = guard(context.Background(), caller, tampered); !errors.Is(err, readprep.ErrStale) {
		t.Fatalf("tampered identity = %v", err)
	}
	// A forged non-owner role fails the live family check instead of inheriting
	// the captured owner authority.
	forged := f.principal
	forged.Role = identity.TierMember
	forgedGuard := f.d.fileInfoAuthorityCheck(forged, caller)
	if _, err = forgedGuard(context.Background(), caller, f.identity); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("forged role = %v", err)
	}
}

func TestFileInfoWindowsConditionalNeverBeatsAuthority(t *testing.T) {
	f := newFileInfoWindowsFixture(t)
	id := f.base.item.Public
	files := f.lane(t, fileInfoLanePath(id, "/file-info", f.opening), nil)
	if files.Code != 200 {
		t.Fatalf("files %d %s", files.Code, files.Body.String())
	}
	tag := files.Header().Get("ETag")
	queryID := fileInfoBody[struct {
		Window struct {
			QueryID string `json:"queryId"`
		} `json:"window"`
	}](t, files).Window.QueryID
	if _, err := f.base.db.Exec(`DELETE FROM catalog_entities WHERE id=?`, f.base.item.ID); err != nil {
		t.Fatal(err)
	}
	// Live authority runs before the conditional decision, so a retained
	// validator never answers 304 for a retired item.
	retired := f.request("GET", "/v1/file-info-queries/"+queryID, map[string]string{"If-None-Match": tag})
	if retired.Code == http.StatusNotModified {
		t.Fatal("304 answered a retired item")
	}
	if retired.Code != 404 && retired.Code != 409 {
		t.Fatalf("retired poll %d %s", retired.Code, retired.Body.String())
	}
	fresh := f.request("GET", fileInfoLanePath(id, "/file-info", f.opening), map[string]string{"If-None-Match": tag})
	if fresh.Code == http.StatusNotModified {
		t.Fatal("304 answered a retired lane")
	}
	if fresh.Code != 404 && fresh.Code != 409 {
		t.Fatalf("retired %d %s", fresh.Code, fresh.Body.String())
	}
	// The runtime's live guard also refuses the retired identity directly.
	guard := f.d.fileInfoAuthorityCheck(f.principal, f.caller)
	if _, err := guard(context.Background(), f.caller, f.identity); err == nil {
		t.Fatal("guard admitted a retired item")
	}
}
