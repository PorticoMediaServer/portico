package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const readingFacetQueryPath = "/v1/libraries/{id}/reading-facet-queries/{queryId}"

type ReadingFacetPreparing struct {
	Kind         string `json:"kind"`
	State        string `json:"state"`
	QueryID      string `json:"queryId"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

func (ReadingFacetPreparing) JSONSchema() map[string]any {
	type wire ReadingFacetPreparing
	s, _ := apikit.Schema(reflect.TypeFor[wire]())
	s["additionalProperties"] = false
	p := s["properties"].(map[string]any)
	p["kind"].(map[string]any)["enum"] = []string{"reading-facet-preparing"}
	p["state"].(map[string]any)["enum"] = []string{"preparing"}
	p["queryId"].(map[string]any)["minLength"] = 1
	p["retryAfterMs"].(map[string]any)["minimum"] = readingInitialRetryMs
	return s
}

// Status-specific schemas preserve the existing FacetPage at 200 exactly.
type ReadingFacetQueryResponse struct{}

func (ReadingFacetQueryResponse) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		return facetPageSchema()
	}
	if status == 202 {
		return withVariantTag((ReadingFacetPreparing{}).JSONSchema(), "reading-preparing")
	}
	return nil
}
func (v ReadingFacetQueryResponse) JSONSchema() map[string]any {
	return map[string]any{"anyOf": []any{v.JSONSchemaForStatus(200), v.JSONSchemaForStatus(202)}}
}

// Parent adds this registration to FoundationRegistry with its cohort. Keeping
// this separate avoids competing edits to the SIMPLE facet route writer.
func registerReadingFacetQueryRoutes(registry *apikit.Registry, d Dependencies) error {
	m := apikit.Metadata{Audience: apikit.Clients, ID: "get_library_reading_facet_query", Method: "GET", Path: readingFacetQueryPath, Summary: "Read a prepared Reading facet page", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, SuccessStatuses: []int{202}, Errors: []string{"invalid_request", "invalid_cursor", "unauthorized", "not_permitted", "not_found", "stale_continuation", "search_busy", "internal"}, ResponseHeaders: []apikit.ResponseHeader{
		{Status: 202, Name: "Location", Description: "Same-library poll path", Required: true},
		{Status: 202, Name: "Retry-After", Description: "Minimum delay before polling, in seconds", Required: true},
	}}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[struct{}, ReadingFacetQueryResponse]{Metadata: m, Serve: d.readingFacetQuery}); err != nil {
		return err
	}
	m.ID, m.Method, m.Summary, m.Status = "delete_library_reading_facet_query", "DELETE", "Release this caller's Reading facet interest", 204
	m.SuccessStatuses, m.ResponseHeaders = nil, nil
	m.NoResponseBody = true
	return apikit.RegisterRaw(registry, apikit.RawRoute[struct{}, struct{}]{Metadata: m, Serve: d.deleteReadingFacetQuery})
}

// Only immutable identity/scope and the minimal live-authority dependencies are
// retained by a worker. In particular d has no Catalog, Reading, request context,
// telemetry context or transaction. The callback always uses the supplied turn.
type readingFacetAuthority struct {
	d           Dependencies
	principal   identity.Principal
	library     string
	expected    readprep.Caller
	credential  identity.APICredential
	credentials *access.Store
}

func (a *readingFacetAuthority) current(ctx context.Context) (catalog.Viewer, readprep.Caller, error) {
	var empty catalog.Viewer
	tx := dbwork.Snapshot(ctx)
	if tx == nil || a.d.Identity == nil {
		return empty, "", identity.ErrUnauthorized
	}
	p, err := a.d.Identity.PrincipalByDigestTx(ctx, tx, a.principal.Hash)
	if err != nil {
		return empty, "", err
	}
	family, err := a.d.Identity.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return empty, "", err
	}
	// PrincipalByDigestTx validates the family but omits accounting DeviceID;
	// reconstruct it through the same seam used by ordinary authentication.
	p.DeviceID = family.DeviceID
	// Session replacement, epoch/profile/role/grant and expiry changes cannot
	// inherit an older handle, even when the effective content policy is equal.
	if p != a.principal {
		return empty, "", identity.ErrUnauthorized
	}
	if a.credential.ID != "" {
		live, err := a.credentials.ResolveCredential(ctx, a.credential.Digest)
		if err != nil {
			return empty, "", err
		}
		if live.ID != a.credential.ID || live.Kind != a.credential.Kind || live.AccountID != a.credential.AccountID || !slices.Equal(live.Scopes, a.credential.Scopes) {
			return empty, "", catalog.ErrStaleContinuation
		}
		if !apikit.ScopeAllows(credentialScopes(live), apikit.ScopeRead) {
			return empty, "", identity.ErrForbidden
		}
	}
	if err = p.PermitsRequest(ctx); err != nil {
		return empty, "", err
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, a.library).Scan(&kind); err != nil {
		return empty, "", err
	}
	if kind != "book" && kind != "comic" {
		return empty, "", sql.ErrNoRows
	}
	viewer, err := a.d.readingBrowseViewer(ctx, p, a.library, empty)
	if err != nil {
		return empty, "", err
	}
	encoded, err := json.Marshal(struct {
		Domain, Library        string
		Principal              identity.Principal
		ExpiresAt              time.Time
		DeviceID, Grant, Fence string
		Policy                 identity.ContentRestrictions
		Credential             identity.APICredential
	}{"reading-facet-v1", a.library, p, p.ExpiresAt, p.DeviceID, p.Grant, viewer.Fence, viewer.EffectiveRestrictions(), a.credential})
	if err != nil {
		return empty, "", err
	}
	caller := readprep.Caller(encoded)
	if a.expected != "" && a.expected != caller {
		return empty, "", catalog.ErrStaleContinuation
	}
	return viewer, caller, nil
}

func (d Dependencies) readingFacetAuthority(r *http.Request) (*readingFacetAuthority, error) {
	p, err := d.principal(r)
	if err != nil {
		return nil, err
	}
	a := &readingFacetAuthority{d: Dependencies{DB: d.DB, Identity: d.Identity, Hosted: d.Hosted, directSchema: d.directSchema}, principal: p, library: r.PathValue("id")}
	if credential, ok := identity.APICredentialFrom(r.Context()); ok {
		credential.Scopes = slices.Clone(credential.Scopes)
		a.credential, a.credentials = credential, d.Access.Access
	}
	err = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		_, caller, err := a.current(ctx)
		a.expected = caller
		return err
	})
	return a, err
}

func (d Dependencies) checkReadingFacetResult(r *http.Request, a *readingFacetAuthority, result readprep.Result) error {
	return dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		viewer, _, err := a.current(ctx)
		if err != nil {
			return err
		}
		basis, err := d.Catalog.WithContext(ctx).ReadingFacetBasis(viewer, a.library)
		if err == nil && result.Basis != "" && basis != result.Basis {
			return catalog.ErrReadingProjectionChanged
		}
		return err
	})
}

func readingFacetQueryFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, readprep.ErrStale) || errors.Is(err, readprep.ErrClosed) || errors.Is(err, catalog.ErrStaleContinuation) {
		// revision_mismatch is 412 in the existing catalogue; preparations use
		// its existing 409 reload grammar, as agreed with the client foundation.
		err = catalog.ErrStaleContinuation
	}
	if errors.Is(err, readprep.ErrCapacity) || errors.Is(err, readprep.ErrUncached) {
		w.Header().Set("Retry-After", "1")
		policyError(w, "search_busy")
		return
	}
	browseFailure(w, err)
}

func readingFacetPollLocation(library string, handle readprep.Handle) string {
	return "/v1/libraries/" + url.PathEscape(library) + "/reading-facet-queries/" + url.PathEscape(string(handle))
}

func writeReadingFacetQuery(w http.ResponseWriter, library string, handle readprep.Handle, result readprep.Result, retryMs int) error {
	if result.State == readprep.Ready {
		// Payload is already exact encoded JSON; no DTO is hydrated here. Hold
		// additional bounded transport/header/copy space until Release too.
		if err := result.ResizeDelivery(8192 + 2*int64(len(result.Payload))); err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(result.Payload)
		return nil
	}
	location := readingFacetPollLocation(library, handle)
	if err := result.ResizeDelivery(int64(4096 + 4*len(location))); err != nil {
		return err
	}
	w.Header().Set("Location", location)
	retrySeconds := "1"
	if retryMs == readingInitialRetryMs {
		retrySeconds = "0" // HTTP delta-seconds; the body carries the 250ms floor.
	}
	w.Header().Set("Retry-After", retrySeconds)
	write(w, 202, variantBody{"reading-preparing", ReadingFacetPreparing{"reading-facet-preparing", "preparing", string(handle), retryMs}})
	return nil
}

func (d Dependencies) readingFacetQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if len(r.URL.Query()) != 0 || r.PathValue("queryId") == "" {
		failure(w, catalog.ErrCursor)
		return
	}
	a, err := d.readingFacetAuthority(r)
	if err != nil {
		readingFacetQueryFailure(w, err)
		return
	}
	store := d.Catalog.ReadPreparations()
	if store == nil {
		readingFacetQueryFailure(w, readprep.ErrStale)
		return
	}
	handle := readprep.Handle(r.PathValue("queryId"))
	result, pollErr := store.Poll(r.Context(), a.expected, handle)
	defer result.Release() // survives every serialization/transport exit
	if err = d.checkReadingFacetResult(r, a, result); err != nil {
		_ = store.Cancel(r.Context(), a.expected, handle)
		readingFacetQueryFailure(w, err)
		return
	}
	if pollErr != nil {
		readingFacetQueryFailure(w, pollErr)
		return
	}
	if err := writeReadingFacetQuery(w, a.library, handle, result, readingBrowseRetryMs); err != nil {
		readingFacetQueryFailure(w, err)
	}
}

func (d Dependencies) deleteReadingFacetQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if len(r.URL.Query()) != 0 || r.PathValue("queryId") == "" {
		failure(w, catalog.ErrCursor)
		return
	}
	a, err := d.readingFacetAuthority(r)
	if err != nil {
		readingFacetQueryFailure(w, err)
		return
	}
	store := d.Catalog.ReadPreparations()
	if store == nil {
		readingFacetQueryFailure(w, readprep.ErrStale)
		return
	}
	err = store.Cancel(r.Context(), a.expected, readprep.Handle(r.PathValue("queryId")))
	if after := d.checkReadingFacetResult(r, a, readprep.Result{}); after != nil {
		err = after
	}
	if err != nil {
		readingFacetQueryFailure(w, err)
		return
	}
	w.WriteHeader(204)
}

// Parent calls this only after the complete client cohort can consume 202.
// The existing handler's validated request enters here; no public sort is added.
// Foreground facetsHandler is deliberately unchanged in this implementation.
func (d Dependencies) prepareReadingFacets(w http.ResponseWriter, r *http.Request, request catalog.FacetRequest) {
	w.Header().Set("Cache-Control", "no-store")
	a, err := d.readingFacetAuthority(r)
	if err != nil {
		readingFacetQueryFailure(w, err)
		return
	}
	store := d.Catalog.ReadPreparations()
	if store == nil {
		readingFacetQueryFailure(w, readprep.ErrCapacity)
		return
	}
	request.Library = a.library
	var key readprep.Key
	var plan readprep.Plan
	access := catalog.ReadingFacetAccess{RetainedBytes: int64(unsafe.Sizeof(*a)) + int64(3*len(a.expected)+len(a.library)) + 4096, Check: func(ctx context.Context) (catalog.Viewer, error) {
		viewer, _, err := a.current(ctx)
		return viewer, err
	}}
	instant := time.Now()
	if d.Catalog.Clock != nil {
		instant = d.Catalog.Clock()
	}
	err = dbwork.WithReadSnapshot(r.Context(), d.DB, func(ctx context.Context) error {
		viewer, _, err := a.current(ctx)
		if err != nil {
			return err
		}
		key, plan, err = d.Catalog.WithContext(ctx).PlanReadingFacets(viewer, request, instant, access)
		return err
	})
	if err != nil {
		readingFacetQueryFailure(w, err)
		return
	}
	handle, submitErr := store.Submit(r.Context(), a.expected, key, plan)
	defer func() {
		if r.Context().Err() != nil {
			_ = store.Cancel(context.WithoutCancel(r.Context()), a.expected, handle)
		}
	}()
	// Live authority/witness are checked even if admission fails.
	if err = d.checkReadingFacetResult(r, a, readprep.Result{Basis: key.Basis}); err != nil {
		if handle != "" {
			_ = store.Cancel(r.Context(), a.expected, handle)
		}
		readingFacetQueryFailure(w, err)
		return
	}
	if submitErr != nil {
		readingFacetQueryFailure(w, submitErr)
		return
	}
	result, pollErr := store.PollWhenReady(r.Context(), a.expected, handle, readprep.InitialReadyGrace)
	defer result.Release()
	if err = d.checkReadingFacetResult(r, a, result); err != nil {
		_ = store.Cancel(r.Context(), a.expected, handle)
		readingFacetQueryFailure(w, err)
		return
	}
	if pollErr != nil {
		_ = store.Cancel(r.Context(), a.expected, handle)
		readingFacetQueryFailure(w, pollErr)
		return
	}
	if result.State == readprep.Ready {
		// Direct 200 has no queryId in the original FacetPage DTO, so no client
		// can release that private lease. Completed content remains deduplicable.
		defer store.Cancel(r.Context(), a.expected, handle)
	}
	if err := writeReadingFacetQuery(w, a.library, handle, result, readingInitialRetryMs); err != nil {
		_ = store.Cancel(r.Context(), a.expected, handle)
		readingFacetQueryFailure(w, err)
	}
}
