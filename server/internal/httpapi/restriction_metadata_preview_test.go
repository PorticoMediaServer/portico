package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// SEC-02 / BE-SRV-01 / ARCH-SRV-08: the metadata preview query.
//
// GET and DELETE /v1/metadata/bulk/preview-queries/{queryId} reach catalogue
// content only through an opaque private query handle minted by
// POST /v1/metadata/bulk/preview. A probe that invents the handle proves only
// that an unknown handle is refused, so this check mints a real one whose
// preparation really carries a title the restricted caller may not see, then
// proves that:
//
//   - the restricted caller (a member: metadata preview is owner/administrator
//     work) is refused the real handle exactly as an absent one, and cannot
//     release or observe the owner's preparation;
//   - another session of the owner's own profile reads it as absent and its
//     release is a no-op, so the owner keeps the handle;
//   - a real handle of another family (a browse-selection preparation) is also
//     absent here and cannot be released through this route;
//   - the owner's own release works and the handle is then gone.
func bulkMetadataPreviewQueryRestrictionCheck(method string) func(*testing.T) {
	return func(t *testing.T) {
		f := newBulkPreviewHTTPFixture(t)
		// The second session is issued before any handle is minted: a fresh
		// session must not disturb the first session's fence or family.
		other, err := f.d.Identity.Issue("owner", "profile", "local", "owner", 1)
		if err != nil {
			t.Fatal(err)
		}
		c := catalogtest.New(t, f.d.DB)
		hidden := c.Movie(c.Handle("private-library"), "/preview/hidden.mkv", "Preview Hidden Title", 2018)
		visible := editorItemPublic(t, f.d.DB, "Alpha")
		c.Drain()

		// A real private preparation: the worker is held so the POST answers a
		// genuine pending handle rather than racing to a ready body.
		release := blockBulkPreviewWorker(t, f.store)
		request := catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{
			{Kind: "item", ContentKind: "movie", ID: hidden.Public, LibraryID: "private-library"},
			{Kind: "item", ContentKind: "movie", ID: visible, LibraryID: "private-library"},
		}}
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		response := editorRequest(t, f.h, "POST", "/v1/metadata/bulk/preview", f.owner, "application/json", raw)
		if response.Code != 202 {
			t.Fatalf("prepare %d %s", response.Code, response.Body.String())
		}
		path := response.Header().Get("Location")
		if !strings.HasPrefix(path, "/v1/metadata/bulk/preview-queries/") {
			t.Fatalf("preparation location %q", path)
		}
		release()
		ready := bulkPreviewHTTPReady(t, f.h, f.owner, response)
		if ready.Code != 200 || !strings.Contains(ready.Body.String(), hidden.Public) {
			t.Fatalf("control: the real preparation does not carry the hidden title: %d %s", ready.Code, clip(ready.Body.String()))
		}
		absent := "/v1/metadata/bulk/preview-queries/matrix-absent-query"

		// The restricted member is refused both handles, and the owner's real
		// handle survives both attempts.
		memberGet := editorRequest(t, f.h, "GET", path, f.member, "", nil)
		if memberGet.Code != 403 {
			t.Fatalf("member read %d %s", memberGet.Code, memberGet.Body.String())
		}
		memberDelete := editorRequest(t, f.h, "DELETE", path, f.member, "", nil)
		if memberDelete.Code != 403 {
			t.Fatalf("member release %d %s", memberDelete.Code, memberDelete.Body.String())
		}
		sameAnswer(t, "member read", memberGet, editorRequest(t, f.h, "GET", absent, f.member, "", nil))
		sameAnswer(t, "member release", memberDelete, editorRequest(t, f.h, "DELETE", absent, f.member, "", nil))
		if survived := editorRequest(t, f.h, "GET", path, f.owner, "", nil); survived.Code != 200 || !strings.Contains(survived.Body.String(), hidden.Public) {
			t.Fatalf("member attempts disturbed the owner's preparation: %d %s", survived.Code, clip(survived.Body.String()))
		}

		// Another session of the owner's profile reads it as absent; its release
		// is a no-op and the owner keeps the handle.
		otherGet := editorRequest(t, f.h, "GET", path, other.AccessToken, "", nil)
		if otherGet.Code != 409 || bodyErrorCode(otherGet.Body.String()) != "stale_continuation" {
			t.Fatalf("another session's read %d %s", otherGet.Code, otherGet.Body.String())
		}
		sameAnswer(t, "another session's read", otherGet, editorRequest(t, f.h, "GET", absent, other.AccessToken, "", nil))
		otherDelete := editorRequest(t, f.h, "DELETE", path, other.AccessToken, "", nil)
		if otherDelete.Code != 204 {
			t.Fatalf("another session's release %d %s", otherDelete.Code, otherDelete.Body.String())
		}
		sameAnswer(t, "another session's release", otherDelete, editorRequest(t, f.h, "DELETE", absent, other.AccessToken, "", nil))
		if survived := editorRequest(t, f.h, "GET", path, f.owner, "", nil); survived.Code != 200 {
			t.Fatalf("another session's release disturbed the owner's preparation: %d %s", survived.Code, survived.Body.String())
		}

		// A real handle of another family is an absent query here, and releasing
		// it through this route does not release the other preparation.
		browsePath, browseQuery := metadataPreviewBrowseHandle(t, f)
		wrong := editorRequest(t, f.h, "GET", "/v1/metadata/bulk/preview-queries/"+browseQuery, f.owner, "", nil)
		if wrong.Code != 409 || bodyErrorCode(wrong.Body.String()) != "stale_continuation" {
			t.Fatalf("wrong-family read %d %s", wrong.Code, wrong.Body.String())
		}
		sameAnswer(t, "wrong-family read", wrong, editorRequest(t, f.h, "GET", absent, f.owner, "", nil))
		if wrongDelete := editorRequest(t, f.h, "DELETE", "/v1/metadata/bulk/preview-queries/"+browseQuery, f.owner, "", nil); wrongDelete.Code != 204 {
			t.Fatalf("wrong-family release %d %s", wrongDelete.Code, wrongDelete.Body.String())
		}
		if browse := editorRequest(t, f.h, "GET", browsePath, f.owner, "", nil); browse.Code != 200 {
			t.Fatalf("a wrong-family release cancelled the browse preparation: %d %s", browse.Code, browse.Body.String())
		}

		// The owner's own verb, then release; afterwards the handle is gone.
		if method == "GET" {
			if owned := editorRequest(t, f.h, "GET", path, f.owner, "", nil); owned.Code != 200 {
				t.Fatalf("owned read %d %s", owned.Code, owned.Body.String())
			}
		}
		if released := editorRequest(t, f.h, "DELETE", path, f.owner, "", nil); released.Code != 204 {
			t.Fatalf("owned release %d %s", released.Code, released.Body.String())
		}
		if gone := editorRequest(t, f.h, "GET", path, f.owner, "", nil); gone.Code != 409 || bodyErrorCode(gone.Body.String()) != "stale_continuation" {
			t.Fatalf("released query survived %d %s", gone.Code, gone.Body.String())
		}
		if stats := f.store.Stats(); stats.Deliveries != 0 {
			t.Fatalf("private preview delivery leaked: %+v", stats)
		}
	}
}

// metadataPreviewBrowseHandle mints a real browse-selection preparation through
// its own route, so "another family" names a handle that actually exists.
func metadataPreviewBrowseHandle(t *testing.T, f *bulkPreviewHTTPFixture) (string, string) {
	t.Helper()
	f.d.PrepareBrowseSelections = true
	f.h = New(f.d)
	response := editorRequest(t, f.h, "POST", "/v1/libraries/private-library/browse", f.owner, "application/json", []byte(`{"pivot":"movies","limit":1}`))
	if response.Code != 200 {
		t.Fatalf("browse control %d %s", response.Code, response.Body.String())
	}
	var page catalog.BrowseResult
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.WholeSelectionPreparing == nil || page.WholeSelectionPreparing.QueryID == "" {
		t.Fatalf("browse control did not prepare: %s", clip(response.Body.String()))
	}
	path := browseSelectionQueryPath("private-library", page.WholeSelectionPreparing.QueryID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		response = editorRequest(t, f.h, "GET", path, f.owner, "", nil)
		if response.Code == 200 {
			return path, page.WholeSelectionPreparing.QueryID
		}
		if response.Code != 202 {
			t.Fatalf("browse poll %d %s", response.Code, response.Body.String())
		}
		if time.Now().After(deadline) {
			t.Fatal("browse preparation did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBulkMetadataPreviewQueryPrivateHandleRespectsRestriction(t *testing.T) {
	for _, method := range []string{"GET", "DELETE"} {
		t.Run(method, bulkMetadataPreviewQueryRestrictionCheck(method))
	}
}
