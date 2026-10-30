package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

// SEC-02 / BE-SRV-01 / ARCH-SRV-08: the selection-action family.
//
// These routes reach catalogue content only through an opaque private handle: a
// volatile preparation, or a confirmed operation. A probe that borrows or
// invents a handle only exercises a cache miss (or an admission refusal), so
// each check mints the real handle through these same routes and then proves:
//
//  1. The restricted viewer's own preparation is built from its own effective
//     selection: the exact count, a sample of visible titles, and no hidden
//     identifier or title anywhere in the answer.
//  2. A real preparation minted while the whole selection was still allowed is
//     refused exactly like one that never existed once the profile may no
//     longer see one of its titles: current policy, not a cached payload, is
//     authority.
//  3. Another session of the same profile, a handle of another family
//     (a browse-selection preparation) and a library the profile is not given
//     are all indistinguishable from an absent handle, and cannot read, release
//     or consume the owner's handle. The owner keeps it afterwards (lifetime
//     and authority survive the attempts), and the confirmed operation is
//     likewise private to its session binding.
//
// The probes are real handles end to end; no dummy id proves anything here.

// restrictedSelectionActions is a fixture whose owner profile passes every
// authority gate (so role checks cannot mask the content story) and is then
// restricted: it may see only the "movies" library, and one of its titles is
// blocked by label. "other" is the hidden library.
type restrictedSelectionActions struct {
	f      *selectionHTTPFixture
	hidden catalogtest.Item
	// driftedInput and drifted were minted before the restriction, when the
	// hidden title was still part of the exact whole-view selection.
	driftedInput selectionActionInput
	drifted      selectionactions.Preview
}

func newRestrictedSelectionActions(t *testing.T) *restrictedSelectionActions {
	t.Helper()
	f := actionHTTPFixture(t, 3)
	// Keep the extra title inside the library's configured source: a delete
	// preview reads the same source facts as a real one.
	var root string
	if err := f.db.QueryRow(`SELECT root FROM libraries WHERE id='movies'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	hidden := f.c.Movie(f.c.Handle("movies"), filepath.Join(root, "hidden-selection-action.mp4"), "Hidden Selection Action", 2017)
	f.c.Attributes(hidden.ID, "label", "selection-action-private")
	f.c.Drain()

	driftedInput := actionHTTPInput(t, f)
	drifted := actionHTTPPreview(t, f, driftedInput)
	if drifted.Total != 4 || len(drifted.Sample) != 4 {
		t.Fatalf("unrestricted control selection holds %d titles: %+v", drifted.Total, drifted)
	}
	if !strings.Contains(mustJSON(drifted), hidden.Public) {
		t.Fatalf("control preparation does not carry the later-hidden title: %s", clip(mustJSON(drifted)))
	}

	ctx := context.Background()
	owner, err := f.d.Identity.DirectLogin(ctx, "owner", "long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	var primary identity.DirectProfile
	for _, profile := range owner.Profiles {
		if profile.Primary {
			primary = profile
		}
	}
	if primary.ID == "" {
		t.Fatal("fixture has no primary profile")
	}
	current, err := f.d.Identity.ProfileRestrictions(ctx, owner.AccountToken, primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.d.Identity.SetProfileRestrictions(ctx, owner.AccountToken, primary.ID, identity.ProfileRestrictionEdit{
		ExpectedRevision:   current.Revision,
		RatingSystem:       current.RatingSystem,
		MaximumAgeRating:   current.MaximumAgeRating,
		AllowUnrated:       true,
		BlockedLabels:      append(append([]string{}, current.BlockedLabels...), "selection-action-private"),
		AllowDownloads:     true,
		AllowLiveTV:        current.AllowLiveTV,
		AllowDVR:           current.AllowDVR,
		AllowWatchTogether: current.AllowWatchTogether,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.d.Identity.EditDirectProfile(ctx, owner.AccountToken, primary.ID, identity.DirectProfileEdit{
		ExpectedRevision: primary.Revision,
		Policy:           &identity.ProfilePolicy{AllowedLibraries: []string{"movies"}},
	}); err != nil {
		t.Fatal(err)
	}
	return &restrictedSelectionActions{f: f, hidden: hidden, driftedInput: driftedInput, drifted: drifted}
}

// secondSession is another live session for the same profile: the same
// effective restrictions, but not this session's private handles.
func (r *restrictedSelectionActions) secondSession(t *testing.T) string {
	t.Helper()
	p, err := r.f.d.Identity.Authenticate(r.f.token)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := r.f.d.Identity.Issue(p.AccountID, p.ProfileID, p.Authority, p.Role, p.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	return envelope.AccessToken
}

// fresh mints the restricted profile's own preparation from its exact current
// selection, and also returns the browse-selection handle of the same view so a
// cross-family attempt has a real handle to name.
func (r *restrictedSelectionActions) fresh(t *testing.T) (selectionactions.Preview, selectionActionInput, string) {
	t.Helper()
	pending, _ := r.f.prepare(t, nil)
	query := browseSelectionQueryPath("movies", pending.QueryID)
	descriptor, _ := selectionHTTPReady(t, r.f, query)
	remove := false
	input := selectionActionInput{
		Action:   "delete",
		Selector: catalog.JobSelector{Query: &descriptor.Selector.Query},
		Expected: catalog.JobExpected{CatalogRevision: descriptor.CatalogRevision},
		Options:  selectionactions.Options{DeleteFiles: &remove},
	}
	preview := actionHTTPPreview(t, r.f, input)
	if preview.Total != 3 || len(preview.Sample) != 3 {
		t.Fatalf("restricted preparation holds %d titles: %+v", preview.Total, preview)
	}
	if !strings.Contains(mustJSON(preview), r.f.item.Public) {
		t.Fatalf("control: the restricted preparation does not carry the visible title: %s", clip(mustJSON(preview)))
	}
	r.markerFree(t, "restricted preparation", mustJSON(preview))
	return preview, input, pending.QueryID
}

func (r *restrictedSelectionActions) markerFree(t *testing.T, what, body string) {
	t.Helper()
	for _, secret := range []string{r.hidden.Public, "Hidden Selection Action"} {
		if strings.Contains(body, secret) {
			t.Errorf("%s tells the restricted profile about a hidden title %q: %s", what, secret, clip(body))
		}
	}
}

// selectionActionAs sends one request as an explicit token, so a second session
// of the same profile can be probed without touching the fixture's own token.
func selectionActionAs(t *testing.T, f *selectionHTTPFixture, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

// sameAnswer fails when a real private handle answers differently from one that
// does not exist: that difference is exactly the existence signal the matrix
// forbids.
func sameAnswer(t *testing.T, what string, held, absent *httptest.ResponseRecorder) {
	t.Helper()
	heldCode, absentCode := bodyErrorCode(held.Body.String()), bodyErrorCode(absent.Body.String())
	if held.Code != absent.Code || heldCode != absentCode {
		t.Errorf("%s: a real private handle is distinguishable from an absent one: %d %q vs %d %q", what, held.Code, heldCode, absent.Code, absentCode)
	}
}

func selectionActionInputWithLibrary(in selectionActionInput, library string) selectionActionInput {
	out := in
	query := *in.Selector.Query
	query.LibraryID = library
	out.Selector.Query = &query
	return out
}

func selectionActionInputWithBasis(in selectionActionInput, basis string) selectionActionInput {
	out := in
	query := *in.Selector.Query
	query.SelectionBasis = basis
	out.Selector.Query = &query
	return out
}

func selectionActionReady(t *testing.T, f *selectionHTTPFixture, token, path string) selectionactions.Preview {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w := selectionActionAs(t, f, token, "GET", path, nil)
		if w.Code == 200 {
			var preview selectionactions.Preview
			if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			return preview
		}
		if w.Code != 202 {
			t.Fatalf("poll %d %s", w.Code, w.Body.String())
		}
		if time.Now().After(deadline) {
			t.Fatalf("action preparation did not finish: %+v", f.store.Stats())
		}
		time.Sleep(time.Millisecond)
	}
}

// selectionActionPreparationCreateRestrictionCheck covers
// POST /v1/libraries/{id}/selection-actions/preparations.
func selectionActionPreparationCreateRestrictionCheck(t *testing.T) {
	r := newRestrictedSelectionActions(t)
	// The real basis minted before the restriction now names a title the
	// profile may not see. It is refused like a basis that never existed.
	held := r.f.request("POST", "/v1/libraries/movies/selection-actions/preparations", nil, r.driftedInput)
	absent := r.f.request("POST", "/v1/libraries/movies/selection-actions/preparations", nil, selectionActionInputWithBasis(r.driftedInput, strings.Repeat("0", 64)))
	if held.Code != 409 || bodyErrorCode(held.Body.String()) != "stale_continuation" {
		t.Fatalf("stale real basis %d %s", held.Code, held.Body.String())
	}
	sameAnswer(t, "stale real basis", held, absent)
	r.markerFree(t, "stale real basis", held.Body.String())

	// The current exact selection is the visible subset, and it prepares for
	// real with no hidden identifier in the answer.
	_, input, _ := r.fresh(t)

	// A library the profile is not given answers as one that does not exist.
	hidden := r.f.request("POST", "/v1/libraries/other/selection-actions/preparations", nil, selectionActionInputWithLibrary(input, "other"))
	missing := r.f.request("POST", "/v1/libraries/no-such-library/selection-actions/preparations", nil, selectionActionInputWithLibrary(input, "no-such-library"))
	if hidden.Code != 404 || hidden.Code != missing.Code || bodyErrorCode(hidden.Body.String()) != bodyErrorCode(missing.Body.String()) {
		t.Errorf("hidden library differs from an absent one: %d %q / %d %q", hidden.Code, bodyErrorCode(hidden.Body.String()), missing.Code, bodyErrorCode(missing.Body.String()))
	}
	r.markerFree(t, "hidden library", hidden.Body.String())
}

// selectionActionPreparationQueryRestrictionCheck covers GET and DELETE
// /v1/libraries/{id}/selection-actions/preparations/{preparationId}.
func selectionActionPreparationQueryRestrictionCheck(method string) func(*testing.T) {
	return func(t *testing.T) {
		r := newRestrictedSelectionActions(t)
		preview, _, browseQuery := r.fresh(t)
		path := actionPreparationPath("movies", preview.PreparationID)
		absent := actionPreparationPath("movies", "matrix-absent-preparation")
		other := r.secondSession(t)

		// The owner's own real handle reads, and says nothing hidden.
		owned := r.f.request("GET", path, nil, nil)
		if owned.Code != 200 {
			t.Fatalf("owned preparation %d %s", owned.Code, owned.Body.String())
		}
		r.markerFree(t, "owned preparation", owned.Body.String())

		// A preparation minted while the hidden title was still selected is
		// refused exactly like one that never existed.
		drifted := r.f.request("GET", actionPreparationPath("movies", r.drifted.PreparationID), nil, nil)
		sameAnswer(t, "drifted preparation", drifted, r.f.request("GET", absent, nil, nil))
		r.markerFree(t, "drifted preparation", drifted.Body.String())

		// Another session of the same profile cannot read or release it.
		sameAnswer(t, "another session's poll", selectionActionAs(t, r.f, other, "GET", path, nil), selectionActionAs(t, r.f, other, "GET", absent, nil))
		if refused := selectionActionAs(t, r.f, other, "GET", path, nil); refused.Code != 409 || bodyErrorCode(refused.Body.String()) != "stale_continuation" {
			t.Fatalf("another session's poll %d %s", refused.Code, refused.Body.String())
		}
		if released := selectionActionAs(t, r.f, other, "DELETE", path, nil); released.Code != 204 {
			t.Fatalf("another session's release %d %s", released.Code, released.Body.String())
		}
		if again := r.f.request("GET", path, nil, nil); again.Code != 200 {
			t.Fatalf("a foreign release disturbed the owner's handle: %d %s", again.Code, again.Body.String())
		}

		// A real handle of another family is an absent preparation id, and
		// releasing it through this route must not release that other
		// preparation: cancellation is per caller and per handle family.
		browsePath := browseSelectionQueryPath("movies", browseQuery)
		wrong := r.f.request("GET", actionPreparationPath("movies", browseQuery), nil, nil)
		sameAnswer(t, "wrong-family preparation", wrong, r.f.request("GET", absent, nil, nil))
		wrongDelete := r.f.request("DELETE", actionPreparationPath("movies", browseQuery), nil, nil)
		if wrongDelete.Code != 204 {
			t.Fatalf("wrong-family release %d %s", wrongDelete.Code, wrongDelete.Body.String())
		}
		sameAnswer(t, "wrong-family release", wrongDelete, r.f.request("DELETE", absent, nil, nil))
		if browse := r.f.request("GET", browsePath, nil, nil); browse.Code != 200 {
			t.Fatalf("a wrong-family release cancelled the browse preparation: %d %s", browse.Code, browse.Body.String())
		}

		// Another session's real preparation cannot be released here, and it
		// remains usable by that session.
		foreignPath := actionPreparationPath("movies", r.mustPrepareAs(t, other))
		if stolen := r.f.request("DELETE", foreignPath, nil, nil); stolen.Code != 204 {
			t.Fatalf("another session's preparation release %d %s", stolen.Code, stolen.Body.String())
		}
		if still := selectionActionAs(t, r.f, other, "GET", foreignPath, nil); still.Code != 200 && still.Code != 202 {
			t.Fatalf("a foreign release cancelled another session's preparation: %d %s", still.Code, still.Body.String())
		}
		if released := selectionActionAs(t, r.f, other, "DELETE", foreignPath, nil); released.Code != 204 {
			t.Fatalf("own release for the other session %d %s", released.Code, released.Body.String())
		}

		// A library the profile is not given answers as one that does not exist,
		// for reading and for release.
		for _, verb := range []string{"GET", "DELETE"} {
			held := r.f.request(verb, actionPreparationPath("other", preview.PreparationID), nil, nil)
			missing := r.f.request(verb, actionPreparationPath("no-such-library", preview.PreparationID), nil, nil)
			if held.Code != 404 || held.Code != missing.Code || bodyErrorCode(held.Body.String()) != bodyErrorCode(missing.Body.String()) {
				t.Errorf("%s hidden library differs from an absent one: %d %q / %d %q", verb, held.Code, bodyErrorCode(held.Body.String()), missing.Code, bodyErrorCode(missing.Body.String()))
			}
			r.markerFree(t, verb+" hidden library", held.Body.String())
		}
		if again := r.f.request("GET", path, nil, nil); again.Code != 200 {
			t.Fatalf("a hidden-library attempt disturbed the owner's handle: %d %s", again.Code, again.Body.String())
		}

		// The owner's own verb, then release, then the handle is gone.
		if owned := r.f.request(method, path, nil, nil); method == "GET" && owned.Code != 200 || method == "DELETE" && owned.Code != 204 {
			t.Fatalf("owned %s %d %s", method, owned.Code, owned.Body.String())
		}
		if method == "GET" {
			if released := r.f.request("DELETE", path, nil, nil); released.Code != 204 {
				t.Fatalf("owned release %d %s", released.Code, released.Body.String())
			}
		}
		if gone := r.f.request("GET", path, nil, nil); gone.Code != 409 || bodyErrorCode(gone.Body.String()) != "stale_continuation" {
			t.Fatalf("released handle survived %d %s", gone.Code, gone.Body.String())
		}
	}
}

// selectionActionConfirmOperationRestrictionCheck covers the confirmation and
// the confirmed operation: POST /v1/libraries/{id}/selection-actions, and GET
// and DELETE /v1/selection-actions/{operationId}.
func selectionActionConfirmOperationRestrictionCheck(method string) func(*testing.T) {
	return func(t *testing.T) {
		r := newRestrictedSelectionActions(t)
		preview, _, _ := r.fresh(t)
		other := r.secondSession(t)
		absentPrep := "matrix-absent-preparation"
		absentOp := "matrix-absent-operation"

		// A real preparation whose selection now hides a title is an absent
		// preparation at confirmation: neither consumes anything.
		drifted := r.f.request("POST", "/v1/libraries/movies/selection-actions", nil, selectionactions.Confirm{OperationID: "matrix-drifted-action", PreparationID: r.drifted.PreparationID, Confirmation: r.drifted.Confirmation})
		if drifted.Code != 409 || bodyErrorCode(drifted.Body.String()) != "stale_continuation" {
			t.Fatalf("drifted confirmation %d %s", drifted.Code, drifted.Body.String())
		}
		sameAnswer(t, "drifted confirmation", drifted, r.f.request("POST", "/v1/libraries/movies/selection-actions", nil, selectionactions.Confirm{OperationID: "matrix-absent-prep-action", PreparationID: absentPrep, Confirmation: "1"}))
		r.markerFree(t, "drifted confirmation", drifted.Body.String())

		// Another session's real preparation cannot be consumed here, and it
		// remains usable by that session afterwards.
		foreignPreview := selectionActionReady(t, r.f, other, actionPreparationPath("movies", r.mustPrepareAs(t, other)))
		foreignPath := actionPreparationPath("movies", foreignPreview.PreparationID)
		stolen := r.f.request("POST", "/v1/libraries/movies/selection-actions", nil, selectionactions.Confirm{OperationID: "matrix-foreign-prep-action", PreparationID: foreignPreview.PreparationID, Confirmation: foreignPreview.Confirmation})
		sameAnswer(t, "another session's preparation", stolen, r.f.request("POST", "/v1/libraries/movies/selection-actions", nil, selectionactions.Confirm{OperationID: "matrix-absent-prep-action-2", PreparationID: absentPrep, Confirmation: "1"}))
		if stolen.Code != 409 || bodyErrorCode(stolen.Body.String()) != "stale_continuation" {
			t.Fatalf("another session's preparation %d %s", stolen.Code, stolen.Body.String())
		}
		if still := selectionActionAs(t, r.f, other, "GET", foreignPath, nil); still.Code != 200 {
			t.Fatalf("a stolen confirmation consumed another session's preparation: %d %s", still.Code, still.Body.String())
		}
		if released := selectionActionAs(t, r.f, other, "DELETE", foreignPath, nil); released.Code != 204 {
			t.Fatalf("foreign release %d %s", released.Code, released.Body.String())
		}

		// A library the profile is not given answers as one that does not exist.
		hidden := r.f.request("POST", "/v1/libraries/other/selection-actions", nil, selectionactions.Confirm{OperationID: "matrix-hidden-library-action", PreparationID: preview.PreparationID, Confirmation: preview.Confirmation})
		missing := r.f.request("POST", "/v1/libraries/no-such-library/selection-actions", nil, selectionactions.Confirm{OperationID: "matrix-missing-library-action", PreparationID: preview.PreparationID, Confirmation: preview.Confirmation})
		if hidden.Code != 404 || hidden.Code != missing.Code || bodyErrorCode(hidden.Body.String()) != bodyErrorCode(missing.Body.String()) {
			t.Errorf("confirmation in a hidden library differs from an absent one: %d %q / %d %q", hidden.Code, bodyErrorCode(hidden.Body.String()), missing.Code, bodyErrorCode(missing.Body.String()))
		}
		r.markerFree(t, "confirmation in a hidden library", hidden.Body.String())

		// The owner's own exact confirmation is accepted and starts the durable
		// command for the visible subset only.
		accepted := r.f.request("POST", "/v1/libraries/movies/selection-actions", nil, selectionactions.Confirm{OperationID: "matrix-restricted-selection", PreparationID: preview.PreparationID, Confirmation: preview.Confirmation})
		if accepted.Code != 202 {
			t.Fatalf("own confirmation %d %s", accepted.Code, accepted.Body.String())
		}
		var job selectionactions.Job
		if err := json.Unmarshal(accepted.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.Total != 3 || job.Action != "delete" || job.OperationID == "" {
			t.Fatalf("own confirmation %+v", job)
		}
		r.markerFree(t, "own confirmation", accepted.Body.String())
		operation := "/v1/selection-actions/" + job.OperationID

		// The confirmed operation is private to its session binding: another
		// session reads and releases it exactly like an absent operation.
		owned := r.f.request("GET", operation, nil, nil)
		if owned.Code != 200 {
			t.Fatalf("owned operation %d %s", owned.Code, owned.Body.String())
		}
		r.markerFree(t, "owned operation", owned.Body.String())
		foreignGet := selectionActionAs(t, r.f, other, "GET", operation, nil)
		absentGet := selectionActionAs(t, r.f, other, "GET", "/v1/selection-actions/"+absentOp, nil)
		if foreignGet.Code != 404 {
			t.Fatalf("another session's operation read %d %s", foreignGet.Code, foreignGet.Body.String())
		}
		sameAnswer(t, "another session's operation read", foreignGet, absentGet)
		foreignDelete := selectionActionAs(t, r.f, other, "DELETE", operation, nil)
		absentDelete := selectionActionAs(t, r.f, other, "DELETE", "/v1/selection-actions/"+absentOp, nil)
		if foreignDelete.Code != 404 {
			t.Fatalf("another session's operation cancel %d %s", foreignDelete.Code, foreignDelete.Body.String())
		}
		sameAnswer(t, "another session's operation cancel", foreignDelete, absentDelete)
		if alive := r.f.request("GET", operation, nil, nil); alive.Code != 200 {
			t.Fatalf("a foreign cancel stopped the owner's operation: %d %s", alive.Code, alive.Body.String())
		}

		// The owner's own read and release; the operation stays readable.
		if method == "DELETE" {
			if own := r.f.request("DELETE", operation, nil, nil); own.Code != 200 {
				t.Fatalf("owned operation cancel %d %s", own.Code, own.Body.String())
			}
		}
		if after := r.f.request("GET", operation, nil, nil); after.Code != 200 {
			t.Fatalf("operation vanished after cancellation %d %s", after.Code, after.Body.String())
		}
	}
}

// mustPrepareAs mints a real preparation as another session and returns its id.
func (r *restrictedSelectionActions) mustPrepareAs(t *testing.T, token string) string {
	t.Helper()
	pending, _ := r.f.prepare(t, nil)
	query := browseSelectionQueryPath("movies", pending.QueryID)
	descriptor, _ := selectionHTTPReady(t, r.f, query)
	remove := false
	input := selectionActionInput{
		Action:   "delete",
		Selector: catalog.JobSelector{Query: &descriptor.Selector.Query},
		Expected: catalog.JobExpected{CatalogRevision: descriptor.CatalogRevision},
		Options:  selectionactions.Options{DeleteFiles: &remove},
	}
	response := selectionActionAs(t, r.f, token, "POST", "/v1/libraries/movies/selection-actions/preparations", input)
	if response.Code != 202 {
		t.Fatalf("second session's preparation %d %s", response.Code, response.Body.String())
	}
	var pendingAction selectionactions.Pending
	if err := json.Unmarshal(response.Body.Bytes(), &pendingAction); err != nil {
		t.Fatal(err)
	}
	return pendingAction.PreparationID
}

func TestSelectionActionPrivateHandlesRespectRestriction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	t.Run("create preparation", selectionActionPreparationCreateRestrictionCheck)
	for _, method := range []string{"GET", "DELETE"} {
		t.Run("preparation "+method, selectionActionPreparationQueryRestrictionCheck(method))
	}
	t.Run("confirm", selectionActionConfirmOperationRestrictionCheck("POST"))
	t.Run("operation", selectionActionConfirmOperationRestrictionCheck("DELETE"))
}
