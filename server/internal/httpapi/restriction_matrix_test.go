package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
)

// SEC-02 / BE-SRV-01: a restricted profile sees nothing restricted on ANY route.
//
// The matrix walks this fixture's actual registered API metadata plus the
// non-API infrastructure inventory. Every GET route must appear in
// restrictionRoutes, either with the concrete requests that reach catalogue
// content, or with the reason it cannot carry a title. A new GET route without
// an entry fails this test, so adding a route means deciding its restriction
// story. The content-bearing POST routes (browse, search-like reads) are listed
// in restrictionReadPOSTs and probed the same way.
//
// A probe passes when the restricted viewer is refused (any 4xx) or when the
// answer contains no identifier of anything that viewer may not see: a title
// over the rating ceiling or carrying a blocked label, a container with no
// visible member, or anything in a library the profile is not given. The
// hidden set is computed with the catalogue's own visibility predicate
// (catalog.VisibleItem/VisibleEntity), the single rule every surface must
// apply, and checked for being non-empty and visible to the open viewer so the
// test cannot pass vacuously.
func TestRestrictedProfileRouteMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	m := buildRestrictionMatrix(t)
	routeLanes := m.routeInventory
	routes := restrictionRoutes()

	// 1. Exhaustive: every GET route is classified, and nothing stale is.
	for pattern := range routeLanes {
		if !strings.HasPrefix(pattern, "GET ") {
			continue
		}
		if _, ok := routes[pattern]; !ok {
			t.Errorf("%s has no restriction classification: add it to restrictionRoutes (restriction_matrix_test.go) with the requests that reach catalogue content, or the reason it carries none", pattern)
		}
	}
	for pattern := range routes {
		if _, ok := routeLanes[pattern]; !ok {
			t.Errorf("restrictionRoutes lists %s, which the router does not register", pattern)
		}
	}
	for pattern := range restrictionReadPOSTs(m) {
		if _, ok := routeLanes[pattern]; !ok {
			t.Errorf("restrictionReadPOSTs lists %s, which the router does not register", pattern)
		}
	}

	// 2. No route answers 401 to a valid session (CD-51). Every GET route is
	// called as the restricted member, with generic path parameters, except
	// routes whose credential is not the session (a grant or device token).
	for pattern := range routeLanes {
		if !strings.HasPrefix(pattern, "GET ") || nonSessionCredential[pattern] != "" {
			continue
		}
		path := pathParameter.ReplaceAllString(strings.TrimPrefix(pattern, "GET "), "matrix-unknown")
		if path == "" {
			path = "/"
		}
		status, response := m.callWithin("GET", path, "restricted", 300*time.Millisecond)
		if status == 401 {
			t.Errorf("%s: GET %s answered 401 to a valid restricted session: %s", pattern, path, strings.TrimSpace(response))
		}
	}

	// 2b. Owner administration refuses the member outright, whatever it
	// names. Every GET under /v1/admin/ is exempted from probing on that
	// ground, so the ground itself is checked here, as it is for writes.
	for pattern := range routeLanes {
		if !strings.HasPrefix(pattern, "GET /v1/admin/") {
			continue
		}
		for _, id := range append(m.hiddenSamples(), m.reports...) {
			path := pathParameter.ReplaceAllString(strings.TrimPrefix(pattern, "GET "), url.PathEscape(id))
			status, response := m.call("GET", path, "", "restricted")
			if status != 403 && status != 404 {
				t.Errorf("%s: GET %s answered %d to a member profile; owner administration must refuse (403): %s", pattern, path, status, clip(response))
			}
			if found := m.marker.FindString(response); found != "" {
				t.Errorf("%s: refusal carries a hidden identifier %q", pattern, found)
			}
		}
	}

	// 3. Probes.
	probed := 0
	patterns := make([]string, 0, len(routes))
	for pattern := range routes {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, pattern := range patterns {
		route := routes[pattern]
		if route.check != nil {
			t.Run(pattern, route.check)
			continue
		}
		if route.probe == nil {
			if route.reason == "" {
				t.Errorf("%s: an exemption needs a reason", pattern)
			}
			continue
		}
		viewer := route.viewer
		if viewer == "" {
			viewer = "restricted"
		}
		for _, path := range route.probe(m) {
			probed++
			m.check(t, pattern, viewer, "GET", path, "")
		}
	}
	for pattern, bodies := range restrictionReadPOSTs(m) {
		for _, request := range bodies {
			probed++
			m.check(t, pattern, "restricted", "POST", request.path, request.body)
		}
	}
	t.Run("container-writes", func(t *testing.T) {
		for _, id := range m.hiddenEntities["show"] {
			code, _ := m.call("PUT", "/v1/containers/show/"+id+"/personal-state", `{"expectedRevision":0,"watched":true}`, "restricted")
			if code != 404 {
				t.Errorf("hidden container: %d", code)
			}
		}
		if len(m.visibleEntities["show"]) > 0 {
			id := m.visibleEntities["show"][0]
			code, body := m.call("PUT", "/v1/containers/show/"+id+"/personal-state", `{"expectedRevision":0,"watched":true}`, "restricted")
			if code != 200 {
				t.Fatalf("visible container: %d %s", code, body)
			}
			m.check(t, "GET /v1/containers/{kind}/{id}/personal-state", "restricted", "GET", "/v1/containers/show/"+id+"/personal-state", "")
		}
	})
	t.Run("container-watermark-security", func(t *testing.T) { m.containerWatermarkSecurity(t) })
	t.Run("download-request-security", func(t *testing.T) { m.downloadRequestSecurity(t) })
	// Non-vacuous: most probed routes must actually answer the restricted
	// profile, so their bodies were checked rather than refused wholesale.
	answered := 0
	for pattern, statuses := range m.statuses {
		ok := false
		for _, status := range statuses {
			ok = ok || status < 400
		}
		if ok {
			answered++
		}
		if testing.Verbose() {
			t.Logf("%-55s %v", pattern, statuses)
		}
	}
	if answered < 40 {
		t.Errorf("only %d probed routes answered the restricted profile; the fixture or the probes are wrong", answered)
	}
	t.Logf("%d restricted-viewer requests over %d routes (%d answered with content); %d hidden identifiers", probed, len(routes), answered, len(m.markers))
}

// refusedStatus is the answer to a refused request (CD-51): 401 only without
// a credential, 403 for a valid session that may not do this.
func refusedStatus(token string) int {
	if token == "" || token == "retired-authority-credential" {
		return 401
	}
	return 403
}

// nonSessionCredential are the GET routes authenticated by something other than
// the session, so a session bearer there is rightly "not a credential" (401).
var nonSessionCredential = map[string]string{
	"GET /v1/downloads/artifacts/{grant}/artwork/{role}":                         "download transfer grant, resolved and reauthorized by downloadExtra",
	"GET /v1/downloads/artifacts/{grant}/subtitles/{resource}/{revision}.vtt":    "download transfer grant, resolved and reauthorized by downloadExtra",
	"GET /v1/downloads/artifacts/{grant}/subtitles/{resource}/{revision}/{file}": "download transfer grant, resolved and reauthorized by downloadExtra",
	"GET /v1/downloads/artifacts/{grant}/trickplay/{set}/thumbnails.vtt":         "download transfer grant, resolved and reauthorized by downloadExtra",
	"GET /v1/downloads/artifacts/{grant}/trickplay/{set}/tiles/{tile}":           "download transfer grant, resolved and reauthorized by downloadExtra",
	"GET /v1/downloads/artifacts/{grant}/chapters/{source}/{index}/image":        "download transfer grant, resolved and reauthorized by downloadExtra",

	"GET /v1/topshelf":                                                   "Top Shelf device token",
	"GET /v1/topshelf/art/{id}":                                          "Top Shelf device token",
	"GET /v1/media/{grant}":                                              "media grant",
	"GET /v1/media/{grant}/{file}":                                       "media grant",
	"GET /v1/media/{grant}/audio-converted":                              "media grant",
	"GET /v1/media/{grant}/audio-index":                                  "media grant",
	"GET /v1/media/{grant}/audio":                                        "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}":              "media grant",
	"GET /v1/downloads/artifacts/{grant}":                                "download grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}.vtt":          "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.ass": "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.ssa": "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.sup": "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.idx": "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.sub": "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.mks": "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/fonts":        "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/lines":        "media grant",
	"GET /v1/media/subtitles/{grant}/{resource}/{revision}/fonts/{font}": "media grant",
}

var pathParameter = regexp.MustCompile(`\{[^}]+\}`)

type restrictionRoute struct {
	// check exercises routes that need captured session/header authority.
	check  func(*testing.T)
	probe  func(*restrictionEnv) []string
	reason string
	// viewer names the credential the probe uses; empty is the restricted
	// profile's session. The Top Shelf feed takes its own device token.
	viewer string
}

type postProbe struct{ path, body string }

func exempt(reason string) restrictionRoute { return restrictionRoute{reason: reason} }
func probe(f func(*restrictionEnv) []string) restrictionRoute {
	return restrictionRoute{probe: f}
}
func static(paths ...string) restrictionRoute {
	return probe(func(*restrictionEnv) []string { return paths })
}

// perItem expands a suffix over one hidden sample of every item kind, plus one
// visible title (whose answer must still not leak hidden neighbours).
func perItem(suffix string) restrictionRoute {
	return probe(func(m *restrictionEnv) []string {
		out := []string{}
		for _, id := range append(m.hiddenItemSamples(), m.visibleMovie) {
			out = append(out, "/v1/items/"+id+suffix)
		}
		return out
	})
}

func perLibrary(suffix string) restrictionRoute {
	return probe(func(m *restrictionEnv) []string {
		out := []string{}
		for _, id := range m.libraries {
			out = append(out, "/v1/libraries/"+id+suffix)
		}
		return out
	})
}

func perEntity(prefix, kind, suffix string) restrictionRoute {
	return probe(func(m *restrictionEnv) []string {
		out := []string{}
		for _, id := range append(append([]string{}, m.hiddenEntities[kind]...), m.visibleEntities[kind]...) {
			out = append(out, prefix+id+suffix)
		}
		return out
	})
}

const (
	reasonAdmin    = "owner-only administration: a member profile is refused before any catalogue read (admin_access_test, owner_authority_test)"
	reasonNoTitles = "carries no catalogue titles"
	reasonGrant    = "addresses an opaque grant, session or job minted only by a request that already passed the restriction check (restriction_admission_test, playback_v1_security_test, downloads_routes_test)"
	reasonStream   = "long-lived event stream; its payloads are the viewer's own changes and ids already admitted (social_routes_test, notifications tests)"
)

// restrictionRoutes classifies every GET route the router registers.
func restrictionRoutes() map[string]restrictionRoute {
	routes := map[string]restrictionRoute{
		"GET /v1/facet-queries/{id}":                                {check: facetQueryRestrictionCheck},
		"GET /v1/genre-queries/{id}":                                {check: genreQueryRestrictionCheck},
		"GET /v1/libraries/{id}/browse-selection-queries/{queryId}": {check: browseSelectionQueryRestrictionCheck("GET")},
		"GET /v1/libraries/{id}/reading-browse-queries/{queryId}":   {check: readingBrowseQueryRestrictionCheck("GET")},
		"GET /v1/libraries/{id}/reading-facet-queries/{queryId}":    {check: readingFacetQueryRestrictionCheck("GET")},
		// Opaque private handles: the check mints the real preparation, the real
		// confirmed operation and the real metadata preview query, then proves the
		// restricted viewer cannot read or release them and never sees their
		// hidden content.
		"GET /v1/libraries/{id}/selection-actions/preparations/{preparationId}": {check: selectionActionPreparationQueryRestrictionCheck("GET")},
		"GET /v1/selection-actions/{operationId}":                               {check: selectionActionConfirmOperationRestrictionCheck("GET")},
		"GET /v1/metadata/bulk/preview-queries/{queryId}":                       {check: bulkMetadataPreviewQueryRestrictionCheck("GET")},
		"GET /v1/user-mutations/{key}":                                          {check: userMutationReceiptRestrictionCheck},
		"GET /v1/user-mutations/policy":                                         exempt("server time and numeric replay cutoff only; no catalogue identifiers or content"),
		"GET /v1/reading/search":                                                {check: readingBodySearchRestrictionCheck},
		"GET /v1/reading/sessions/{sessionId}/search":                           {check: readingBodySearchRestrictionCheck},
		"GET /v1/libraries/{libraryId}/reading/mark-all":                        perLibrary("/reading/mark-all"),
		"GET /v1/items/{id}/disc-titles":                                        perItem("/disc-titles"),
		"GET /v1/admin/dvr/recording-root":                                      exempt(reasonAdmin),
		"GET /v1/libraries/{libraryId}/reading/periodicals":                     {check: readingPeriodicalRestrictionCheck},
		"GET /v1/libraries/{libraryId}/reading/periodicals/{seriesId}/issues":   {check: readingPeriodicalRestrictionCheck},

		"GET /v1/reading/continue":                                {check: readingRestrictionCheck("GET /v1/reading/continue")},
		"GET /v1/reading/items":                                   {check: readingRestrictionCheck("GET /v1/reading/items")},
		"GET /v1/reading/discover":                                {check: readingRestrictionCheck("GET /v1/reading/discover")},
		"GET /v1/libraries/{libraryId}/reading/series/{seriesId}": {check: readingSeriesRestrictionCheck},
		"GET /v1/libraries/{libraryId}/reading/groups/{groupKind}/{seriesId}/my-list": {check: readingSavedGroupRestrictionCheck},
		"GET /v1/items/{itemId}/reading/favorite":                                     {check: readingFavoriteRestrictionCheck},
		"GET /v1/items/{itemId}/reading/my-list":                                      {check: readingSavedRestrictionCheck},
		"GET /v1/items/{itemId}/reading/mark":                                         {check: readingMarkRestrictionCheck},
		"GET /v1/items/{itemId}/reading/versions":                                     {check: readingRestrictionCheck("GET /v1/items/{itemId}/reading/versions")},
		"GET /v1/reading/sessions/{sessionId}/source":                                 {check: readingRestrictionCheck("GET /v1/reading/sessions/{sessionId}/source")},
		"GET /v1/reading/sessions/{sessionId}/assets":                                 {check: readingRestrictionCheck("GET /v1/reading/sessions/{sessionId}/assets")},
		"GET /v1/reading/sessions/{sessionId}/progress":                               {check: readingRestrictionCheck("GET /v1/reading/sessions/{sessionId}/progress")},
		"GET /v1/reading/sessions/{sessionId}/annotations":                            {check: readingRestrictionCheck("GET /v1/reading/sessions/{sessionId}/annotations")},
		"GET /v1/reading/sessions/{sessionId}/pages/{pageIndex}":                      {check: readingRestrictionCheck("GET /v1/reading/sessions/{sessionId}/pages/{pageIndex}")},
		"GET /v1/me/saved/summary":                                                    static("/v1/me/saved/summary"),
		"GET /v1/me/removed-titles/{id}":                                              probe(func(m *restrictionEnv) []string { return prefixed("/v1/me/removed-titles/", m.hiddenItemSamples()) }),
		"GET /v1/admin/activity":                                                      exempt(reasonAdmin),
		"GET /v1/admin/access/members/{id}/profiles/{profileId}/avatar":               exempt(reasonAdmin),
		"GET /v1/admin/access/events":                                                 exempt("administrator-only server maintenance events; no catalogue item, title, path or asset; a member is refused at admission"),
		"GET /v1/admin/playback/route-failures":                                       exempt(reasonAdmin),
		"GET /v1/downloads/artifacts/{grant}/artwork/{role}":                          exempt("download transfer grant rechecked against current item and library visibility by downloadExtra"),
		"GET /v1/downloads/artifacts/{grant}/subtitles/{resource}/{revision}.vtt":     exempt("download transfer grant rechecked against current item and library visibility by downloadExtra"),
		"GET /v1/downloads/artifacts/{grant}/subtitles/{resource}/{revision}/{file}":  exempt("download transfer grant rechecked against current item and library visibility by downloadExtra"),
		"GET /v1/downloads/artifacts/{grant}/trickplay/{set}/thumbnails.vtt":          exempt("download transfer grant rechecked against current item and library visibility by downloadExtra and the analysis read"),
		"GET /v1/downloads/artifacts/{grant}/trickplay/{set}/tiles/{tile}":            exempt("download transfer grant rechecked against current item and library visibility by downloadExtra and the analysis read"),
		"GET /v1/downloads/artifacts/{grant}/chapters/{source}/{index}/image":         exempt("download transfer grant rechecked against current item and library visibility by downloadExtra and the analysis read"),
		// The list for the session's own device (any other device is not this
		// session's to read). A request whose container has since become hidden
		// is checked in the download-request-security subtest.
		"GET /v1/downloads/requests": probe(func(m *restrictionEnv) []string {
			return []string{"/v1/downloads/requests?deviceId=" + url.QueryEscape(m.device)}
		}),

		// The profile's inbox: notices written in the past, and by the owner,
		// whose links can lead to what the profile may not see now.
		"GET /v1/notifications/inbox":        static("/v1/notifications/inbox?limit=100", "/v1/notifications/inbox?state=unread&limit=100", "/v1/notifications/inbox?audience=profile&state=all&limit=100"),
		"GET /v1/notifications/unread-count": static("/v1/notifications/unread-count", "/v1/notifications/unread-count?audience=profile"),
		"GET /v1/notifications":              static("/v1/notifications?limit=40"),
		// The profile's own reports, each naming the title it was about.
		"GET /v1/feedback/reports": static("/v1/feedback/reports?limit=100"),
		"GET /v1/feedback/reports/{id}": probe(func(m *restrictionEnv) []string {
			return prefixed("/v1/feedback/reports/", m.reports)
		}),
		"GET /v1/admin/feedback/reports":           exempt(reasonAdmin),
		"GET /v1/admin/feedback/reports/{id}":      exempt(reasonAdmin),
		"GET /v1/admin/playback/history":           exempt(reasonAdmin),
		"GET /v1/admin/console/alerts":             exempt(reasonAdmin),
		"GET /v1/admin/console/diagnostics/{lane}": exempt(reasonAdmin),
		"GET /v1/admin/console/exports/{id}":       exempt(reasonAdmin),
		"GET /v1/admin/console/jobs":               exempt(reasonAdmin),
		"GET /v1/admin/console/panels/{panel}":     exempt(reasonAdmin),
		"GET /v1/admin/console/schedules":          exempt(reasonAdmin),

		// A download asked for before the restriction: what travels with it
		// (its title, artwork, chapters) is refused once the film is hidden.
		"GET /v1/downloads/preparations/{id}/bundle": probe(func(m *restrictionEnv) []string {
			return prefixed("/v1/downloads/preparations/", m.preparations, "/bundle")
		}),
		"GET /v1/admin/catalogue/failures":                exempt(reasonAdmin),
		"GET /v1/admin/resource-usage":                    exempt(reasonAdmin),
		"GET /v1/admin/libraries/{id}/scans/{job}/issues": exempt(reasonAdmin),
		"GET /v1/console/registry":                        exempt("owner setting definitions only; no catalogue titles"),

		"GET /v1/networking/connection": exempt(reasonNoTitles),
		"GET /v1/me/api-access":         exempt(reasonNoTitles),
		"GET /v1/me/access-tokens":      exempt("the caller's credentials, not catalogue content"),
		"GET /v1/mcp":                   exempt("GET refuses the method; scoped tools use POST"),
		// The title list a script pages through: every kind in every library,
		// in each order.
		"GET /v1/titles": probe(func(m *restrictionEnv) []string {
			out := []string{}
			for _, library := range m.libraries {
				for _, kind := range []string{"movie", "show", "episode", "album", "song", "book"} {
					for _, sort := range []string{"title", "year", "added"} {
						out = append(out, "/v1/titles?library="+url.QueryEscape(library)+"&kind="+kind+"&sort="+sort+"&limit=100")
					}
				}
			}
			return out
		}),
		"GET /v1/admin/attention":                                   exempt(reasonAdmin),
		"GET /v1/admin/console/accounts":                            exempt(reasonAdmin),
		"GET /v1/admin/console/job-kinds":                           exempt(reasonAdmin),
		"GET /v1/admin/console/stream-options":                      exempt(reasonAdmin),
		"GET /v1/admin/jobs":                                        exempt(reasonAdmin),
		"GET /v1/admin/libraries":                                   exempt(reasonAdmin),
		"GET /v1/admin/libraries/{id}":                              exempt(reasonAdmin),
		"GET /v1/admin/notifications/settings":                      exempt(reasonAdmin),
		"GET /v1/admin/playback-operations/{operationId}":           exempt(reasonAdmin),
		"GET /v1/admin/playback-sessions":                           exempt(reasonAdmin),
		"GET /v1/admin/telemetry":                                   exempt(reasonAdmin),
		"GET /v1/admin/telemetry/now":                               exempt(reasonAdmin),
		"GET /v1/admin/transcode/capacity":                          exempt(reasonAdmin),
		"GET /v1/albums/{id}/metadata/musicbrainz":                  exempt("owner-only metadata state; member refused before catalogue access (manual_metadata_test, musicbrainz_test)"),
		"GET /v1/albums/{id}/songs":                                 perEntity("/v1/albums/", "album", "/songs"),
		"GET /v1/artists/{id}/albums":                               perEntity("/v1/artists/", "artist", "/albums"),
		"GET /v1/books/{id}/chapters":                               perEntity("/v1/books/", "book", "/chapters"),
		"GET /v1/books/{id}/files":                                  perEntity("/v1/books/", "book", "/files"),
		"GET /v1/app-reports":                                       exempt("current owner-only receipts of explicitly submitted closed coded app journals; no catalogue identifiers, titles, paths or assets"),
		"GET /v1/app-reports/recipient":                             exempt("current session and verified device read only the owner's display name and opaque recipient revision; no catalogue lookup or content"),
		"GET /v1/feedback/capabilities":                             exempt("caller-authored feedback and reports; no catalogue lookup, scoped to the caller (notifications_routes_test)"),
		"GET /v1/items/{id}/analysis":                               perItem("/analysis"),
		"GET /v1/items/{id}/analysis/artifacts/{artifact}":          perItem("/analysis/artifacts/unknown"),
		"GET /v1/items/{id}/lyrics":                                 perItem("/lyrics"),
		"GET /v1/items/{id}/lyrics/{resource}/revisions/{revision}": perItem("/lyrics/unknown/revisions/1"),
		"GET /v1/items/{id}/metadata/manual":                        exempt("owner-only metadata state; member refused before catalogue access (manual_metadata_test, musicbrainz_test)"),
		"GET /v1/items/{id}/metadata/musicbrainz":                   exempt("owner-only metadata state; member refused before catalogue access (manual_metadata_test, musicbrainz_test)"),
		"GET /v1/items/{id}/prepared-versions":                      perItem("/prepared-versions"),
		"GET /v1/metadata/{kind}/{id}":                              exempt("owner-only metadata state; member refused before catalogue access (manual_metadata_test, musicbrainz_test)"),
		"GET /v1/notifications/capabilities":                        static("/v1/notifications/capabilities"),
		"GET /v1/preferences":                                       exempt(reasonNoTitles),

		"GET /v1/items/{id}/metadata/screen":                                 perItem("/metadata/screen"),
		"GET /v1/shows/{id}/metadata/screen":                                 perEntity("/v1/shows/", "show", "/metadata/screen"),
		"GET /v1/admin/live/sources/{id}/groups":                             exempt("owner-only line-up choices"),
		"GET /v1/admin/live/sources/{id}/channels":                           exempt("owner-only line-up choices"),
		"GET /v1/admin/storage/status":                                       exempt("owner-only storage status"),
		"GET /v1/admin/storage/usage":                                        exempt("owner-only folder totals and filesystem notices; no catalogue content"),
		"GET /v1/admin/metadata/attention":                                   exempt("owner-only metadata evidence"),
		"GET /v1/admin/metadata/attention/counts":                            exempt("owner-only metadata evidence"),
		"GET /v1/libraries/{id}/metadata/attention":                          perLibrary("/metadata/attention"),
		"GET /v1/items/{itemId}/subtitles":                                   perItem("/subtitles"),
		"GET /v1/items/{itemId}/playback/{session}/subtitles":                perItem("/playback/unknown/subtitles"),
		"GET /v1/admin/console/settings":                                     exempt(reasonAdmin),
		"GET /v1/admin/settings/configuration":                               static("/v1/admin/settings/configuration"),
		"GET /v1/admin/metadata/settings":                                    exempt(reasonAdmin),
		"GET /v1/admin/webhooks":                                             exempt(reasonAdmin),
		"GET /v1/admin/webhooks/{id}":                                        exempt(reasonAdmin),
		"GET /v1/admin/webhooks/{id}/deliveries":                             exempt(reasonAdmin),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}.vtt":          exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.ass": exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.ssa": exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.sup": exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.idx": exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.sub": exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/original.mks": exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/fonts":        exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/lines":        exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}/fonts/{font}": exempt(reasonGrant),
		"GET /v1/admin/ingestion/jobs/{id}":                                  static("/v1/admin/ingestion/jobs/unknown"),
		"GET /v1/downloads/requests/{id}":                                    static("/v1/downloads/requests/unknown"),
		"GET /v1/jobs/{id}":                                                  static("/v1/jobs/unknown"),
		"GET /v1/jobs/{id}/failures":                                         static("/v1/jobs/unknown/failures"),
		"GET /v1/containers/{kind}/{id}/personal-state":                      perEntity("/v1/containers/show/", "show", "/personal-state"),
		// Health, identity, device and server description.
		"GET ":                                               exempt("the web app shell and first-run setup page (installation.go); static, no catalogue data"),
		"GET /health/live":                                   exempt(reasonNoTitles),
		"GET /health/ready":                                  exempt(reasonNoTitles),
		"GET /v1/subtitle-fonts/cjk":                         exempt(reasonNoTitles),
		"GET /receiver/cast/":                                exempt("static Cast receiver page"),
		"GET /v1/attribution":                                exempt(reasonNoTitles),
		"GET /v1/auth/services-reachability":                 exempt(reasonNoTitles),
		"GET /v1/auth/capabilities":                          exempt(reasonNoTitles),
		"GET /v1/capabilities":                               exempt(reasonNoTitles),
		"GET /v1/devices":                                    exempt(reasonNoTitles),
		"GET /v1/direct":                                     exempt(reasonNoTitles),
		"GET /v1/direct/members":                             exempt(reasonAdmin),
		"GET /v1/direct/members/{id}/deletion-preview":       exempt(reasonAdmin),
		"GET /v1/direct/pin-recovery":                        exempt(reasonNoTitles),
		"GET /v1/direct/profiles/avatars":                    exempt(reasonNoTitles),
		"GET /v1/direct/profiles/{id}/deletion-preview":      exempt(reasonNoTitles),
		"GET /v1/direct/profiles/{id}/retirement":            {check: profileRetirementRestrictionCheck},
		"GET /v1/direct/profiles/{id}/restrictions":          exempt("the restriction document itself, no titles"),
		"GET /v1/direct/registration-policy":                 exempt(reasonNoTitles),
		"GET /v1/direct/sessions":                            exempt(reasonNoTitles),
		"GET /v1/direct/two-factor":                          exempt(reasonNoTitles),
		"GET /v1/me":                                         exempt(reasonNoTitles),
		"GET /v1/me/devices/current/capabilities":            exempt(reasonNoTitles),
		"GET /v1/profiles/{id}/avatar":                       exempt(reasonNoTitles),
		"GET /v1/rating-systems":                             exempt(reasonNoTitles),
		"GET /v1/readiness":                                  exempt(reasonNoTitles),
		"GET /v1/server":                                     exempt(reasonNoTitles),
		"GET /v1/setup/state":                                exempt(reasonNoTitles),
		"GET /v1/system":                                     exempt(reasonNoTitles),
		"GET /v1/networking/certificate":                     exempt(reasonNoTitles),
		"GET /v1/networking/claim":                           exempt(reasonNoTitles),
		"GET /v1/networking/remote":                          exempt(reasonNoTitles),
		"GET /v1/playback/delivery-policy":                   exempt(reasonNoTitles),
		"GET /v1/listening/preferences":                      exempt(reasonNoTitles),
		"GET /v1/downloads/settings":                         exempt(reasonNoTitles),
		"GET /v1/downloads/usage":                            exempt(reasonNoTitles),
		"GET /v1/downloads/receipt-keys":                     exempt(reasonNoTitles),
		"GET /v1/downloads/receipts/revocations":             exempt(reasonNoTitles),
		"GET /v1/cast/configuration":                         exempt(reasonNoTitles),
		"GET /v1/storage/mounts":                             exempt(reasonAdmin),
		"GET /v1/storage/rclone/bundled":                     exempt(reasonAdmin),
		"GET /v1/storage/sources":                            exempt(reasonAdmin),
		"GET /v1/storage/operations/{id}":                    exempt(reasonAdmin),
		"GET /v1/storage/source-operations/{id}":             exempt(reasonAdmin),
		"GET /v1/storage/sources/rclone/providers":           exempt(reasonAdmin),
		"GET /v1/storage/sources/rclone/config":              exempt(reasonAdmin),
		"GET /v1/storage/sources/rclone/operations/{id}":     exempt(reasonAdmin),
		"GET /v1/storage/sources/{id}/settings/read-profile": exempt(reasonAdmin),
		"GET /v1/storage/sources/{id}/folders":               exempt("owner-only source configuration/read: Owner plus ScopeAdmin, storageOwner is checked before any folder listing and only the saved source's own folders are returned"),
		"GET /v1/storage/sources/{id}/settings/advanced":     exempt("owner-only source configuration/read: Owner plus ScopeAdmin, storageOwner checked before the source's scan, cache and analysis settings are read"),
		"GET /v1/storage/sources/{id}/status":                exempt("owner-only source status: Owner plus ScopeAdmin, storageOwner checked before the source's state, libraries and live read counts are read"),
		"GET /v1/saved-pins/order":                           exempt("pin order: resource ids only, each read through the saved-resource routes probed below"),

		// Streams.
		"GET /v1/events":             exempt(reasonStream),
		"GET /v1/groups/{id}/events": exempt(reasonStream),

		// Grants, sessions, jobs and social objects created by admitted requests.
		"GET /v1/cast/bootstrap/{id}":         exempt(reasonGrant),
		"GET /v1/downloads/artifacts/{grant}": exempt(reasonGrant),
		"GET /v1/downloads/preparations/{id}": probe(func(m *restrictionEnv) []string {
			return prefixed("/v1/downloads/preparations/", m.preparations)
		}),
		"GET /v1/groups/{id}":                                   exempt(reasonGrant),
		"GET /v1/groups/{id}/queue":                             exempt(reasonGrant),
		"GET /v1/handoffs/{id}":                                 exempt(reasonGrant),
		"GET /v1/media/{grant}":                                 exempt(reasonGrant),
		"GET /v1/media/{grant}/audio":                           exempt(reasonGrant),
		"GET /v1/media/{grant}/audio-converted":                 exempt(reasonGrant),
		"GET /v1/media/{grant}/audio-index":                     exempt(reasonGrant),
		"GET /v1/media/subtitles/{grant}/{resource}/{revision}": exempt(reasonGrant),
		"GET /v1/media/{grant}/{file}":                          exempt(reasonGrant),
		"GET /v1/playback/sessions/{id}":                        exempt(reasonGrant),
		"GET /v1/playback/sessions/{id}/chapters":               exempt(reasonGrant),
		"GET /v1/queues/{id}":                                   exempt(reasonGrant),
		"GET /v1/queues/{id}/entry": probe(func(m *restrictionEnv) []string {
			out := []string{}
			for _, id := range m.hiddenItemSamples() {
				out = append(out, "/v1/queues/matrix-unknown/entry?itemId="+url.QueryEscape(id))
			}
			return out
		}),
		"GET /v1/queues/{id}/entries":                                   exempt(reasonGrant),
		"GET /v1/queues/{id}/window":                                    exempt(reasonGrant),
		"GET /v1/receivers/{id}/grants":                                 exempt(reasonGrant),
		"GET /v1/receivers/{id}/inbox":                                  exempt(reasonGrant),
		"GET /v1/receivers/{id}/inbox/wait":                             exempt(reasonGrant),
		"GET /v1/media/linear/{playbackId}/{generation}/{token}/{name}": exempt(reasonGrant + "; v1 capability handler, with live revocation proven by TestChannelsARestrictionMidPlayFencesMedia"),

		// Owner-only administration.
		"GET /v1/admin/sign-in":                                 exempt("owner-only sign-in kind and owner/move state; no catalogue lookup"),
		"GET /v1/admin/sign-in/move":                            exempt("owner-only account move progress; no catalogue lookup"),
		"GET /v1/admin/sign-in/move/waiting":                    exempt("owner-only waiting account ids, usernames and code expiry; no catalogue lookup; fresh initiating-owner proof after the read"),
		"GET /v1/admin/sign-in/policy":                          exempt("owner-only sign-in policy; no catalogue lookup"),
		"GET /v1/admin/api-keys":                                exempt(reasonAdmin),
		"GET /v1/admin/access/members/{id}":                     exempt(reasonAdmin),
		"GET /v1/admin/access/invitations/{id}":                 exempt(reasonAdmin),
		"GET /v1/admin/access/devices/{id}":                     exempt(reasonAdmin),
		"GET /v1/admin/access/devices":                          exempt(reasonAdmin),
		"GET /v1/admin/access/invitations":                      exempt(reasonAdmin),
		"GET /v1/admin/access/members":                          exempt(reasonAdmin),
		"GET /v1/admin/play-history":                            exempt(reasonAdmin),
		"GET /v1/admin/play-history/summary":                    exempt(reasonAdmin),
		"GET /v1/admin/play-history/dashboard":                  exempt(reasonAdmin),
		"GET /v1/admin/play-history/people":                     exempt(reasonAdmin),
		"GET /v1/admin/dashboard/report":                        exempt(reasonAdmin),
		"GET /v1/admin/dashboard/status":                        exempt(reasonAdmin),
		"GET /v1/admin/access/members/{id}/limits":              exempt(reasonAdmin),
		"GET /v1/admin/access/members/{id}/profiles":            exempt(reasonAdmin),
		"GET /v1/admin/analysis-operations":                     exempt(reasonAdmin),
		"GET /v1/admin/backups":                                 exempt(reasonAdmin),
		"GET /v1/admin/backups/settings":                        static("/v1/admin/backups/settings"),
		"GET /v1/admin/connectivity/policy":                     exempt(reasonAdmin),
		"GET /v1/admin/connectivity/status":                     exempt(reasonAdmin),
		"GET /v1/admin/diagnostics/bundle":                      exempt(reasonAdmin),
		"GET /v1/admin/diagnostics/capabilities":                exempt(reasonAdmin),
		"GET /v1/admin/diagnostics/client-logs":                 exempt(reasonAdmin),
		"GET /v1/admin/diagnostics/client-logs/{id}/download":   exempt(reasonAdmin),
		"GET /v1/admin/diagnostics/client-logs/{id}":            exempt(reasonAdmin),
		"GET /v1/admin/diagnostics/concurrency":                 exempt(reasonAdmin),
		"GET /v1/admin/diagnostics/pprof/{profile}":             exempt(reasonAdmin),
		"GET /v1/admin/dvr/recording-groups":                    exempt(reasonAdmin),
		"GET /v1/admin/dvr/recording-owners":                    exempt(reasonAdmin),
		"GET /v1/admin/dvr/recording-permissions":               exempt(reasonAdmin),
		"GET /v1/admin/dvr/settings":                            exempt(reasonAdmin),
		"GET /v1/admin/dvr/tuners":                              exempt(reasonAdmin),
		"GET /v1/admin/filesystem":                              exempt(reasonAdmin),
		"GET /v1/admin/libraries/{id}/inventory":                exempt(reasonAdmin),
		"GET /v1/admin/libraries/{id}/inventory-config":         exempt(reasonAdmin),
		"GET /v1/admin/libraries/{id}/settings":                 exempt(reasonAdmin),
		"GET /v1/admin/libraries/{id}/sonic-analysis":           exempt(reasonAdmin),
		"GET /v1/admin/libraries/{id}/sources/{source}/changes": exempt(reasonAdmin),
		"GET /v1/admin/library-channels":                        exempt(reasonAdmin),
		"GET /v1/admin/library-channels/block-presets":          exempt(reasonAdmin),
		"GET /v1/admin/library-channels/criteria":               exempt(reasonAdmin),
		"GET /v1/admin/library-channels/templates":              exempt(reasonAdmin),
		"GET /v1/admin/library-channels/{id}/health":            exempt(reasonAdmin),
		"GET /v1/admin/library-settings":                        exempt(reasonAdmin),
		"GET /v1/admin/live-sources":                            exempt(reasonAdmin),
		"GET /v1/admin/live-sources/{id}/channel-map":           exempt(reasonAdmin),
		"GET /v1/admin/live-sources/{id}/configuration":         exempt(reasonAdmin),
		"GET /v1/admin/live/settings":                           exempt(reasonAdmin),
		"GET /v1/admin/logs":                                    exempt(reasonAdmin),
		"GET /v1/admin/logs/events":                             exempt(reasonAdmin),
		"GET /v1/admin/logs/settings":                           exempt(reasonAdmin),
		"GET /v1/admin/maintenance/settings":                    exempt(reasonAdmin),
		"GET /v1/admin/metadata-providers":                      exempt(reasonAdmin),
		"GET /v1/admin/operations/{panel}":                      exempt(reasonAdmin),
		"GET /v1/admin/playback/diagnostics":                    exempt(reasonAdmin),
		// Registered by registerHardwareFacts with apikit.Owner + ScopeAdmin and a
		// constant answer; it classifies exactly like the capacity read beside it.
		"GET /v1/admin/playback/hardware":              exempt(reasonAdmin),
		"GET /v1/admin/segments":                       exempt(reasonAdmin),
		"GET /v1/admin/segments/titles":                exempt(reasonAdmin),
		"GET /v1/admin/segments/titles/{id}":           exempt(reasonAdmin),
		"GET /v1/admin/playback/history.csv":           exempt(reasonAdmin),
		"GET /v1/playback/controls":                    exempt("own sessions with current title visibility checked"),
		"GET /v1/admin/sessions/{id}/controls":         exempt("admin transport state without title or media capability"),
		"GET /v1/admin/sessions":                       exempt(reasonAdmin),
		"GET /v1/admin/storage-usage":                  exempt(reasonAdmin),
		"GET /v1/admin/support-report":                 exempt(reasonAdmin),
		"GET /v1/admin/missing-titles":                 exempt(reasonAdmin),
		"GET /v1/admin/updates":                        exempt(reasonAdmin),
		"GET /v1/library-kinds/{kind}/metadata-agents": exempt(reasonAdmin),

		// Catalogue: global lists.
		"GET /v1/bootstrap":   static("/v1/bootstrap"),
		"GET /v1/home":        static("/v1/home"),
		"GET /v1/home/layout": static("/v1/home/layout"),
		// The viewer's own saved lists (Home has its own route now).
		"GET /v1/content":               static("/v1/content?view=watchlist&limit=100", "/v1/content?view=favorites&limit=100", "/v1/content?view=playlists&limit=100"),
		"GET /v1/suggestions":           static("/v1/suggestions?limit=50"),
		"GET /v1/libraries":             static("/v1/libraries"),
		"GET /v1/me/library-navigation": static("/v1/me/library-navigation"),
		"GET /v1/personal-history":      static("/v1/personal-history"),
		"GET /v1/topshelf":              {probe: func(*restrictionEnv) []string { return []string{"/v1/topshelf"} }, viewer: "topshelf"},
		"GET /v1/search/history":        static("/v1/search/history"),
		"GET /v1/items": probe(func(m *restrictionEnv) []string {
			return []string{"/v1/items?ids=" + strings.Join(m.hiddenItemSamples(), ","), "/v1/items?limit=200"}
		}),
		"GET /v1/search":               probe(func(m *restrictionEnv) []string { return m.searchPaths() }),
		"GET /v1/people":               static("/v1/people?q=Person&limit=100"),
		"GET /v1/home/rows/{id}":       probe(func(m *restrictionEnv) []string { return m.homeRows() }),
		"GET /v1/saved-resources":      static("/v1/saved-resources?kind=collection", "/v1/saved-resources?kind=collection&pinned=true"),
		"GET /v1/saved-resources/{id}": probe(func(m *restrictionEnv) []string { return []string{"/v1/saved-resources/" + m.savedResource} }),
		"GET /v1/saved-resources/{id}/content": probe(func(m *restrictionEnv) []string {
			return []string{"/v1/saved-resources/" + m.savedResource + "/content"}
		}),
		"GET /v1/saved-resources/{id}/share-candidates": probe(func(m *restrictionEnv) []string {
			return []string{"/v1/saved-resources/" + m.savedResource + "/share-candidates"}
		}),
		"GET /v1/playlists/{id}":                  probe(func(m *restrictionEnv) []string { return []string{"/v1/playlists/" + m.playlist} }),
		"GET /v1/playlists/{id}/content":          probe(func(m *restrictionEnv) []string { return []string{"/v1/playlists/" + m.playlist + "/content"} }),
		"GET /v1/playlists/{id}/order":            probe(func(m *restrictionEnv) []string { return []string{"/v1/playlists/" + m.playlist + "/order"} }),
		"GET /v1/playlists/{id}/share-candidates": probe(func(m *restrictionEnv) []string { return []string{"/v1/playlists/" + m.playlist + "/share-candidates"} }),
		"GET /v1/downloads/preparations":          static("/v1/downloads/preparations"),
		"GET /v1/music/mixes":                     static("/v1/music/mixes"),
		"GET /v1/receivers":                       static("/v1/receivers"),
		"GET /v1/groups":                          static("/v1/groups"),

		// Catalogue: one title.
		"GET /v1/items/{id}":                          perItem(""),
		"GET /v1/items/{id}/detail":                   perItem("/detail"),
		"GET /v1/items/{id}/credits":                  perItem("/credits?group=cast"),
		"GET /v1/items/{id}/art/{kind}":               perItem("/art/poster"),
		"GET /v1/items/{id}/chapters/{chapter}/image": perItem("/chapters/0/image"),
		"GET /v1/items/{itemId}/download-options":     perItem("/download-options"),
		"GET /v1/items/{id}/listening":                perItem("/listening"),
		"GET /v1/books/{id}/timeline":                 perEntity("/v1/books/", "book", "/timeline?limit=10&positionMs=0"),
		"GET /v1/items/{id}/similar": probe(func(m *restrictionEnv) []string {
			var out []string
			for _, kind := range []string{"album", "book", "artist"} {
				out = append(out, perEntity("/v1/items/", kind, "/similar?limit=12").probe(m)...)
				out = append(out, perEntity("/v1/items/", kind, "/similar?by=sound&limit=12").probe(m)...)
			}
			return out
		}),
		"GET /v1/items/{id}/playback-offers":                  perItem("/playback-offers"),
		"GET /v1/items/{id}/recommendations":                  perItem("/recommendations"),
		"GET /v1/items/{id}/file-info":                        perItem("/file-info"),
		"GET /v1/items/{id}/file-info/groups":                 perItem("/file-info/groups"),
		"GET /v1/items/{id}/file-info/groups/members":         perItem("/file-info/groups/members?group=matrix"),
		"GET /v1/items/{id}/file-info/tracks":                 perItem("/file-info/tracks?occurrence=matrix&lane=audio"),
		"GET /v1/items/{id}/file-info/groups/head-tracks":     perItem("/file-info/groups/head-tracks?group=matrix&lane=audio"),
		"GET /v1/items/{id}/file-info/sources":                perItem("/file-info/sources"),
		"GET /v1/items/{id}/file-info/disc-titles":            perItem("/file-info/disc-titles?occurrence=matrix"),
		"GET /v1/file-info-queries/{queryId}":                 {check: fileInfoQueryRestrictionCheck},
		"GET /v1/items/{id}/recommendations/rows/{row}":       perItem("/recommendations/rows/more_like"),
		"GET /v1/items/{id}/source-availability":              perItem("/source-availability"),
		"GET /v1/items/{id}/trickplay":                        perItem("/trickplay"),
		"GET /v1/items/{id}/trickplay/{set}/thumbnails.vtt":   perItem("/trickplay/set/thumbnails.vtt"),
		"GET /v1/items/{id}/trickplay/{set}/tiles/{tile}.jpg": perItem("/trickplay/set/tiles/0.jpg"),
		"GET /v1/items/{itemId}/playback-options":             perItem("/playback-options"),
		"GET /v1/topshelf/art/{id}":                           {probe: func(m *restrictionEnv) []string { return prefixed("/v1/topshelf/art/", m.hiddenItemSamples()) }, viewer: "topshelf"},
		"GET /v1/metadata/{kind}/{id}/art/{role}": probe(func(m *restrictionEnv) []string {
			return prefixed("/v1/metadata/item/", m.hiddenItemSamples(), "/art/poster")
		}),
		"GET /v1/metadata/{kind}/{id}/preview": probe(func(m *restrictionEnv) []string {
			return prefixed("/v1/metadata/item/", m.hiddenItemSamples(), "/preview")
		}),

		// Catalogue: containers and people.
		"GET /v1/collections/{id}":                      perEntity("/v1/collections/", "collection", ""),
		"GET /v1/collections/{id}/items":                perEntity("/v1/collections/", "collection", "/items"),
		"GET /v1/shows/{id}/episodes":                   perEntity("/v1/shows/", "show", "/episodes"),
		"GET /v1/shows/{id}/seasons":                    perEntity("/v1/shows/", "show", "/seasons"),
		"GET /v1/shows/{id}/recommendations":            perEntity("/v1/shows/", "show", "/recommendations"),
		"GET /v1/shows/{id}/recommendations/rows/{row}": perEntity("/v1/shows/", "show", "/recommendations/rows/more_like"),
		"GET /v1/shows/{id}/metadata/tvdb":              perEntity("/v1/shows/", "show", "/metadata/tvdb"),
		"GET /v1/seasons/{id}/episodes":                 perEntity("/v1/seasons/", "season", "/episodes"),
		"GET /v1/people/{id}":                           probe(func(m *restrictionEnv) []string { return prefixed("/v1/people/", m.people) }),
		"GET /v1/people/{id}/portrait":                  probe(func(m *restrictionEnv) []string { return prefixed("/v1/people/", m.people, "/portrait") }),

		// Catalogue: one library.
		"GET /v1/libraries/{id}/artists":             perLibrary("/artists"),
		"GET /v1/libraries/{id}/books":               perLibrary("/books"),
		"GET /v1/libraries/{id}/browse":              perLibrary("/browse"),
		"GET /v1/libraries/{id}/browse-capabilities": perLibrary("/browse-capabilities"),
		"GET /v1/libraries/{id}/categories":          perLibrary("/categories"),
		"GET /v1/libraries/{id}/collections":         perLibrary("/collections"),
		"GET /v1/libraries/{id}/content":             probe(func(m *restrictionEnv) []string { return m.contentPaths() }),
		"GET /v1/libraries/{id}/discover":            perLibrary("/discover"),
		"GET /v1/libraries/{id}/episode-issues":      perLibrary("/episode-issues"),
		"GET /v1/libraries/{id}/facets":              probe(func(m *restrictionEnv) []string { return m.facetPaths() }),
		// Catalogue: lists across the viewer's libraries.
		"GET /v1/browse-capabilities": static("/v1/browse-capabilities?pivot=titles", "/v1/browse-capabilities?pivot=items"),
		"GET /v1/search/genres":       static("/v1/search/genres?limit=50"),
		"GET /v1/browse/facets": probe(func(m *restrictionEnv) []string {
			out := []string{}
			for _, field := range catalog.BrowseFacetFields() {
				out = append(out, "/v1/browse/facets?field="+field)
			}
			return out
		}),
		"GET /v1/libraries/{id}/inventory-status": perLibrary("/inventory-status"),
		// What plays when a library, an album, an artist or a book is started.
		"GET /v1/libraries/{id}/listening/selection": probe(func(m *restrictionEnv) []string {
			out := []string{}
			for _, library := range m.libraries {
				base := "/v1/libraries/" + library + "/listening/selection?mode=ordered&kind="
				out = append(out, base+"library&entityId="+url.QueryEscape(library))
				for _, kind := range []string{"album", "artist", "book"} {
					for _, id := range append(append([]string{}, m.hiddenEntities[kind]...), m.visibleEntities[kind]...) {
						out = append(out, base+kind+"&entityId="+url.QueryEscape(id))
					}
				}
			}
			return out
		}),
		"GET /v1/libraries/{id}/metadata/agent":       perLibrary("/metadata/agent"),
		"GET /v1/libraries/{id}/metadata/screen":      perLibrary("/metadata/screen"),
		"GET /v1/libraries/{id}/network-roots":        perLibrary("/network-roots"),
		"GET /v1/libraries/{id}/show-workspace":       probe(func(m *restrictionEnv) []string { return m.showWorkspacePaths() }),
		"GET /v1/libraries/{id}/shows":                perLibrary("/shows"),
		"GET /v1/libraries/{id}/strm-analysis-policy": perLibrary("/strm-analysis-policy"),

		// Live TV, guide and recordings: channels and recordings carry their own
		// restriction (channel_restriction_test, recordings_switch_test); the
		// matrix still checks that no catalogue title leaks through them.
		"GET /v1/guide":           static("/v1/guide?kind=live-source&start=2026-09-19T12:00:00Z&end=2026-09-19T14:00:00Z&timezone=UTC&limit=30"),
		"GET /v1/guide/channels":  static("/v1/guide/channels?kind=all&sort=number&offset=0&limit=50"),
		"GET /v1/guide/sources":   static("/v1/guide/sources"),
		"GET /v1/guide/on-now":    static("/v1/guide/on-now"),
		"GET /v1/guide/now-next":  static("/v1/guide/now-next"),
		"GET /v1/guide/airings":   static("/v1/guide/airings?start=2026-09-19T12:00:00Z&end=2026-09-20T12:00:00Z&search=Programme"),
		"GET /v1/guide/reminders": static("/v1/guide/reminders"),
		"GET /v1/dvr/summary":     static("/v1/dvr/summary"),
		"GET /v1/dvr/groups":      static("/v1/dvr/groups"),
		"GET /v1/dvr/recordings/{id}/image": probe(func(m *restrictionEnv) []string {
			return prefixed("/v1/dvr/recordings/", m.hiddenItemSamples(), "/image")
		}),
		"GET /v1/guide/programme-images/{generation}/{programme}": exempt("programme image rechecks live source access and rating restrictions; guide_images_test covers hidden and changed programmes"),
		"GET /v1/dvr":                 static("/v1/dvr"),
		"GET /v1/dvr/channels":        static("/v1/dvr/channels"),
		"GET /v1/dvr/storage":         static("/v1/dvr/storage"),
		"GET /v1/dvr/recordings/{id}": probe(func(m *restrictionEnv) []string { return prefixed("/v1/dvr/recordings/", m.hiddenItemSamples()) }),
		"GET /v1/dvr/recordings/{id}/delete-preview": probe(func(m *restrictionEnv) []string {
			return prefixed("/v1/dvr/recordings/", m.hiddenItemSamples(), "/delete-preview")
		}),
		"GET /v1/library-channels/{id}/logo": exempt("channel logo image; library channels apply the profile restriction to their schedule (channel_restriction_test)"),
	}
	addIntegrationRestrictionReads(routes)
	return routes
}

// restrictionReadPOSTs are the POST routes whose answer is catalogue content.
// POST /v1/app-reports is intentionally absent: it accepts only a closed coded
// journal and returns its receipt, never catalogue IDs or content. This does not
// classify or exempt any other route.
func restrictionReadPOSTs(m *restrictionEnv) map[string][]postProbe {
	browse := []postProbe{}
	if m != nil {
		for _, library := range m.libraries {
			for _, pivot := range []string{"movies", "shows", "episodes", "collections", "categories", "artists", "albums", "songs", "genres", "authors", "books", "series"} {
				browse = append(browse, postProbe{"/v1/libraries/" + library + "/browse", fmt.Sprintf(`{"pivot":%q,"limit":200}`, pivot)})
			}
		}
	}
	jobs := []postProbe{}
	if m != nil {
		jobs = append(jobs, postProbe{"/v1/jobs", fmt.Sprintf(`{"operationId":"matrix-job","command":"personal-state","selector":{"items":{"ids":[%q]}},"args":{"watched":true}}`, m.visibleMovie)})
	}
	across := []postProbe{}
	if m != nil {
		for _, pivot := range []string{"titles", "items"} {
			for _, group := range []string{`{"variant":"predicate","field":"watchlisted","operator":"equals","value":true}`, `{"variant":"predicate","field":"favorite","operator":"equals","value":true}`, `{"variant":"predicate","field":"lastPlayedAt","operator":"is-present","value":null}`} {
				across = append(across, postProbe{"/v1/browse", fmt.Sprintf(`{"pivot":%q,"query":%s,"limit":100}`, pivot, group)})
			}
			for _, person := range m.people {
				for _, field := range []string{"person", "cast", "crew"} {
					across = append(across, postProbe{"/v1/browse", fmt.Sprintf(`{"pivot":%q,"query":{"variant":"predicate","field":%q,"operator":"equals","value":%q},"limit":100}`, pivot, field, person)})
				}
			}
		}
	}
	pickers := []postProbe{}
	if m != nil {
		for _, kind := range []string{"playlist", "collection"} {
			pickers = append(pickers, postProbe{"/v1/home/destination-picker", fmt.Sprintf(`{"kind":%q,"limit":40,"itemIds":[%q,%q]}`, kind, m.visibleMovie, m.hiddenItems["movie"][0])})
		}
	}
	counts, acrossCounts := []postProbe{}, []postProbe{}
	if m != nil {
		for _, library := range m.libraries {
			counts = append(counts, postProbe{"/v1/libraries/" + library + "/browse/counts", `{"queries":[{"id":"clear","query":null},{"id":"rating","query":{"variant":"predicate","field":"year","operator":"at-least","value":1900}}]}`})
		}
		acrossCounts = append(acrossCounts, postProbe{"/v1/browse/counts", `{"pivot":"titles","queries":[{"id":"my-list","query":{"variant":"predicate","field":"watchlisted","operator":"equals","value":true}},{"id":"favorites","query":{"variant":"predicate","field":"favorite","operator":"equals","value":true}}]}`})
	}
	return map[string][]postProbe{"POST /v1/libraries/{id}/browse/counts": counts, "POST /v1/browse/counts": acrossCounts, "POST /v1/libraries/{id}/browse": browse, "POST /v1/browse": across, "POST /v1/jobs": jobs, "POST /v1/home/destination-picker": pickers}
}

func prefixed(prefix string, ids []string, suffix ...string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, prefix+id+strings.Join(suffix, ""))
	}
	return out
}

type restrictionEnv struct {
	routeInventory map[string]string

	downloads        *downloads.Service
	db               *sql.DB
	catalog          *catalog.Service
	fixtures         *catalogtest.Catalog
	names            catalogtest.Names
	itemsByID        map[string]catalogtest.Item
	principals       map[string]identity.Principal
	bulkAccess       catalog.BulkAccess
	visibilityOracle catalog.Viewer

	call            func(method, path, body, viewer string) (int, string)
	callWithin      func(method, path, viewer string, limit time.Duration) (int, string)
	libraries       []string
	hiddenItems     map[string][]string // item kind -> hidden ids
	hiddenEntities  map[string][]string // entity kind -> hidden ids
	visibleEntities map[string][]string
	visibleMovie    string
	people          []string
	playlist        string
	// playlistRevision is the fixture playlist's revision when the probes are
	// built, so a write probe reaches the action's own item checks instead of
	// stopping at the earlier revision fence.
	playlistRevision int64
	savedResource    string
	markers          []string
	statuses         map[string][]int
	marker           *regexp.Regexp
	// The restricted session's own device, and what was recorded in its name
	// before it was restricted: reports about titles it can no longer see, and
	// notices whose links lead to them.
	device  string
	reports []string
	// preparations are downloads the profile asked for before it was
	// restricted: one of a film now hidden from it, one of a film it may see.
	preparations []string
	// callAs sends a request with a bearer that is not one of the named
	// sessions (a personal access token).
	callAs func(token, method, path, body string) (int, string)
	tokens map[string]string
}

func (m *restrictionEnv) hiddenItemSamples() []string {
	out := []string{}
	kinds := make([]string, 0, len(m.hiddenItems))
	for kind := range m.hiddenItems {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		out = append(out, m.hiddenItems[kind][0])
	}
	return out
}

func (m *restrictionEnv) homeRows() []string {
	rows := []string{"continue", "new_on_server", "because_you_watched", "listen_next", "continue_books", "recommended", "trending_now", "community_watching"}
	for _, library := range m.libraries {
		rows = append(rows, "recent_"+library)
	}
	return prefixed("/v1/home/rows/", rows, "?limit=100")
}

func (m *restrictionEnv) searchPaths() []string {
	out := []string{}
	for _, term := range []string{"Word001", "Word002", "Volume", "Release", "Person", "movie", "episode"} {
		out = append(out, "/v1/search?q="+url.QueryEscape(term)+"&limit=100")
	}
	return out
}

// showWorkspacePaths opens the show workspace on hidden and visible shows.
func (m *restrictionEnv) showWorkspacePaths() []string {
	out := []string{}
	for _, library := range m.libraries {
		for _, id := range append(append([]string{}, m.hiddenEntities["show"]...), m.visibleEntities["show"]...) {
			out = append(out, "/v1/libraries/"+library+"/show-workspace?showId="+url.QueryEscape(id))
		}
	}
	return out
}

// contentPaths covers the library content page and its entity views, each
// opened on a hidden container and on a visible one.
func (m *restrictionEnv) contentPaths() []string {
	out := []string{}
	for _, library := range m.libraries {
		out = append(out, "/v1/libraries/"+library+"/content?limit=200")
		for _, view := range []string{"collection", "show", "season", "artist", "album", "book"} {
			for _, id := range append(append([]string{}, m.hiddenEntities[view]...), m.visibleEntities[view]...) {
				out = append(out, "/v1/libraries/"+library+"/content?limit=200&view="+view+"&entityId="+url.QueryEscape(id))
			}
		}
	}
	return out
}

func (m *restrictionEnv) facetPaths() []string {
	out := []string{}
	for _, library := range m.libraries {
		for _, field := range catalog.BrowseFacetFields() {
			out = append(out, "/v1/libraries/"+library+"/facets?field="+field)
		}
	}
	return out
}

// check runs one request as the restricted viewer.
func (m *restrictionEnv) check(t *testing.T, pattern, viewer, method, path, body string) {
	t.Helper()
	status, response := m.call(method, path, body, viewer)
	m.statuses[pattern] = append(m.statuses[pattern], status)
	if status == 401 && viewer == "restricted" {
		// CD-51: the session is valid; a refusal is 403 or 404, and a 401 would
		// send the person back to the sign-in screen.
		t.Errorf("%s: %s %s answered 401 to a valid restricted session: %s", pattern, method, path, strings.TrimSpace(response))
		return
	}
	if status >= 400 {
		return
	}
	if found := m.marker.FindString(response); found != "" {
		// PORTICO_MATRIX_DUMP=<dir> keeps each leaking answer for inspection.
		if dump := os.Getenv("PORTICO_MATRIX_DUMP"); dump != "" {
			_ = os.WriteFile(filepath.Join(dump, strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "_").Replace(path)+".json"), []byte(response), 0o600)
		}
		snippet := response
		if i := strings.Index(response, strings.Trim(found, "\"/?&=,: ")); i >= 0 {
			start, end := max(0, i-120), min(len(response), i+120)
			snippet = response[start:end]
		}
		t.Errorf("%s: %s %s answered %d to the restricted profile with a hidden identifier %q: …%s…", pattern, method, path, status, strings.Trim(found, "\"/?&=,: "), snippet)
	}
}

func buildRestrictionMatrix(t *testing.T) *restrictionEnv {
	t.Helper()
	ctx := context.Background()
	// Canonical: the analysis artifact store refuses a symlinked directory
	// (macOS's /var is a link to /private/var).
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "db")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	libraries, err := fixture.Generate(ctx, db, fixture.Tiny(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	catalogFixtures := catalogtest.New(t, db)
	if _, err = cat.ClassifyPendingRatings(ctx); err != nil {
		t.Fatal(err)
	}
	cat.Clock = func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }
	host, err := hosted.New(db, ident, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Every optional service a read route consults is wired, so a route answers
	// on its merits instead of "not configured".
	offline, err := downloads.New(downloads.Options{DB: db, OpenSource: func(_ context.Context, name string, _, _ int64) (io.ReadSeekCloser, error) { return os.Open(name) }})
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has no media files: analysis opens nothing, so a route
	// reaches its restriction check and then finds no artifact.
	analysis, err := mediaanalysis.New(mediaanalysis.Options{DB: db, Directory: filepath.Join(root, "analysis"),
		Open: func(context.Context, string, string) (mediaanalysis.Input, error) { return nil, sql.ErrNoRows },
		Decoder: func(mediaanalysis.Input) mediaanalysis.Decode {
			return func(context.Context, string, decoder.AnalysisSpec, func(io.Reader) error) error { return sql.ErrNoRows }
		}})
	if err != nil {
		t.Fatal(err)
	}
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	// A live source every member may see, whose guide holds a TV-MA programme
	// and a TV-Y one in the probed window: the Kids path (Channels spec §8.1).
	liveOwner := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "matrix-live", func(string, string) bool { return true }, nil
	}
	liveSource, err := live.Save(ctx, liveOwner, livechannels.SourceInput{ID: strings.Repeat("7a", 24), RequestID: strings.Repeat("7b", 24), Name: "Matrix antenna", TunerCount: 1,
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"m\" tvg-chno=\"5\",Matrix Five\nhttps://provider.invalid/m\n",
		Guide:    `<?xml version="1.0"?><tv><programme channel="m" start="20260919120000 +0000" stop="20260919130000 +0000"><title>MatrixAdultProgramme</title><rating system="VCHIP"><value>TV-MA</value></rating></programme><programme channel="m" start="20260919130000 +0000" stop="20260919140000 +0000"><title>MatrixKidsProgramme</title><rating system="VCHIP"><value>TV-Y</value></rating></programme></tv>`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO live_source_settings(source_id,viewer_access) VALUES(?,'server-members') ON CONFLICT(source_id) DO UPDATE SET viewer_access='server-members'`, liveSource.ID); err != nil {
		t.Fatal(err)
	}
	recordings, err := dvr.New(db, live, recordingaccess.Policy{Cached: host}.Durable)
	if err != nil {
		t.Fatal(err)
	}
	console := operations.New(db)
	scheduler := operations.NewScheduler(console)
	deps := Dependencies{Console: console, Access: AccessArea{Access: access.New(db)}, Scheduler: scheduler, DB: db, Identity: ident, Catalog: cat, Hosted: host, Playback: playback.New(db), Downloads: offline, Metadata: metadata.New(db, ""), Analysis: analysis, Lyrics: &lyrics.Service{DB: db}, LiveChannels: live, DVR: recordings}
	// The tool endpoint authenticates a personal access token and then calls
	// this same handler as that token's profile.
	deps.credentials = newCredentialGate()
	deps.self = &selfHandler{}
	handler := New(deps)
	t.Cleanup(func() {
		flush, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = FlushCredentialUse(flush, handler)
	})

	all := []string{libraries.Movies, libraries.Shows, libraries.Music, libraries.Books}
	allJSON, _ := json.Marshal(all)
	restriction := fixture.Restriction(libraries)
	narrowed, _ := json.Marshal(restriction.Libraries)
	blocked, _ := json.Marshal(restriction.BlockedLabels)
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('matrix-owner','matrix-owner',x'00','matrix-owner-profile',1)`); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	principals := map[string]identity.Principal{}
	for _, viewer := range []struct {
		name, account, profile, libraries string
	}{
		{"open", "matrix-open", "matrix-open-profile", string(allJSON)},
		{"restricted", "matrix-limited", "matrix-limited-profile", string(narrowed)},
	} {
		if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, viewer.account, viewer.account, viewer.profile); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO direct_memberships(account_id,role,allowed_libraries,revision,disabled) VALUES(?,'member',?,1,0)
 ON CONFLICT(account_id) DO UPDATE SET role='member',allowed_libraries=excluded.allowed_libraries,disabled=0`, viewer.account, string(allJSON)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position,allowed_libraries) VALUES(?,?,?,1,0,?)
 ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id,is_primary=1,deleted=0,allowed_libraries=excluded.allowed_libraries`,
			viewer.profile, viewer.account, viewer.name, viewer.libraries); err != nil {
			t.Fatal(err)
		}
		envelope, issueErr := ident.Issue(viewer.account, viewer.profile, "local", "member", 1)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		tokens[viewer.name] = envelope.AccessToken
		p, authErr := ident.Authenticate(envelope.AccessToken)
		if authErr != nil {
			t.Fatal(authErr)
		}
		principals[viewer.name] = p
	}
	restricted := principals["restricted"]
	key := identity.PersonalKey(restricted.Viewer)

	// The restricted profile's own history, lists and saved things reach hidden
	// titles too: state recorded before the restriction, or on a title that has
	// since been re-rated. Copy one fixture profile's personal state.
	for _, q := range []string{
		`INSERT INTO personal_items(profile_id,item_id,watched,favorite,watchlisted,last_played_at,revision) SELECT ?,item_id,watched,favorite,watchlisted,last_played_at,revision FROM personal_items WHERE profile_id='fixture-profile-000'`,
		`INSERT INTO progress(profile_id,item_id,position,playback_id) SELECT ?,item_id,position,playback_id FROM progress WHERE profile_id='fixture-profile-000'`,
		`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT ?,library_id,item_id,updated_at,state FROM progress_activity WHERE profile_id='fixture-profile-000'`,
	} {
		if _, err = db.Exec(q, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,blocked_labels,allow_unrated,revision) VALUES(?,?,?,0,1)`,
		restricted.ProfileID, restriction.MaximumAge, string(blocked)); err != nil {
		t.Fatal(err)
	}
	// An approved tvOS device of the restricted account, for the Top Shelf token.
	if _, err = db.Exec(`INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('matrix-device','local',?,'matrixinstallation0000000000000000001','Apple TV','tvos','portico','1',?,?)`,
		restricted.AccountID, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	restrictions, err := ident.ViewerRestrictions(ctx, restricted)
	if err != nil {
		t.Fatal(err)
	}
	oracle := catalog.Viewer{Profile: key, Libraries: restriction.Libraries, Restrictions: restrictions}
	open := catalog.Viewer{Profile: identity.PersonalKey(principals["open"].Viewer), Libraries: all}
	// The permission oracle reads compact item and membership facts. Publish
	// the fixture before deriving its hidden/visible marker sets.
	catalogFixtures.Drain()

	m := &restrictionEnv{visibilityOracle: oracle, downloads: offline, db: db, catalog: cat, fixtures: catalogFixtures, names: catalogtest.Names{}, itemsByID: map[string]catalogtest.Item{}, principals: principals, bulkAccess: (Dependencies{DB: db, Identity: ident, Hosted: host}).bulkAccess, statuses: map[string][]int{}, libraries: all, hiddenItems: map[string][]string{}, hiddenEntities: map[string][]string{}, visibleEntities: map[string][]string{}}
	m.routeInventory = restrictionRouteInventory(t, deps.self.registry, routeLanes)
	rows, err := db.Query(`SELECT e.id,pid(e.public_id),k.name,i.source_key FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_identities i ON i.public_id=e.public_id WHERE k.playable=1 ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id, public, kind, key string
	}
	items := []row{}
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.public, &r.kind, &r.key); err != nil {
			t.Fatal(err)
		}
		items = append(items, r)
		itemID, _ := strconv.ParseInt(r.id, 10, 64)
		fixtureItem := catalogtest.Item{ID: itemID, Public: r.public}
		m.names[r.key] = fixtureItem
		m.itemsByID[r.public] = fixtureItem
	}
	rows.Close()
	hidden := []string{}
	for _, item := range items {
		if cat.VisibleItem(ctx, open, item.public) != nil {
			continue // not visible to anyone: not a meaningful marker
		}
		switch err := cat.VisibleItem(ctx, oracle, item.public); {
		case errors.Is(err, sql.ErrNoRows):
			m.hiddenItems[item.kind] = append(m.hiddenItems[item.kind], item.public)
			hidden = append(hidden, item.public)
		case err != nil:
			t.Fatal(err)
		case item.kind == "movie" && m.visibleMovie == "":
			m.visibleMovie = item.public
		}
	}
	entities, err := db.Query(`SELECT e.id,pid(e.public_id),k.name,i.source_key FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_identities i ON i.public_id=e.public_id WHERE k.browsable=1 ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	type entity struct {
		id, public, kind, key string
	}
	all2 := []entity{}
	for entities.Next() {
		var e entity
		if err = entities.Scan(&e.id, &e.public, &e.kind, &e.key); err != nil {
			t.Fatal(err)
		}
		all2 = append(all2, e)
		entityID, _ := strconv.ParseInt(e.id, 10, 64)
		fixtureItem := catalogtest.Item{ID: entityID, Public: e.public}
		m.names[e.key] = fixtureItem
		m.itemsByID[e.public] = fixtureItem
	}
	entities.Close()
	for _, e := range all2 {
		if cat.VisibleEntity(ctx, open, e.kind, e.public) != nil {
			continue
		}
		switch err := cat.VisibleEntity(ctx, oracle, e.kind, e.public); {
		case errors.Is(err, sql.ErrNoRows):
			m.hiddenEntities[e.kind] = append(m.hiddenEntities[e.kind], e.public)
			hidden = append(hidden, e.public)
		case err != nil:
			t.Fatal(err)
		default:
			m.visibleEntities[e.kind] = append(m.visibleEntities[e.kind], e.public)
		}
	}
	// Libraries the profile is not given are hidden too.
	for _, library := range all {
		if !oracle.AllowsLibrary(library) {
			hidden = append(hidden, library)
		}
	}
	if len(m.hiddenItems["movie"]) == 0 || m.visibleMovie == "" || len(m.hiddenItems) < 3 {
		t.Fatalf("fixture gives no meaningful split: hidden %v, visible movie %q", m.hiddenItems, m.visibleMovie)
	}
	// People credited on hidden titles.
	credits, err := db.Query(`SELECT DISTINCT p.token FROM catalog_credits c JOIN catalog_people p ON p.id=c.person_id JOIN catalog_entities e ON e.id=c.entity_id WHERE e.public_id IN(SELECT pid_blob(value) FROM json_each(?)) ORDER BY p.token LIMIT 6`, mustJSON(hidden))
	if err != nil {
		t.Fatal(err)
	}
	for credits.Next() {
		var id string
		if err = credits.Scan(&id); err != nil {
			t.Fatal(err)
		}
		m.people = append(m.people, id)
	}
	credits.Close()

	// A playlist and a saved collection of the restricted profile's own, each
	// holding a hidden and a visible title.
	now := time.Now().UTC().Format(time.RFC3339)
	m.playlist, m.savedResource = "matrix-playlist", "matrix-saved"
	actor := catalog.ResourceActor{Authority: restricted.Authority, AccountID: restricted.AccountID, ProfileID: restricted.ProfileID}
	playlistName := "Mixed"
	createdPlaylist, err := cat.MutatePlaylist(actor, "", "create", "", catalog.PlaylistMutation{OperationID: "matrix-playlist-create", Name: &playlistName}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.playlist = createdPlaylist.PlaylistID
	addedHidden, err := cat.MutatePlaylist(actor, m.playlist, "add", "", catalog.PlaylistMutation{OperationID: "matrix-playlist-hidden", ExpectedRevision: createdPlaylist.Revision, ItemID: m.hiddenItems["movie"][0]}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cat.MutatePlaylist(actor, m.playlist, "add", "", catalog.PlaylistMutation{OperationID: "matrix-playlist-visible", ExpectedRevision: addedHidden.Revision, ItemID: m.visibleMovie}, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT revision FROM catalog_playlists WHERE token=?`, m.playlist).Scan(&m.playlistRevision); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES(?,'collection',?,?,?,?,'Mixed',?)`, []any{m.savedResource, key, restricted.Authority, restricted.AccountID, restricted.ProfileID, now}},
		{`INSERT INTO saved_resource_entries(id,resource_id,item_id) VALUES('matrix-saved-1',?,?),('matrix-saved-2',?,?)`, []any{m.savedResource, m.itemsByID[m.hiddenItems["movie"][0]].ID, m.savedResource, m.itemsByID[m.visibleMovie].ID}},
	} {
		if _, err = db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	// Reports the profile sent while it could still see the title, and one
	// about a title it can see.
	before := func(context.Context, *sql.Tx, string) error { return nil }
	for i, item := range []string{m.hiddenItems["movie"][0], m.visibleMovie} {
		filed, err := console.SubmitFeedback(ctx, restricted, before, operations.FeedbackSubmission{OperationID: fmt.Sprint("matrix-feedback-", i), Kind: "library", Category: "wrong-details", Message: fmt.Sprint("The year is wrong on this one, report ", i), ItemID: item})
		if err != nil {
			t.Fatal(err)
		}
		m.reports = append(m.reports, filed.Report.ID)
	}
	// Downloads asked for while the film could still be seen: the claim keeps
	// the film's name with it, as every download does.
	for i, item := range []string{m.hiddenItems["movie"][0], m.visibleMovie} {
		var title, library string
		if err = db.QueryRow(`SELECT e.title,cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&title, &library); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprint("matrix-preparation-", i)
		if _, err = db.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,item_public,library_id,title_json,quality,origin,state,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,'original','item','queued',?,?)`,
			id, operations.ViewerKey(restricted), restricted.Authority, restricted.AccountID, restricted.ProfileID, m.itemsByID[item].ID, item, library, mustJSON(map[string]any{"kind": "movie", "title": title}), time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		m.preparations = append(m.preparations, id)
	}
	// Notices in the profile's inbox whose links lead to a hidden film, a
	// hidden show and a library it is not given, as a message the owner sent
	// to everyone would, and one that leads somewhere it may go.
	links := [][]notify.Action{
		{{Kind: "navigate", Label: "Open", Target: &notify.NavigateTarget{View: "item", EntityID: m.hiddenItems["movie"][0]}}},
		{{Kind: "navigate", Label: "Open", Target: &notify.NavigateTarget{View: "item", EntityID: m.visibleMovie}}},
	}
	if shows := m.hiddenEntities["show"]; len(shows) > 0 {
		links = append(links, []notify.Action{{Kind: "navigate", Label: "Open", Target: &notify.NavigateTarget{View: "show", EntityID: shows[0]}}})
	}
	for _, library := range all {
		if !oracle.AllowsLibrary(library) {
			links = append(links, []notify.Action{
				{Kind: "navigate", Label: "Open", Target: &notify.NavigateTarget{View: "library", EntityID: library}},
				{Kind: "navigate", Label: "Browse", Target: &notify.NavigateTarget{View: "search", LibraryID: library}},
			})
		}
	}
	if len(links) < 3 {
		t.Fatalf("fixture gives the inbox too few hidden destinations: %d", len(links))
	}
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		for i, actions := range links {
			if _, err := notify.Raise(tx, time.Now().UnixMilli(), notify.Draft{Audience: notify.AudienceProfile, Scope: notify.ProfileScope(restricted.Authority, restricted.AccountID, restricted.ProfileID),
				Severity: notify.SeverityInfo, Source: notify.SourceBroadcast, Category: "server.message", Title: "From the owner", Body: "Have a look at this.", Actions: actions, DedupeKey: fmt.Sprint("matrix-link-", i)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	hidden = append(hidden, "MatrixAdultProgramme")
	// A hidden title's name gives it away as surely as its identifier does.
	// Every hidden title whose name no other title shares is a marker too, so
	// a route that drops the id but still says the name (in a message, a
	// notice, a label) fails the same way.
	named, err := db.Query(`SELECT e.title FROM catalog_entities e WHERE e.public_id IN(SELECT pid_blob(value) FROM json_each(?)) AND length(e.title)>12 AND (SELECT count(*) FROM catalog_entities o WHERE o.title=e.title)=1 ORDER BY e.id`, mustJSON(hidden))
	if err != nil {
		t.Fatal(err)
	}
	names := 0
	for named.Next() {
		var title string
		if err = named.Scan(&title); err != nil {
			t.Fatal(err)
		}
		hidden = append(hidden, title)
		names++
	}
	named.Close()
	if names < 10 {
		t.Fatalf("fixture gives only %d hidden titles with a name of their own", names)
	}
	m.markers = hidden
	quoted := make([]string, 0, len(hidden))
	for _, id := range hidden {
		quoted = append(quoted, regexp.QuoteMeta(id))
	}
	sort.Slice(quoted, func(i, j int) bool { return len(quoted[i]) > len(quoted[j]) })
	m.marker = regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(?:` + strings.Join(quoted, "|") + `)(?:$|[^A-Za-z0-9_-])`)

	catalogFixtures.Drain()
	m.callWithin = func(method, path, viewer string, limit time.Duration) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		r := httptest.NewRequest(method, path, nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+tokens[viewer])
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	m.tokens = tokens
	m.callAs = func(token, method, path, body string) (int, string) {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if err = db.QueryRow(`SELECT b.device_id FROM authorization_family_tokens t JOIN identity_device_families b ON b.family_id=t.family_id WHERE t.token_hash=?`, restricted.Hash).Scan(&m.device); err != nil {
		t.Fatal(err)
	}
	m.call = func(method, path, body, viewer string) (int, string) {
		if viewer == "topshelf" && tokens["topshelf"] == "" {
			// The Top Shelf extension's token, minted by the restricted session.
			r := httptest.NewRequest("POST", "/v1/topshelf/token", bytes.NewBufferString(`{"installationId":"matrixinstallation0000000000000000001"}`))
			r.Header.Set("Authorization", "Bearer "+tokens["restricted"])
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			var minted TopShelfToken
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &minted) != nil {
				t.Fatalf("top shelf token: %d %s", w.Code, w.Body.String())
			}
			tokens["topshelf"] = minted.Token
		}
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+tokens[viewer])
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}

	// Controls: the restricted viewer is a working viewer, and the hidden set
	// is really served to the open viewer, so an absence means something.
	for _, path := range []string{"/v1/home", "/v1/libraries", "/v1/items/" + m.visibleMovie + "/detail"} {
		if status, response := m.call("GET", path, "", "restricted"); status != 200 {
			t.Fatalf("control: restricted viewer %s: %d %s", path, status, response)
		}
	}
	if status, response := m.call("GET", "/v1/libraries/"+libraries.Movies+"/content?limit=200", "", "open"); status != 200 || !m.marker.MatchString(response) {
		t.Fatalf("control: the open viewer does not see the hidden titles (%d)", status)
	}
	// The inbox really holds the links, and the reports really name the film:
	// read with the restriction lifted they are there, so their absence below
	// is the restriction at work and not an empty fixture.
	var stored int
	if err = db.QueryRow(`SELECT count(*) FROM notification_records WHERE actions LIKE '%'||?||'%'`, m.hiddenItems["movie"][0]).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("control: the hidden link is not in the inbox: %d %v", stored, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM console_reports WHERE item_id=?`, m.itemsByID[m.hiddenItems["movie"][0]].ID).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("control: the reports about the hidden film are not stored: %d %v", stored, err)
	}
	if status, response := m.call("GET", "/v1/notifications/inbox?limit=100", "", "restricted"); status != 200 || !strings.Contains(response, m.visibleMovie) || !strings.Contains(response, "From the owner") {
		t.Fatalf("control: the restricted viewer's inbox does not carry its visible link (%d) %s", status, response)
	}
	if status, response := m.call("GET", "/v1/feedback/reports?limit=100", "", "restricted"); status != 200 || !strings.Contains(response, m.visibleMovie) {
		t.Fatalf("control: the restricted viewer does not get its own reports (%d) %s", status, response)
	}
	if status, response := m.call("GET", "/v1/downloads/preparations", "", "restricted"); status != 200 || !strings.Contains(response, m.preparations[1]) {
		t.Fatalf("control: the restricted viewer does not get its own download of a visible film (%d) %s", status, response)
	}
	guideProbe := "/v1/guide?kind=live-source&start=2026-09-19T12:00:00Z&end=2026-09-19T14:00:00Z&timezone=UTC&limit=30"
	if status, response := m.call("GET", guideProbe, "", "open"); status != 200 || !strings.Contains(response, "MatrixAdultProgramme") {
		t.Fatalf("control: the open viewer does not see the live guide (%d) %s", status, response)
	}
	if status, response := m.call("GET", guideProbe, "", "restricted"); status != 200 || !strings.Contains(response, "MatrixKidsProgramme") {
		t.Fatalf("control: the restricted viewer does not see the live guide (%d) %s", status, response)
	}
	// The restricted profile's visibility classes are built by the server's
	// background rebuilder after first use; the matrix probes a server whose
	// classes serve, not the build window (which answers "building" for every
	// id alike). Build them here, then publish them.
	effective := catalog.Viewer{Restrictions: restrictions}.EffectiveRestrictions()
	for _, library := range m.libraries {
		if err := cat.RebuildVisibilityClass(ctx, library, effective); err != nil {
			t.Fatal(err)
		}
	}
	catalogFixtures.Drain()
	prepareIntegrationRestrictionMatrix(t, m)
	return m
}

func mustJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
