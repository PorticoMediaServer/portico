package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
)

// Routes that exist for tools outside Portico: the bounded title query and the
// "these paths changed" report. First-party screens do not use them.

// PathChange is one path a tool reports. Change says what the tool did; the
// scan looks at the folder either way and works out the rest.
type PathChange struct {
	Path string `json:"path"`
	// Change is "added", "changed" or "removed". It may be left out.
	Change string `json:"change,omitempty"`
}

type PathsChangedRequest struct {
	// Empty preserves ordinary reporting. strict refuses any broader work.
	Mode  string       `json:"mode,omitempty"`
	Paths []PathChange `json:"paths"`
}

func titleQueryError(err error) error {
	var field *catalog.ErrTitleQuery
	switch {
	case err == nil:
		return nil
	case errors.As(err, &field):
		return &apierror.Error{Code: "invalid_request", Fields: []apierror.Field{{Path: field.Field, Code: "invalid", Message: "This value can't be used here."}}}
	case errors.Is(err, catalog.ErrCursor):
		return &apierror.Error{Code: "invalid_cursor"}
	case errors.Is(err, catalog.ErrTitleLibrary), errors.Is(err, sql.ErrNoRows):
		return &apierror.Error{Code: "not_found"}
	case errors.Is(err, catalog.ErrStaleContinuation):
		return &apierror.Error{Code: "cursor_expired"}
	case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrNotVisible):
		return foundationAuthError(err)
	default:
		return &apierror.Error{Code: "internal", Cause: err}
	}
}

func registerPlatformRoutes(registry *apikit.Registry, d Dependencies) error {
	err := apikit.Register(registry, apikit.Route[struct{}, catalog.TitlePage]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneBrowsing, ID: "query_titles", Method: "GET", Path: "/v1/titles", Summary: "List titles of one kind in one library, or find a title by its provider id",
		// The library is a query parameter here, so the handler makes the
		// library and restriction checks itself, inside the query.
		Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200,
		Query:  []string{"library", "kind", "sort", "order", "yearFrom", "yearTo", "addedAfter", "addedBefore", "provider", "providerId", "cursor", "limit"}, QueryIntegers: emptyLimitQuery(catalog.TitleQueryDefaultLimit, catalog.TitleQueryMaximumLimit),
		Errors: []string{"unauthorized", "invalid_request", "invalid_cursor", "cursor_expired", "not_found", "internal"},
	}, Handler: func(_ context.Context, r *http.Request, _ struct{}) (catalog.TitlePage, error) {
		empty := catalog.TitlePage{Items: []catalog.TitleRow{}}
		if d.Catalog == nil {
			return empty, &apierror.Error{Code: "internal"}
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return empty, foundationAuthError(err)
		}
		query := r.URL.Query()
		for key, values := range query {
			if len(values) != 1 {
				return empty, titleQueryError(&catalog.ErrTitleQuery{Field: key})
			}
		}
		number := func(key string) (int, error) {
			raw := query.Get(key)
			if raw == "" {
				return 0, nil
			}
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || len(raw) > 6 {
				return 0, &catalog.ErrTitleQuery{Field: key}
			}
			return n, nil
		}
		q := catalog.TitleQuery{Library: query.Get("library"), Kind: query.Get("kind"), Sort: query.Get("sort"), Order: query.Get("order"), AddedAfter: query.Get("addedAfter"), AddedBefore: query.Get("addedBefore"), Provider: query.Get("provider"), ProviderID: query.Get("providerId"), Cursor: query.Get("cursor")}
		if q.Limit, err = number("limit"); err != nil {
			return empty, titleQueryError(err)
		}
		if q.YearFrom, err = number("yearFrom"); err != nil {
			return empty, titleQueryError(err)
		}
		if q.YearTo, err = number("yearTo"); err != nil {
			return empty, titleQueryError(err)
		}
		libraries, fence, err := d.homeScopeFor(r, p)
		if err != nil {
			return empty, titleQueryError(err)
		}
		viewer, err := d.catalogViewer(r, p, libraries, fence)
		if err != nil {
			return empty, titleQueryError(err)
		}
		var page catalog.TitlePage
		if err = d.compose(r, viewer.Restrictions, func(cat *catalog.Service) error {
			var e error
			page, e = cat.QueryTitles(viewer, q)
			return e
		}); err != nil {
			return empty, titleQueryError(err)
		}
		// What the caller may see is proved again after the read: a page cut
		// while their access was changing is not handed over.
		after, err := d.homeFenceAfter(r, p)
		if err != nil {
			return empty, titleQueryError(err)
		}
		if after != fence {
			return empty, &apierror.Error{Code: "cursor_expired"}
		}
		return page, nil
	}})
	if err != nil {
		return err
	}
	if err = apikit.Register(registry, apikit.Route[PathsChangedRequest, ingestion.PathReport]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneDefault, ID: "report_changed_paths", Method: "POST", Path: "/v1/libraries/paths-changed", Summary: "Report paths that were added, changed or removed, and scan only those folders",
		Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: 1 << 20, Status: 202,
		Errors: []string{"unauthorized", "not_permitted", "invalid_request", "body_too_large", "administration_unavailable", "internal"},
	}, Handler: func(ctx context.Context, r *http.Request, body PathsChangedRequest) (ingestion.PathReport, error) {
		empty := ingestion.PathReport{Paths: []ingestion.ReportedPath{}, Jobs: []ingestion.Job{}}
		if d.Ingestion == nil {
			return empty, &apierror.Error{Code: "administration_unavailable"}
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return empty, foundationAuthError(err)
		}
		if len(body.Paths) == 0 || len(body.Paths) > ingestion.MaxReportedPaths {
			return empty, &apierror.Error{Code: "invalid_request", Fields: []apierror.Field{{Path: "paths", Code: "invalid", Message: "Send between 1 and 200 paths."}}}
		}
		if body.Mode != "" && body.Mode != "strict" {
			return empty, &apierror.Error{Code: "invalid_request", Fields: []apierror.Field{{Path: "mode", Code: "invalid", Message: "Use strict or omit mode."}}}
		}
		paths := make([]string, 0, len(body.Paths))
		for _, change := range body.Paths {
			switch change.Change {
			case "", "added", "changed", "removed":
			default:
				return empty, &apierror.Error{Code: "invalid_request", Fields: []apierror.Field{{Path: "paths.change", Code: "invalid", Message: "Use added, changed or removed."}}}
			}
			paths = append(paths, change.Path)
		}
		var out ingestion.PathReport
		if body.Mode == "strict" {
			out, err = d.Ingestion.ReportPathsStrict(ctx, paths, func(tx *sql.Tx, library string) error { return d.resourceAuthorize(tx, p, library) })
		} else {
			out, err = d.Ingestion.ReportPaths(ctx, paths, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		}
		var refusal *ingestion.StrictPathRefusal
		if errors.As(err, &refusal) {
			if refusal.Retryable() {
				// Reuse the native retryable 503 catalogue entry; do not label
				// active work or a notice-mode boundary as invalid input.
				return empty, &apierror.Error{Code: "administration_unavailable", Reason: string(refusal.Reason), RetryAfterSeconds: 2, Cause: err}
			}
			return empty, &apierror.Error{Code: "invalid_request", Reason: string(refusal.Reason), Fields: []apierror.Field{{Path: "paths", Code: string(refusal.Reason), Message: "No paths from this request were queued. Report a specific stable folder."}}}
		}
		switch {
		case err == nil:
			return out, nil
		case errors.Is(err, ingestion.ErrPathReport):
			return empty, &apierror.Error{Code: "invalid_request"}
		case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, identity.ErrForbidden):
			return empty, foundationAuthError(err)
		default:
			return empty, &apierror.Error{Code: "internal", Cause: err}
		}
	}}); err != nil {
		return err
	}
	return registerIntegrationRefreshRoute(registry, d)
}
