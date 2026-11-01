package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/integrationids"
)

// Compatibility is an exact, integration-only protocol vocabulary. It has no
// player routes, discovery service, plugin framework or independent behaviour.
type compatibilityRoute struct {
	method, path, id string
	scope            apikit.Scope
	public           bool
}

var compatibilityRoutes = []compatibilityRoute{
	{"GET", "/System/Configuration", "configuration", apikit.ScopeRead, false},
	{"GET", "/System/Info", "info", apikit.ScopeRead, false},
	{"GET", "/System/Info/Public", "public_info", apikit.ScopeOpen, true},
	{"POST", "/Users/AuthenticateByName", "authenticate", apikit.ScopeSession, true},
	{"POST", "/Auth/Keys", "mint_key", apikit.ScopeRead, false},
	{"GET", "/Auth/Keys", "keys", apikit.ScopeRead, false},
	{"GET", "/Users", "users", apikit.ScopeRead, false},
	{"GET", "/Users/{id}", "user", apikit.ScopeRead, false},
	{"GET", "/Users/{id}/Views", "views", apikit.ScopeRead, false},
	{"GET", "/Users/{id}/Items", "user_items", apikit.ScopeRead, false},
	{"GET", "/Users/{id}/Items/Latest", "user_latest", apikit.ScopeRead, false},
	{"GET", "/Users/{id}/Items/{item}", "user_item", apikit.ScopeRead, false},
	{"GET", "/Users/{id}/Images/Primary", "user_image", apikit.ScopeRead, false},
	{"GET", "/Library/MediaFolders", "media_folders", apikit.ScopeRead, false},
	{"GET", "/Library/VirtualFolders", "virtual_folders", apikit.ScopeRead, false},
	{"POST", "/Library/Media/Updated", "media_updated", apikit.ScopeLibrary, false},
	{"POST", "/Library/Refresh", "untargeted_refresh", apikit.ScopeLibrary, false},
	{"POST", "/Notifications/Admin", "notify", apikit.ScopeAdmin, false},
	{"GET", "/Items", "items", apikit.ScopeRead, false},
	{"GET", "/Items/Latest", "latest", apikit.ScopeRead, false},
	{"POST", "/Items/{id}/Refresh", "refresh_item", apikit.ScopeLibrary, false},
	{"GET", "/Items/{id}/PlaybackInfo", "media_info", apikit.ScopeRead, false},
	{"GET", "/Items/{id}/Images/{type}", "image", apikit.ScopeRead, false},
	{"GET", "/Items/{id}/Images/{type}/{index}", "indexed_image", apikit.ScopeRead, false},
	{"GET", "/Shows/{id}/Seasons", "seasons", apikit.ScopeRead, false},
	{"GET", "/Shows/{id}/Episodes", "episodes", apikit.ScopeRead, false},
	{"GET", "/Sessions", "sessions", apikit.ScopeRead, false},
	{"POST", "/Sessions/{id}/Playing/{command}", "transport", apikit.ScopePlayback, false},
	{"GET", "/DisplayPreferences/usersettings", "display_preferences", apikit.ScopeRead, false},
	{"GET", "/Plugins", "plugins", apikit.ScopeRead, false},
}

var compatibilityQueries = []string{"App", "app", "userId", "UserId", "userid", "ids", "Ids", "parentId", "ParentId", "fields", "Fields", "recursive", "Recursive", "includeItemTypes", "IncludeItemTypes", "excludeItemTypes", "ExcludeItemTypes", "years", "Years", "sortBy", "SortBy", "sortOrder", "SortOrder", "startIndex", "StartIndex", "limit", "Limit", "searchTerm", "SearchTerm", "anyProviderIdEquals", "AnyProviderIdEquals", "isMissing", "IsMissing", "excludeLocationTypes", "ExcludeLocationTypes", "collapseBoxSetItems", "CollapseBoxSetItems", "enableTotalRecordCount", "EnableTotalRecordCount", "minDateCreated", "MinDateCreated", "season", "Season", "seasonId", "SeasonId", "metadataRefreshMode", "MetadataRefreshMode", "imageRefreshMode", "ImageRefreshMode", "replaceAllMetadata", "ReplaceAllMetadata", "replaceAllImages", "ReplaceAllImages", "refreshRecursive", "RecursiveRefresh", "client", "Client", "maxWidth", "MaxWidth", "maxHeight", "MaxHeight", "fillWidth", "FillWidth", "quality", "Quality", "blur", "Blur", "tag", "Tag", "seekPositionTicks", "SeekPositionTicks", "playCommand", "PlayCommand", "itemIds", "ItemIds"}

func compatibilityErrorCodes() []string {
	codes := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "internal", "insufficient_scope", "session_required", "administration_unavailable", "authentication_busy", "credential_limit", "rate_limited", "invalid_cursor", "cursor_expired", "stale_continuation", "revision_required", "revision_mismatch", "session_ended"}
	seen := map[string]bool{}
	for _, code := range codes {
		seen[code] = true
	}
	for _, code := range subtitleRouteErrors() {
		if !seen[code] {
			codes = append(codes, code)
			seen[code] = true
		}
	}
	return codes
}

func registerCompatibilityRoutes(registry *apikit.Registry, d Dependencies) error {
	for _, spec := range compatibilityRoutes {
		for _, alias := range []struct{ prefix, path, suffix string }{{"", spec.path, ""}, {"/mediabrowser", spec.path, "_legacy"}, {"", strings.ToLower(spec.path), "_lower"}, {"/mediabrowser", strings.ToLower(spec.path), "_legacy_lower"}} {
			spec := spec
			path := alias.prefix + alias.path
			id := "compat_" + spec.id + alias.suffix
			access := apikit.Device
			if spec.public {
				access = apikit.Public
			}
			body := int64(0)
			if spec.method == "POST" {
				body = 1 << 20
			}
			m := apikit.Metadata{Audience: apikit.Integrations, Protocol: apikit.ProtocolCompatibility, ID: id, Method: spec.method, Path: path, Summary: "Integration compatibility: " + spec.id, Access: access, Requires: spec.scope, Lane: apikit.Default, Cost: apikit.PageSized, Admission: laneDefault, Status: 200, BodyLimit: body, Query: compatibilityQueries, Errors: compatibilityErrorCodes()}
			// Only these compatibility handlers consume limit; the other
			// operations accept the shared vocabulary but ignore this field.
			switch spec.id {
			case "items", "user_items", "latest", "user_latest", "seasons", "episodes":
				integer := apikit.QueryInteger{Name: "limit", Minimum: 0, Maximum: 5000000, NoDefault: true, EmptyIsDefault: true, Description: "Absent or empty means no overall row limit; explicit zero emits no rows. Native reads remain bounded pages. Limit is a case-insensitive alias."}
				if spec.id == "latest" || spec.id == "user_latest" {
					integer.Default, integer.NoDefault = 20, false
					integer.Description = "Absent or empty means 20 rows; explicit zero emits no rows. Native reads remain bounded pages. Limit is a case-insensitive alias."
				}
				m.QueryIntegers = []apikit.QueryInteger{integer}
				integer.Name = "Limit"
				m.QueryIntegers = append(m.QueryIntegers, integer)
			}

			if err := apikit.RegisterRaw[json.RawMessage, json.RawMessage](registry, apikit.RawRoute[json.RawMessage, json.RawMessage]{Metadata: m, Serve: func(w http.ResponseWriter, r *http.Request) { d.serveCompatibility(w, r, spec) }}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d Dependencies) compatibilityID(r *http.Request, kind, native string) (string, error) {
	return (integrationids.Store{DB: d.DB}).Encode(r.Context(), kind, native)
}
func (d Dependencies) compatibilityNativeID(r *http.Request, kind, guid string) (string, error) {
	return (integrationids.Store{DB: d.DB}).Decode(r.Context(), kind, guid)
}
func (d Dependencies) integrationCall(r *http.Request, method, path string, query url.Values, body any) (map[string]any, error) {
	status, header, raw, err := d.mcpInvoke(r)(mcpCall{Method: method, Path: path, Query: query, Body: body})
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		var doc struct {
			Error apierror.Error `json:"error"`
		}
		_ = json.Unmarshal(raw, &doc)
		switch doc.Error.Code {
		case "":
			doc.Error.Code = "internal"
		// The existing console envelope predates the canonical error catalogue.
		// Translate its permission/validation refusals without changing authority.
		case "forbidden":
			doc.Error.Code = "not_permitted"
		case "invalid_console_request":
			doc.Error.Code = "invalid_request"
		case "console_unavailable":
			doc.Error.Code = "administration_unavailable"
		case "console_conflict":
			doc.Error.Code = "revision_mismatch"
		case "console_capacity":
			doc.Error.Code = "rate_limited"
		}
		seconds, _ := strconv.Atoi(header.Get("Retry-After"))
		if seconds < 0 || seconds > 3600 {
			seconds = 0
		}
		// Preserve actionable native refusal details, but never a Current resource:
		// a playback revision error may contain a native presentation or media URL.
		return nil, &apierror.Error{Code: doc.Error.Code, Reason: doc.Error.Reason, Fields: doc.Error.Fields, RetryAfterSeconds: seconds}
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
func compatibilityFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, entityid.ErrNotFound) {
		err = &apierror.Error{Code: "not_found"}
	} else {
		err = foundationAuthError(err)
	}
	policyErrors.Write(w, "", err)
}
func compatibilityDecode(w http.ResponseWriter, r *http.Request, out any) error {
	reader := http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(out); err != nil {
		return &apierror.Error{Code: "invalid_request"}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return &apierror.Error{Code: "invalid_request"}
	}
	return nil
}
func compatObject(value any) map[string]any {
	out, _ := value.(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	return out
}
func compatObjects(value any) []map[string]any {
	values, _ := value.([]any)
	out := make([]map[string]any, 0, len(values))
	for _, v := range values {
		out = append(out, compatObject(v))
	}
	return out
}
func compatText(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func compatNumber(v any) float64 { n, _ := v.(float64); return n }

func (d Dependencies) compatibilityInfo(r *http.Request) (map[string]any, error) {
	doc, err := d.integrationCall(r, "GET", "/v1/system", nil, nil)
	if err != nil {
		return nil, err
	}
	guid, err := d.compatibilityID(r, "server", compatText(doc["id"]))
	if err != nil {
		return nil, err
	}
	// The exact build is not published before sign-in (O-169): a compatibility
	// client only needs a version it can read, so it gets a fixed placeholder.
	const version = "0.0.0+portico"
	return map[string]any{"Id": guid, "ServerName": doc["name"], "Name": doc["name"], "Version": version, "ProductName": "Portico", "StartupWizardCompleted": doc["setupRequired"] == false, "SupportsLocalPorticoIntegrations": true}, nil
}
func (d Dependencies) compatibilityUser(r *http.Request, native map[string]any) (map[string]any, error) {
	id := compatText(native["profileId"])
	if id == "" {
		id = compatText(native["id"])
	}
	guid, err := d.compatibilityID(r, "user", id)
	if err != nil {
		return nil, err
	}
	info, err := d.compatibilityInfo(r)
	if err != nil {
		return nil, err
	}
	role := compatText(native["role"])
	name := native["username"]
	if name == nil {
		name = native["name"]
	}
	return map[string]any{"Id": guid, "Name": name, "ServerId": info["Id"], "HasPassword": true, "HasConfiguredPassword": true, "Policy": map[string]any{"IsAdministrator": role == "owner" || role == "admin", "IsDisabled": native["disabled"] == true, "EnableAllFolders": native["allLibraries"] == true}}, nil
}
func (d Dependencies) requireCompatibilityUser(r *http.Request, guid string) error {
	if guid == "" || strings.EqualFold(guid, "me") {
		return nil
	}
	id, err := d.compatibilityNativeID(r, "user", guid)
	if err != nil {
		return &apierror.Error{Code: "not_found"}
	}
	p, err := d.foundationCaller(r)
	if err != nil {
		return foundationAuthError(err)
	}
	if id == p.ProfileID {
		return nil
	}
	c, _ := identity.APICredentialFrom(r.Context())
	if p.Role == identity.TierOwner || p.Role == identity.TierAdmin {
		for _, s := range c.Scopes {
			if s == "admin" {
				return nil
			}
		}
	}
	return &apierror.Error{Code: "not_permitted"}
}

func (d Dependencies) serveCompatibility(w http.ResponseWriter, r *http.Request, spec compatibilityRoute) {
	if err := d.requireIntegrations(r.Context()); err != nil {
		compatibilityFailure(w, err)
		return
	}
	q, ok := compatibilityQuery(r)
	if !ok {
		compatibilityFailure(w, &apierror.Error{Code: "invalid_request"})
		return
	}
	if strings.HasPrefix(spec.id, "user_") || spec.id == "views" || spec.id == "user" {
		if err := d.requireCompatibilityUser(r, r.PathValue("id")); err != nil {
			compatibilityFailure(w, err)
			return
		}
	}
	if user := q.Get("userid"); user != "" {
		if err := d.requireCompatibilityUser(r, user); err != nil {
			compatibilityFailure(w, err)
			return
		}
	}
	var result any
	var err error
	switch spec.id {
	case "info", "public_info", "configuration":
		result, err = d.compatibilityInfo(r)
	case "authenticate":
		var in struct {
			Username string
			Pw       string
			Password string
		}
		if err = compatibilityDecode(w, r, &in); err == nil {
			if in.Pw != "" && in.Password != "" && in.Pw != in.Password {
				err = &apierror.Error{Code: "invalid_request"}
				break
			}
			password := in.Pw
			if password == "" {
				password = in.Password
			}
			var login map[string]any
			login, err = d.integrationCall(r, "POST", "/v1/integrations/authenticate", nil, IntegrationLogin{Username: in.Username, Password: password, Name: "Integration"})
			if err == nil {
				rr := r.Clone(r.Context())
				rr.Header.Set("Authorization", "Bearer "+compatText(login["token"]))
				var selected map[string]any
				selected, err = d.compatibilityNativeUser(rr, compatText(login["userId"]))
				if err == nil {
					var translated map[string]any
					translated, err = d.compatibilityUser(rr, selected)
					if err == nil {
						result = map[string]any{"AccessToken": login["token"], "User": translated, "ServerId": translated["ServerId"]}
					}
				}
			}
		}
	case "mint_key":
		var issued map[string]any
		issued, err = d.integrationCall(r, "POST", "/v1/integrations/keys", nil, IntegrationTokenRequest{Name: q.Get("app")})
		if err == nil {
			result = map[string]any{"AccessToken": issued["secret"], "AppName": q.Get("app")}
		}
	case "keys":
		var keys map[string]any
		keys, err = d.integrationCall(r, "GET", "/v1/integrations/keys", nil, nil)
		if err == nil {
			items := []any{}
			for _, key := range compatObjects(keys["items"]) {
				var guid string
				guid, err = d.compatibilityID(r, "key", compatText(key["id"]))
				if err != nil {
					break
				}
				items = append(items, map[string]any{"Id": guid, "AppName": key["name"], "AccessToken": key["secret"]})
			}
			result = map[string]any{"Items": items, "TotalRecordCount": len(items)}
		}
	case "users":
		d.compatibilityStream(w, r, nil, url.Values{"limit": {"100"}}, "/v1/integrations/users", true, 0, 0, false, func(row map[string]any) (any, error) { return d.compatibilityUser(r, row) })
		return
	case "user", "display_preferences":
		want := r.PathValue("id")
		if want == "" || strings.EqualFold(want, "me") {
			var p identity.Principal
			p, err = d.foundationCaller(r)
			if err != nil {
				err = foundationAuthError(err)
				break
			}
			want = p.ProfileID
		} else {
			want, err = d.compatibilityNativeID(r, "user", want)
			if err != nil {
				break
			}
		}
		var selected map[string]any
		selected, err = d.compatibilityNativeUser(r, want)
		if err == nil {
			result, err = d.compatibilityUser(r, selected)
		}
	case "media_folders", "virtual_folders", "views":
		d.compatibilityStream(w, r, nil, url.Values{"limit": {"100"}}, "/v1/integrations/libraries", spec.id == "virtual_folders", 0, 0, false, func(row map[string]any) (any, error) { return d.compatibilityLibrary(r, row, spec.id) })
		return
	case "items", "user_items", "latest", "user_latest":
		d.compatibilityItems(w, r, q, spec.id)
		return
	case "user_item":
		var id string
		id, err = d.compatibilityNativeID(r, "item", r.PathValue("item"))
		if err == nil {
			var item map[string]any
			item, err = d.integrationCall(r, "GET", "/v1/integrations/items", url.Values{"ids": {id}}, nil)
			if err == nil {
				rows := compatObjects(item["items"])
				if len(rows) == 0 {
					err = &apierror.Error{Code: "not_found"}
				} else {
					result, err = d.compatibilityItem(r, rows[0])
				}
			}
		}
	case "seasons", "episodes":
		d.compatibilityHierarchy(w, r, q, spec.id)
		return
	case "sessions":
		d.compatibilitySessions(w, r)
		return
	case "media_info":
		var id string
		id, err = d.compatibilityNativeID(r, "item", r.PathValue("id"))
		if err == nil {
			var media map[string]any
			media, err = d.integrationCall(r, "GET", "/v1/integrations/items/"+url.PathEscape(id)+"/media-info", nil, nil)
			if err == nil {
				result = map[string]any{"MediaSources": d.compatibilitySources(r, id, media["mediaSources"], compatText(media["nextCursor"]))}
			}
		}
	case "media_updated":
		var in struct {
			Updates []struct {
				Path       string
				UpdateType *string
			}
		}
		if err = compatibilityDecode(w, r, &in); err == nil {
			changes := []PathChange{}
			for _, p := range in.Updates {
				change := "changed"
				if p.UpdateType != nil {
					switch strings.ToLower(*p.UpdateType) {
					case "created":
						change = "added"
					case "deleted":
						change = "removed"
					case "modified", "":
					default:
						err = &apierror.Error{Code: "invalid_request"}
					}
				}
				changes = append(changes, PathChange{Path: p.Path, Change: change})
			}
			if err == nil {
				result, err = d.integrationCall(r, "POST", "/v1/libraries/paths-changed", nil, map[string]any{"paths": changes, "mode": "strict"})
			}
		}
	case "untargeted_refresh":
		// A real native refresh: one coalesced ordinary scan per captured
		// enabled source. Unknown watcher coverage or a restart gap is not
		// treated as "nothing changed"; busy sources answer deferred.
		result, err = d.integrationCall(r, "POST", "/v1/integrations/refresh", nil, map[string]any{})
	case "refresh_item":
		result, err = d.compatibilityRefresh(r, q)
	case "transport":
		result, err = d.compatibilityControl(r, q)
	case "plugins":
		result = []any{}
	case "notify":
		var in struct {
			Name        string
			Description string
			ImageUrl    string
		}
		if err = compatibilityDecode(w, r, &in); err == nil {
			operation := "integration-" + identity.Token()
			result, err = d.integrationCall(r, "POST", "/v1/admin/notifications/broadcast", nil, map[string]any{"operationId": operation, "audience": "account-admin", "severity": "info", "dedupeKey": operation, "title": in.Name, "body": in.Description, "actions": []any{}, "expiresInDays": 1})
		}
	case "image", "indexed_image", "user_image":
		d.compatibilityImage(w, r, q, spec.id)
		return
	default:
		err = &apierror.Error{Code: "not_found"}
	}
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			err = &apierror.Error{Code: "unauthorized"}
		}
		compatibilityFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	if err = writeCompatibilityJSON(w, result); err != nil {
		abortCompatibilityStream(w)
	}
}
func (d Dependencies) integrationCallWithToken(r *http.Request, token, method, path string, query url.Values, body any) (map[string]any, error) {
	inner := r.Clone(r.Context())
	inner.Header.Set("Authorization", "Bearer "+token)
	return d.integrationCall(inner, method, path, query, body)
}

// Only the matching user is retained while walking bounded native pages.
func (d Dependencies) compatibilityNativeUser(r *http.Request, id string) (map[string]any, error) {
	q := url.Values{"limit": {"100"}}
	for {
		page, err := d.integrationCall(r, "GET", "/v1/integrations/users", q, nil)
		if err != nil {
			return nil, err
		}
		for _, u := range compatObjects(page["items"]) {
			if compatText(u["profileId"]) == id || compatText(u["id"]) == id {
				return u, nil
			}
		}
		cursor := compatText(page["nextCursor"])
		if cursor == "" {
			return nil, &apierror.Error{Code: "not_found"}
		}
		if cursor == q.Get("cursor") {
			return nil, &apierror.Error{Code: "internal"}
		}
		q.Set("cursor", cursor)
	}
}
func (d Dependencies) compatibilityLibrary(r *http.Request, lib map[string]any, shape string) (any, error) {
	guid, err := d.compatibilityID(r, "library", compatText(lib["id"]))
	if err != nil {
		return nil, err
	}
	collection := ""
	switch compatText(lib["kind"]) {
	case "movie", "movies":
		collection = "movies"
	case "tv", "show", "anime":
		collection = "tvshows"
	case "music":
		collection = "music"
	case "audiobook", "book":
		collection = "books"
	}
	paths := lib["paths"]
	if paths == nil {
		paths = []string{}
	}
	if shape == "virtual_folders" {
		return map[string]any{"ItemId": guid, "Name": lib["name"], "CollectionType": collection, "Locations": paths}, nil
	}
	return map[string]any{"Id": guid, "Name": lib["name"], "CollectionType": collection, "Type": "CollectionFolder", "IsFolder": true, "Locations": paths}, nil
}
