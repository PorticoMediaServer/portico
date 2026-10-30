package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

type IntegrationLibrary struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Kind  string   `json:"kind"`
	Paths []string `json:"paths"`
}
type IntegrationLibraries struct {
	Items      []IntegrationLibrary `json:"items"`
	NextCursor string               `json:"nextCursor,omitempty"`
}
type IntegrationUser struct {
	ProfileName string `json:"profileName"`
	ProfileID   string `json:"profileId"`
	AccountID   string `json:"accountId"`
	Username    string `json:"username"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
}
type IntegrationUsers struct {
	Items      []IntegrationUser `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// A common read-only projection of native dashboard and own-account controls.
// It carries no delivery URLs, queue, global totals, history or stream quota.
type IntegrationSessionItem struct {
	Episode *catalog.EpisodeInfo `json:"episode,omitempty"`
	ID      string               `json:"id"`
	Title   string               `json:"title"`
	Kind    string               `json:"kind,omitempty"`
}

type IntegrationSession struct {
	Actions    []string                `json:"actions"`
	ID         string                  `json:"id"`
	State      string                  `json:"state"`
	Kind       string                  `json:"kind"`
	User       *playbackv1.AdminName   `json:"user,omitempty"`
	Profile    *playbackv1.AdminName   `json:"profile,omitempty"`
	Device     playbackv1.AdminName    `json:"device"`
	Item       *IntegrationSessionItem `json:"item,omitempty"`
	PositionMs int64                   `json:"positionMs"`
	DurationMs int64                   `json:"durationMs,omitempty"`
	StartedAt  string                  `json:"startedAt,omitempty"`
	Location   string                  `json:"location,omitempty"`
	App        string                  `json:"app,omitempty"`
	Platform   string                  `json:"platform,omitempty"`
	Revision   string                  `json:"revision,omitempty"`
}
type IntegrationSessions struct {
	Items      []IntegrationSession `json:"items"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

func integrationReadMetadata(id, path, summary string, query ...string) apikit.Metadata {
	defaultLimit := 50
	switch id {
	case "integration_libraries", "integration_users":
		defaultLimit = 100
	case "integration_media_info":
		defaultLimit = 16
	}

	return apikit.Metadata{ID: id, Method: "GET", Path: path, Summary: summary,
		Audience: apikit.Integrations, Access: apikit.Device, Requires: apikit.ScopeRead,
		Admission: laneBrowsing, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Query: query, QueryIntegers: emptyLimitQuery(defaultLimit, catalog.IntegrationReadMaximumLimit),
		Errors: []string{"unauthorized", "not_permitted", "insufficient_scope", "session_required", "invalid_request", "invalid_cursor", "cursor_expired", "stale_continuation", "not_found", "internal"}}
}

// registerIntegrationReads declares the native bounded automation vocabulary.
// All reads retain the native scope, admission and live authority checks.
func registerIntegrationReads(registry *apikit.Registry, d Dependencies) error {
	if err := apikit.Register(registry, apikit.Route[struct{}, IntegrationLibraries]{Metadata: integrationReadMetadata("integration_libraries", "/v1/integrations/libraries", "Read authorized native libraries and owner-authorized source paths", "cursor", "limit"), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (IntegrationLibraries, error) {
		return d.integrationLibraries(r.WithContext(ctx))
	}}); err != nil {
		return err
	}
	query := []string{"ids", "libraryId", "libraryKind", "kind", "parentId", "provider", "providerId", "year", "search", "sort", "order", "addedAfter", "addedBefore", "includeFiles", "hasPaths", "cursor", "limit"}
	if err := apikit.Register(registry, apikit.Route[struct{}, catalog.IntegrationItemPage]{Metadata: integrationReadMetadata("integration_items", "/v1/integrations/items", "Read bounded native catalog facts with exact visible totals", query...), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (catalog.IntegrationItemPage, error) {
		return d.integrationItems(r.WithContext(ctx), "")
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, IntegrationUsers]{Metadata: integrationReadMetadata("integration_users", "/v1/integrations/users", "Read permitted native user and profile identities", "cursor", "limit"), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (IntegrationUsers, error) {
		return d.integrationUsers(r.WithContext(ctx))
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, IntegrationSessions]{Metadata: integrationReadMetadata("integration_sessions", "/v1/integrations/sessions", "Read native dashboard sessions or own-account controls", "cursor", "limit"), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (IntegrationSessions, error) {
		return d.integrationSessions(r.WithContext(ctx))
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, catalog.IntegrationMediaInfo]{Metadata: integrationReadMetadata("integration_media_info", "/v1/integrations/items/{id}/media-info", "Read paged stored media facts without delivery negotiation", "cursor", "limit"), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (catalog.IntegrationMediaInfo, error) {
		out := catalog.IntegrationMediaInfo{MediaSources: []catalog.IntegrationMediaSource{}}
		if err := homeAllowedQuery(r, "cursor", "limit"); err != nil {
			return out, integrationReadError(err)
		}
		limit, err := integrationReadLimit(r, 16)
		if err != nil {
			return out, err
		}
		err = d.integrationCatalogRead(r.WithContext(ctx), func(_ context.Context, _ identity.Principal, cat *catalog.Service, v catalog.Viewer, paths bool) error {
			var e error
			out, e = cat.IntegrationMediaInfo(v, r.PathValue("id"), r.URL.Query().Get("cursor"), limit, paths)
			return e
		})
		if err != nil {
			return catalog.IntegrationMediaInfo{}, integrationReadError(err)
		}
		return out, nil
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, catalog.IntegrationItemPage]{Metadata: integrationReadMetadata("integration_show_seasons", "/v1/integrations/shows/{id}/seasons", "Read a bounded native season hierarchy", "includeFiles", "hasPaths", "cursor", "limit"), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (catalog.IntegrationItemPage, error) {
		return d.integrationItems(r.WithContext(ctx), "season")
	}}); err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[struct{}, catalog.IntegrationItemPage]{Metadata: integrationReadMetadata("integration_show_episodes", "/v1/integrations/shows/{id}/episodes", "Read bounded native episodes across all seasons, or resolve one real season", "season", "seasonId", "year", "search", "provider", "providerId", "includeFiles", "sort", "order", "addedAfter", "addedBefore", "cursor", "limit"), Handler: func(ctx context.Context, r *http.Request, _ struct{}) (catalog.IntegrationItemPage, error) {
		return d.integrationItems(r.WithContext(ctx), "episode")
	}})
}

func integrationReadError(err error) error {
	if err == nil {
		return nil
	}
	var wire *apierror.Error
	if errors.As(err, &wire) {
		return wire
	}
	if errors.Is(err, catalog.ErrStaleContinuation) {
		return &apierror.Error{Code: "stale_continuation"}
	}
	return titleQueryError(err)
}
func integrationReadLimit(r *http.Request, fallback int) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > catalog.IntegrationReadMaximumLimit || len(raw) > 3 {
		return 0, &apierror.Error{Code: "invalid_request"}
	}
	return n, nil
}

// Every read captures the real native caller, family, restrictions and library
// rights inside its snapshot, then proves them again after releasing it. Owner
// path authority is captured and rechecked independently of general read scope.
func (d Dependencies) integrationCatalogRead(r *http.Request, read func(context.Context, identity.Principal, *catalog.Service, catalog.Viewer, bool) error) error {
	if d.Catalog == nil || d.DB == nil || d.Identity == nil {
		return &apierror.Error{Code: "internal"}
	}
	if err := registryScopeGate(r, apikit.ScopeRead); err != nil {
		return err
	}
	p, err := d.foundationCaller(r)
	if err != nil {
		return err
	}
	var fence string
	var paths bool
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	err = dbwork.WithReadSnapshot(ctx, d.DB, func(snapshot context.Context) error {
		tx := dbwork.Snapshot(snapshot)
		if _, e := d.Identity.SessionFamilyTx(snapshot, tx, p); e != nil {
			return e
		}
		request := r.WithContext(snapshot)
		libraries, f, e := d.homeScopeFor(request, p)
		if e != nil {
			return e
		}
		fence = f
		v, e := d.catalogViewer(request, p, libraries, fence)
		if e != nil {
			return e
		}
		if p.Role == identity.TierOwner {
			if e = d.ownerAuthorityTx(snapshot, tx, p); e == nil {
				paths = true
			} else if !errors.Is(e, identity.ErrForbidden) {
				return e
			}
		}
		return read(snapshot, p, d.Catalog.WithContext(snapshot), v, paths)
	})
	if err != nil {
		return err
	}
	fresh, err := d.strictPrincipalFor(r)
	if err != nil {
		return err
	}
	if presentationIdentity(p) != presentationIdentity(fresh) || p.Epoch != fresh.Epoch {
		return catalog.ErrStaleContinuation
	}
	after, err := d.homeFenceAfter(r, fresh)
	if err != nil {
		return err
	}
	if after != fence {
		return catalog.ErrStaleContinuation
	}
	if paths {
		err = dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassSecurityFence), d.DB, func(check context.Context) error { return d.ownerAuthorityTx(check, dbwork.Snapshot(check), fresh) })
	}
	return err
}

func (d Dependencies) integrationItems(r *http.Request, hierarchy string) (catalog.IntegrationItemPage, error) {
	out := catalog.IntegrationItemPage{Items: []catalog.IntegrationItem{}}
	allowed := []string{"ids", "libraryId", "libraryKind", "kind", "parentId", "provider", "providerId", "year", "search", "sort", "order", "addedAfter", "addedBefore", "includeFiles", "hasPaths", "cursor", "limit"}
	if hierarchy == "season" {
		allowed = []string{"includeFiles", "hasPaths", "cursor", "limit"}
	}
	if hierarchy == "episode" {
		allowed = []string{"season", "seasonId", "year", "search", "provider", "providerId", "includeFiles", "sort", "order", "addedAfter", "addedBefore", "cursor", "limit"}
	}
	if err := homeAllowedQuery(r, allowed...); err != nil {
		return out, integrationReadError(err)
	}
	values := r.URL.Query()
	q := catalog.IntegrationItemQuery{LibraryID: values.Get("libraryId"), LibraryKind: values.Get("libraryKind"), Kind: values.Get("kind"), ParentID: values.Get("parentId"), Provider: values.Get("provider"), ProviderID: values.Get("providerId"), Search: values.Get("search"), Cursor: values.Get("cursor")}
	q.Sort = values.Get("sort")
	q.Order = values.Get("order")
	q.AddedAfter = values.Get("addedAfter")
	q.AddedBefore = values.Get("addedBefore")
	if raw := values.Get("includeFiles"); raw != "" {
		if raw != "true" && raw != "false" {
			return out, &apierror.Error{Code: "invalid_request"}
		}
		q.IncludeFiles = raw == "true"
	}
	if raw := values.Get("hasPaths"); raw != "" {
		if raw != "true" && raw != "false" {
			return out, &apierror.Error{Code: "invalid_request"}
		}
		q.HasPaths = raw == "true"
	}
	var err error
	q.Limit, err = integrationReadLimit(r, 50)
	if err != nil {
		return out, err
	}
	q.IDs, err = catalog.IntegrationIDs(values.Get("ids"))
	if err != nil {
		return out, integrationReadError(err)
	}
	if raw := values.Get("year"); raw != "" {
		q.Year, err = strconv.Atoi(raw)
		if err != nil || len(raw) > 4 || q.Year < 1 || q.Year > 9999 {
			return out, &apierror.Error{Code: "invalid_request"}
		}
	}
	if hierarchy != "" {
		q.ParentID = r.PathValue("id")
		q.Kind = hierarchy
	}
	if raw := values.Get("season"); raw != "" {
		number, e := strconv.Atoi(raw)
		if e != nil || number < 0 || len(raw) > 6 || values.Get("seasonId") != "" {
			return out, &apierror.Error{Code: "invalid_request"}
		}
		q.SeasonNumber = &number
	}
	err = d.integrationCatalogRead(r, func(ctx context.Context, _ identity.Principal, cat *catalog.Service, v catalog.Viewer, paths bool) error {
		if hierarchy != "" {
			if e := cat.VisibleEntity(ctx, v, "show", r.PathValue("id")); e != nil {
				return e
			}
		}
		if season := values.Get("seasonId"); season != "" {
			// Validate the season's actual show before replacing the parent selector.
			tx := dbwork.Snapshot(ctx)
			var show string
			if e := tx.QueryRowContext(ctx, `SELECT pid(sh.public_id) FROM catalog_seasons sd JOIN catalog_entities se ON se.id=sd.entity_id JOIN catalog_entities sh ON sh.id=sd.show_id WHERE se.public_id=pid_blob(?) AND se.retired=0`, season).Scan(&show); e != nil {
				return e
			}
			if show != r.PathValue("id") {
				return sql.ErrNoRows
			}
			q.ParentID = season
		}
		var e error
		out, e = cat.QueryIntegrationItems(v, q, paths)
		return e
	})
	if err != nil {
		return catalog.IntegrationItemPage{}, integrationReadError(err)
	}
	return out, nil
}

// Directory cursors are bounded, untrusted seek positions, never grants. Every
// page reapplies native authority and scope. No account/profile is inferred from
// a cursor; ordinary users remain bound to p.AccountID and p.ProfileID.
func integrationDirectoryAfter(r *http.Request) (string, error) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return "", nil
	}
	if len(raw) > 256 {
		return "", &apierror.Error{Code: "invalid_cursor"}
	}
	value, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(value) < 1 || len(value) > 128 {
		return "", &apierror.Error{Code: "invalid_cursor"}
	}
	return string(value), nil
}
func integrationDirectoryCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func (d Dependencies) integrationLibraries(r *http.Request) (IntegrationLibraries, error) {
	out := IntegrationLibraries{Items: []IntegrationLibrary{}}
	if err := homeAllowedQuery(r, "cursor", "limit"); err != nil {
		return out, integrationReadError(err)
	}
	limit, err := integrationReadLimit(r, 100)
	if err != nil {
		return out, err
	}
	after, err := integrationDirectoryAfter(r)
	if err != nil {
		return out, err
	}
	err = d.integrationCatalogRead(r, func(ctx context.Context, _ identity.Principal, _ *catalog.Service, v catalog.Viewer, paths bool) error {
		raw, _ := json.Marshal(v.Libraries)
		tx := dbwork.Snapshot(ctx)
		rows, e := tx.QueryContext(ctx, `SELECT l.id,l.name,l.kind FROM libraries l JOIN catalog_libraries cl ON cl.library_id=l.id WHERE cl.retired=0 AND l.id IN(SELECT value FROM json_each(?)) AND l.id>? ORDER BY l.id LIMIT ?`, string(raw), after, limit+1)
		if e != nil {
			return e
		}
		for rows.Next() {
			item := IntegrationLibrary{Paths: []string{}}
			if e = rows.Scan(&item.ID, &item.Name, &item.Kind); e != nil {
				rows.Close()
				return e
			}
			out.Items = append(out.Items, item)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			out.NextCursor = integrationDirectoryCursor(out.Items[limit-1].ID)
		}
		if !paths || len(out.Items) == 0 {
			return nil
		}
		// Native LibrarySources is capped at 64 sources per library. Keep the
		// entire authorized page's source read bounded and fail on corrupt excess.
		ids := []string{}
		positions := map[string]int{}
		for i, item := range out.Items {
			ids = append(ids, item.ID)
			positions[item.ID] = i
		}
		raw, _ = json.Marshal(ids)
		rows, e = tx.QueryContext(ctx, `SELECT library_id,configured_root FROM library_sources WHERE library_id IN(SELECT value FROM json_each(?)) AND enabled=1 ORDER BY library_id,id LIMIT ?`, string(raw), limit*64+1)
		if e != nil {
			return e
		}
		count := 0
		for rows.Next() {
			count++
			if count > limit*64 {
				rows.Close()
				return &apierror.Error{Code: "internal"}
			}
			var id, path string
			if e = rows.Scan(&id, &path); e != nil {
				rows.Close()
				return e
			}
			at := positions[id]
			if len(out.Items[at].Paths) >= 64 {
				rows.Close()
				return &apierror.Error{Code: "internal"}
			}
			out.Items[at].Paths = append(out.Items[at].Paths, path)
		}
		e = rows.Err()
		rows.Close()
		return e
	})
	if err != nil {
		return IntegrationLibraries{}, integrationReadError(err)
	}
	return out, nil
}

// A native admin route supplies the live tier/main-profile/admin-scope proof.
// A read-only key minted by an owner does not inherit its account's admin grant.
func (d Dependencies) integrationAdmin(r *http.Request, area string) (bool, error) {
	if err := registryScopeGate(r, apikit.ScopeAdmin); err != nil {
		var wire *apierror.Error
		if errors.As(err, &wire) && wire.Code == "insufficient_scope" {
			return false, nil
		}
		return false, err
	}
	_, err := d.v1Authorize(r.Context(), r, apikit.Metadata{Audience: apikit.Integrations, Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: area})
	if err == nil {
		return true, nil
	}
	var wire *apierror.Error
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrNotVisible) || errors.As(err, &wire) && (wire.Code == "not_permitted" || wire.Code == "not_found") {
		return false, nil
	}
	return false, err
}
func (d Dependencies) integrationUsers(r *http.Request) (IntegrationUsers, error) {
	out := IntegrationUsers{Items: []IntegrationUser{}}
	if err := homeAllowedQuery(r, "cursor", "limit"); err != nil {
		return out, integrationReadError(err)
	}
	limit, err := integrationReadLimit(r, 100)
	if err != nil {
		return out, err
	}
	after, err := integrationDirectoryAfter(r)
	if err != nil {
		return out, err
	}
	p, err := d.foundationCaller(r)
	if err != nil {
		return out, integrationReadError(err)
	}
	admin, err := d.integrationAdmin(r, "members")
	if err != nil {
		return out, integrationReadError(err)
	}
	err = d.integrationCatalogRead(r, func(ctx context.Context, _ identity.Principal, _ *catalog.Service, _ catalog.Viewer, _ bool) error {
		tx := dbwork.Snapshot(ctx)
		// Recheck admin membership in the same snapshot as the rows, after the
		// native area/scope gate. Ordinary keys select only their exact bound pair.
		if admin {
			if _, e := d.tierAuthorityTx(ctx, tx, p, identity.TierAdmin); e != nil {
				return e
			}
		}
		rows, e := tx.QueryContext(ctx, `SELECT p.id,p.name,a.id,a.username,CASE WHEN p.is_primary=1 AND a.profile_id=p.id THEN m.role ELSE 'member' END,m.disabled FROM direct_profiles p JOIN accounts a ON a.id=p.account_id JOIN direct_memberships m ON m.account_id=a.id WHERE p.deleted=0 AND (? OR (a.id=? AND p.id=?)) AND p.id>? ORDER BY p.id LIMIT ?`, admin, p.AccountID, p.ProfileID, after, limit+1)
		if e != nil {
			return e
		}
		for rows.Next() {
			var item IntegrationUser
			if e = rows.Scan(&item.ProfileID, &item.ProfileName, &item.AccountID, &item.Username, &item.Role, &item.Disabled); e != nil {
				rows.Close()
				return e
			}
			if item.ProfileID == p.ProfileID && identity.Grants(item.Role, p.Role) {
				item.Role = p.Role
			}
			out.Items = append(out.Items, item)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			out.NextCursor = integrationDirectoryCursor(out.Items[limit-1].ProfileID)
		}
		return nil
	})
	if err == nil && admin {
		var still bool
		still, err = d.integrationAdmin(r, "members")
		if err == nil && !still {
			err = identity.ErrForbidden
		}
	}
	if err != nil {
		return IntegrationUsers{}, integrationReadError(err)
	}
	return out, nil
}

// In-process router calls reuse native admission, actual credential scope,
// captured/rechecked authority and item redaction. No HTTP network is involved.
func integrationReadNative[T any](d Dependencies, r *http.Request, path string, query url.Values) (T, error) {
	var out T
	status, _, raw, err := d.mcpInvoke(r)(mcpCall{Method: "GET", Path: path, Query: query})
	if err != nil {
		return out, err
	}
	if status < 200 || status >= 300 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &failure) != nil || failure.Error.Code == "" {
			return out, &apierror.Error{Code: "internal"}
		}
		return out, &apierror.Error{Code: failure.Error.Code}
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}
func (d Dependencies) integrationSessions(r *http.Request) (IntegrationSessions, error) {
	out := IntegrationSessions{Items: []IntegrationSession{}}
	if err := homeAllowedQuery(r, "cursor", "limit"); err != nil {
		return out, integrationReadError(err)
	}
	limit, err := integrationReadLimit(r, 50)
	if err != nil {
		return out, err
	}
	if _, err = d.foundationCaller(r); err != nil {
		return out, integrationReadError(err)
	}
	admin, err := d.integrationAdmin(r, "playback")
	if err != nil {
		return out, integrationReadError(err)
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}, "cursor": {r.URL.Query().Get("cursor")}}
	if admin {
		page, e := integrationReadNative[playbackv1.AdminSessionPage](d, r, "/v1/admin/sessions", query)
		if e != nil {
			return out, integrationReadError(e)
		}
		for _, item := range page.Items {
			controls, e := integrationReadNative[playbackv1.SessionControls](d, r, "/v1/admin/sessions/"+url.PathEscape(item.ID)+"/controls", nil)
			if e != nil {
				return out, integrationReadError(e)
			}
			row := IntegrationSession{Actions: controls.Actions, Revision: controls.Revision, ID: item.ID, State: item.State, Kind: item.Kind, User: &item.User, Profile: &item.Profile, Device: item.Device, PositionMs: item.PositionMs, DurationMs: item.DurationMs, StartedAt: item.StartedAt, Location: item.Location, App: item.App, Platform: item.Platform}
			if item.Item != nil {
				row.Item = &IntegrationSessionItem{ID: item.Item.ID, Title: item.Item.Title, Kind: item.Item.Kind}
			}
			out.Items = append(out.Items, row)
		}
		out.NextCursor = page.Page.NextCursor
	} else {
		// /playback/controls requires playback scope as well as this route's read
		// scope. Its HTTP handler captures/rechecks the exact family/device and
		// strips hidden item metadata; we do not bypass either native boundary.
		page, e := integrationReadNative[playbackv1.RemoteControlsDocument](d, r, "/v1/playback/controls", query)
		if e != nil {
			return out, integrationReadError(e)
		}
		for _, item := range page.Items {
			row := IntegrationSession{Actions: item.Actions, ID: item.SessionID, State: item.State, Kind: item.Kind, Device: item.Device, PositionMs: item.PositionMs, DurationMs: item.DurationMs, Revision: item.Revision}
			if item.Item != nil {
				row.Item = &IntegrationSessionItem{ID: item.Item.ID, Title: item.Item.Title}
			}
			out.Items = append(out.Items, row)
		}
		out.NextCursor = page.NextCursor
		if e := d.integrationOwnSessionFacts(r, &out); e != nil {
			return IntegrationSessions{}, integrationReadError(e)
		}
	}
	if err := d.integrationSessionItems(r, &out, admin); err != nil {
		return IntegrationSessions{}, integrationReadError(err)
	}
	return out, nil
}

// Fill native ownership/app facts for the already-authorized own-controls page
// in one batch. No target profile is guessed from the caller's current profile.
func (d Dependencies) integrationOwnSessionFacts(r *http.Request, out *IntegrationSessions) error {
	if len(out.Items) == 0 {
		return nil
	}
	return d.integrationCatalogRead(r, func(ctx context.Context, p identity.Principal, _ *catalog.Service, _ catalog.Viewer, _ bool) error {
		ids := []string{}
		positions := map[string]int{}
		for i, item := range out.Items {
			ids = append(ids, item.ID)
			positions[item.ID] = i
		}
		raw, _ := json.Marshal(ids)
		tx := dbwork.Snapshot(ctx)
		rows, e := tx.QueryContext(ctx, `SELECT s.id,s.account_id,s.profile_id,COALESCE(a.username,''),COALESCE(p.name,''),COALESCE(d.app,''),COALESCE(d.platform,'') FROM playback_v1_sessions s LEFT JOIN accounts a ON a.id=s.account_id LEFT JOIN direct_profiles p ON p.id=s.profile_id AND p.account_id=s.account_id AND p.deleted=0 LEFT JOIN identity_devices d ON d.id=s.device_id AND d.account_id=s.account_id WHERE s.id IN(SELECT value FROM json_each(?)) AND s.account_id=? AND s.authority=?`, string(raw), p.AccountID, p.Authority)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id, account, profile, username, name, app, platform string
			if e = rows.Scan(&id, &account, &profile, &username, &name, &app, &platform); e != nil {
				rows.Close()
				return e
			}
			item := &out.Items[positions[id]]
			item.User = &playbackv1.AdminName{ID: account, Name: username}
			item.Profile = &playbackv1.AdminName{ID: profile, Name: name}
			item.App = app
			item.Platform = platform
		}
		e = rows.Err()
		rows.Close()
		return e
	})
}

// Episode/grouping facts come from one authorized catalog batch for the page.
// This rechecks item/library restrictions after native session composition and
// supplies real season identities, which the dashboard's display item omits.
func (d Dependencies) integrationSessionItems(r *http.Request, out *IntegrationSessions, admin bool) error {
	ids := []string{}
	for _, item := range out.Items {
		if item.Item != nil {
			ids = append(ids, item.Item.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return d.integrationCatalogRead(r, func(_ context.Context, _ identity.Principal, cat *catalog.Service, v catalog.Viewer, _ bool) error {
		page, e := cat.QueryIntegrationItems(v, catalog.IntegrationItemQuery{IDs: ids, Limit: 100}, false)
		if e != nil {
			return e
		}
		visible := map[string]catalog.IntegrationItem{}
		for _, item := range page.Items {
			visible[item.ID] = item
		}
		for i := range out.Items {
			item := &out.Items[i]
			if item.Item == nil {
				continue
			}
			facts, ok := visible[item.Item.ID]
			if !ok {
				item.Item = nil
				item.PositionMs = 0
				item.DurationMs = 0
				if !admin {
					item.Actions = []string{"stop"}
				}
				continue
			}
			item.Item = &IntegrationSessionItem{ID: facts.ID, Title: facts.Title, Kind: facts.Kind, Episode: facts.Episode}
		}
		return nil
	})
}
