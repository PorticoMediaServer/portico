package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
)

// SEC-02 / BE-SRV-01 / BE-MEDIA-04 / ARCH-SRV-08, part two: the rest of the
// route table, and existence.
//
// TestRestrictedProfileRouteMatrix proves that no GET route shows a restricted
// profile a hidden identifier. Three things it can't see are proven here:
//
//  1. Every non-GET route is classified. The walk uses the actual fixture's API
//     registrations and non-API infrastructure, so a new write route
//     fails until someone decides its restriction story. Owner administration
//     (/v1/admin/…) is not listed by hand: every such route is called as the
//     restricted member and must refuse.
//  2. Hidden ≡ absent. A title, container or library the profile may not see
//     must be indistinguishable from one that doesn't exist: every probe that
//     names a hidden id is sent again with an id that doesn't exist, and the
//     status and error code must match. This runs over the read matrix's GET
//     probes as well as the write probes, so no route can answer 403 for "exists
//     but restricted" where an absent id answers 404.
//  3. No effect. A refused write changes nothing: after every probe, no row
//     anywhere in the database references a hidden id that didn't before.
func TestRestrictedProfileWriteRouteMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	m := buildRestrictionMatrix(t)
	routeLanes := m.routeInventory
	routes := restrictionWriteRoutes()

	// 1. Exhaustive classification of the non-GET routes.
	for pattern := range routeLanes {
		if strings.HasPrefix(pattern, "GET ") || ownerAdministration(pattern) {
			continue
		}
		if _, ok := routes[pattern]; !ok {
			t.Errorf("%s has no restriction classification: add it to restrictionWriteRoutes (restriction_writes_test.go) with probes that name hidden ids, or the reason it can't reach catalogue content", pattern)
		}
	}
	for pattern := range routes {
		if _, ok := routeLanes[pattern]; !ok {
			t.Errorf("restrictionWriteRoutes lists %s, which the router does not register", pattern)
		}
		if ownerAdministration(pattern) {
			t.Errorf("%s is owner administration; it is probed by rule and must not be listed", pattern)
		}
	}

	before := hiddenReferenceDigest(t, m)

	// 2a. Owner administration refuses the member outright, whatever it names.
	admin := []string{}
	for pattern := range routeLanes {
		if ownerAdministration(pattern) {
			admin = append(admin, pattern)
		}
	}
	sort.Strings(admin)
	for _, pattern := range admin {
		method, path, _ := strings.Cut(pattern, " ")
		for _, id := range m.hiddenSamples() {
			concrete := pathParameter.ReplaceAllString(path, url.PathEscape(id))
			status, body := m.call(method, concrete, "{}", "restricted")
			if status != 403 && status != 404 {
				t.Errorf("%s: %s %s answered %d to a member profile; owner administration must refuse (403): %s", pattern, method, concrete, status, clip(body))
			}
			if found := m.marker.FindString(body); found != "" {
				t.Errorf("%s: refusal carries a hidden identifier %q", pattern, found)
			}
		}
	}

	// 2b. Content writes: hidden ≡ absent, and never a hidden identifier.
	patterns := make([]string, 0, len(routes))
	for pattern := range routes {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	probed := 0
	for _, pattern := range patterns {
		route := routes[pattern]
		if route.check != nil {
			t.Run(pattern, route.check)
			continue
		}
		if route.probes == nil {
			if route.reason == "" {
				t.Errorf("%s: an exemption needs a reason", pattern)
			}
			continue
		}
		for _, p := range route.probes(m) {
			probed++
			m.compareHiddenAbsent(t, pattern, p)
		}
	}

	// 2c. The read matrix's GET probes: hidden ≡ absent.
	reads := restrictionRoutes()
	readPatterns := make([]string, 0, len(reads))
	for pattern := range reads {
		readPatterns = append(readPatterns, pattern)
	}
	sort.Strings(readPatterns)
	for _, pattern := range readPatterns {
		route := reads[pattern]
		if route.probe == nil || route.viewer != "" {
			continue
		}
		for _, path := range route.probe(m) {
			for _, id := range m.hiddenIn(path) {
				probed++
				m.compareHiddenAbsent(t, pattern, idProbe{hidden: id, method: "GET", path: path})
			}
		}
	}

	// 2d. The tool endpoint, as the restricted profile's own token.
	t.Run("tools", func(t *testing.T) { m.toolSecurity(t) })

	// 3. No effect.
	if after := hiddenReferenceDigest(t, m); after != before {
		t.Errorf("refused writes changed rows that reference hidden ids:\nbefore %s\nafter  %s", before, after)
	}
	t.Logf("%d hidden-vs-absent comparisons over %d classified write routes and %d owner routes", probed, len(routes), len(admin))
}

// ownerAdministration is the rule for owner-only routes: everything under
// /v1/admin/. A member profile is refused before any catalogue read.
func ownerAdministration(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	return strings.HasPrefix(path, "/v1/admin/")
}

// idProbe is one request that names a hidden id somewhere in its path or
// body. The same request is sent with that id replaced by one that doesn't
// exist; both answers must agree.
type idProbe struct {
	hidden             string
	method, path, body string
}

func (p idProbe) with(id string) idProbe {
	return idProbe{hidden: id, method: p.method, path: strings.ReplaceAll(p.path, p.hidden, id), body: strings.ReplaceAll(p.body, p.hidden, id)}
}

// absentTwin names something with the same shape as id that doesn't exist.
func absentTwin(id string) string {
	if guid, ok := absentIntegrationGUID(id); ok {
		return guid
	}
	return id + "-absent"
}

func (m *restrictionEnv) compareHiddenAbsent(t *testing.T, pattern string, p idProbe) {
	t.Helper()
	hiddenStatus, hiddenBody := m.call(p.method, p.path, p.body, "restricted")
	absent := p.with(absentTwin(p.hidden))
	absentStatus, absentBody := m.call(absent.method, absent.path, absent.body, "restricted")
	if hiddenStatus == 401 {
		t.Errorf("%s: %s %s answered 401 to a valid restricted session: %s", pattern, p.method, p.path, clip(hiddenBody))
	}
	if hiddenStatus >= 500 {
		t.Errorf("%s: %s %s answered %d: %s", pattern, p.method, p.path, hiddenStatus, clip(hiddenBody))
	}
	if found := m.marker.FindString(hiddenBody); found != "" && hiddenStatus >= 400 {
		t.Errorf("%s: %s %s refusal carries a hidden identifier %q: %s", pattern, p.method, p.path, found, clip(hiddenBody))
	}
	hiddenCode, absentCode := bodyErrorCode(hiddenBody), bodyErrorCode(absentBody)
	if hiddenStatus != absentStatus || hiddenCode != absentCode {
		t.Errorf("%s: a hidden id is distinguishable from an absent one: %s %s answered %d %q, the absent twin %d %q", pattern, p.method, p.path, hiddenStatus, hiddenCode, absentStatus, absentCode)
		return
	}
	// Both accepted (a batch or an asynchronous job that skips what it can't
	// reach): the answers must say the same thing about the two ids. Whether
	// anything was written for the hidden one is the no-effect check's job.
	if hiddenStatus < 300 {
		h, a := normalizedAnswer(hiddenBody, p.hidden), normalizedAnswer(absentBody, absent.hidden)
		if h != a {
			t.Errorf("%s: %s %s answers a hidden id differently from an absent one:\nhidden %s\nabsent %s", pattern, p.method, p.path, clip(h), clip(a))
		}
	}
}

// hiddenAnswerVolatile are answer fields that differ between any two requests.
var hiddenAnswerVolatile = map[string]bool{"jobId": true, "batchId": true, "viewerFence": true, "receipt": true, "receipts": true, "revision": true, "operationId": true, "createdAt": true, "updatedAt": true, "at": true, "expiresAt": true, "serverId": true, "requestId": true, "etag": true}

// normalizedAnswer is a JSON answer with the probed id replaced by a
// placeholder and volatile fields dropped, re-encoded canonically.
func normalizedAnswer(body, id string) string {
	var v any
	if json.Unmarshal([]byte(strings.ReplaceAll(body, id, "ID")), &v) != nil {
		return strings.ReplaceAll(body, id, "ID")
	}
	var strip func(any) any
	strip = func(x any) any {
		switch t := x.(type) {
		case map[string]any:
			for k := range t {
				if hiddenAnswerVolatile[k] {
					delete(t, k)
				} else {
					t[k] = strip(t[k])
				}
			}
		case []any:
			for i := range t {
				t[i] = strip(t[i])
			}
		}
		return x
	}
	raw, _ := json.Marshal(strip(v))
	return string(raw)
}

func bodyErrorCode(body string) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &envelope) == nil {
		return envelope.Error.Code
	}
	return ""
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// hiddenSamples is one hidden id of every kind: items, containers and the
// libraries the profile isn't given.
func (m *restrictionEnv) hiddenSamples() []string {
	out := m.hiddenItemSamples()
	kinds := make([]string, 0, len(m.hiddenEntities))
	for kind := range m.hiddenEntities {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		out = append(out, m.hiddenEntities[kind][0])
	}
	return append(out, m.hiddenLibraries()...)
}

func (m *restrictionEnv) hiddenLibraries() []string {
	out := []string{}
	for _, library := range m.libraries {
		for _, marker := range m.markers {
			if marker == library {
				out = append(out, library)
			}
		}
	}
	return out
}

// hiddenIn lists the hidden ids a probe path names (a path segment or a query
// value equal to a hidden id).
func (m *restrictionEnv) hiddenIn(path string) []string {
	hidden := map[string]bool{}
	for _, id := range m.markers {
		hidden[id] = true
	}
	found := map[string]bool{}
	base, query, _ := strings.Cut(path, "?")
	for _, segment := range strings.Split(base, "/") {
		if s, err := url.PathUnescape(segment); err == nil && hidden[s] {
			found[s] = true
		}
	}
	if values, err := url.ParseQuery(query); err == nil {
		for _, list := range values {
			for _, value := range list {
				for _, part := range strings.Split(value, ",") {
					if hidden[part] {
						found[part] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(found))
	for id := range found {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// hiddenReferenceDigest counts, for every table column that holds a catalogue
// id, the rows that reference a hidden id. A refused write must not move any
// of these counts.
func hiddenReferenceDigest(t *testing.T, m *restrictionEnv) string {
	t.Helper()
	hidden := mustJSON(m.markers)
	tables, err := m.db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '%_fts%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for tables.Next() {
		var name string
		if err = tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	tables.Close()
	idColumns := map[string]bool{"item_id": true, "media_id": true, "entity_id": true, "show_id": true, "season_id": true, "album_id": true, "artist_id": true, "book_id": true, "container_id": true, "library_id": true, "collection_id": true}
	parts := []string{}
	for _, table := range names {
		columns, err := m.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		cols := []string{}
		for columns.Next() {
			var column string
			if err = columns.Scan(&column); err != nil {
				t.Fatal(err)
			}
			if idColumns[column] {
				cols = append(cols, column)
			}
		}
		columns.Close()
		for _, column := range cols {
			var n int
			q := fmt.Sprintf(`SELECT count(*) FROM %q WHERE %q IN(SELECT value FROM json_each(?))`, table, column)
			if err := m.db.QueryRow(q, hidden).Scan(&n); err != nil && err != sql.ErrNoRows {
				t.Fatalf("%s.%s: %v", table, column, err)
			}
			if n > 0 {
				parts = append(parts, fmt.Sprintf("%s.%s=%d", table, column, n))
			}
		}
	}
	return strings.Join(parts, " ")
}

type writeRoute struct {
	check  func(*testing.T)
	probes func(m *restrictionEnv) []idProbe
	reason string
}

func writeExempt(reason string) writeRoute { return writeRoute{reason: reason} }
func writeProbes(f func(m *restrictionEnv) []idProbe) writeRoute {
	return writeRoute{probes: f}
}

// perHidden builds one probe per hidden sample of the given kinds ("item" is
// every item kind; "library" the libraries the profile isn't given; any other
// word is an entity kind). ID in method, path and body is replaced by the id.
func perHidden(method, path, body string, kinds ...string) writeRoute {
	return writeProbes(func(m *restrictionEnv) []idProbe {
		out := []idProbe{}
		for _, kind := range kinds {
			ids := []string{}
			switch kind {
			case "item":
				ids = m.hiddenItemSamples()
			case "library":
				ids = m.hiddenLibraries()
			default:
				if list := m.hiddenEntities[kind]; len(list) > 0 {
					ids = []string{list[0]}
				}
			}
			for _, id := range ids {
				// The operation id carries the id, so the absent twin is a new
				// operation rather than a reuse of the hidden probe's key.
				op := "matrix-" + kind + "-" + id
				out = append(out, idProbe{hidden: id, method: method,
					path: strings.ReplaceAll(strings.ReplaceAll(path, "ID", url.PathEscape(id)), "OP", op),
					body: strings.ReplaceAll(strings.ReplaceAll(body, "ID", id), "OP", op)})
			}
		}
		return out
	})
}

// playlistItemKinds are the item kinds the playlist write routes accept as media
// leaves (catalog.playlistItemAllowed: playable, not an extra). A hidden book or
// comic is refused for its kind before any membership check runs, so it would
// prove nothing about these routes.
var playlistItemKinds = []string{"movie", "episode", "song", "audiobook_file"}

// itemProbes builds one probe per hidden item of the given kinds. Unlike
// perHidden it does not path-escape: the playlist routes name the destination in
// the path and the probed item in the body.
func itemProbes(m *restrictionEnv, method, path, body string, kinds []string) []idProbe {
	out := []idProbe{}
	for _, kind := range kinds {
		for _, id := range m.hiddenItems[kind] {
			op := "matrix-" + kind + "-" + id
			out = append(out, idProbe{hidden: id, method: method,
				path: strings.ReplaceAll(path, "OP", op),
				body: strings.ReplaceAll(strings.ReplaceAll(body, "ID", id), "OP", op)})
		}
	}
	return out
}

const (
	reasonOwnSession  = "acts on the caller's own session, device, profile or account; names no catalogue id"
	reasonSignIn      = "sign-in, registration, setup or recovery; runs before or outside a profile session"
	reasonNetworking  = "server networking and claim authority; owner-authorized (claim_handler tests), no catalogue ids"
	reasonOwnObject   = "addresses an object the viewer owns (its playlist, saved resource, pin, receiver, group or device) by that object's id; the object's content is read through the probed GET routes"
	reasonSelfPrefs   = "the viewer's own preferences; no catalogue ids"
	reasonGrantWrite  = "addresses an opaque session, queue, grant or handoff minted only by a request that already passed the restriction check (restriction_admission_test, playback_v1_security_test)"
	reasonLiveChannel = "Live TV and DVR act on guide programmes and channels, which carry their own restriction (channel_restriction_test, recordings_switch_test, TestChannelsARestrictionMidPlayFencesMedia)"
)

// restrictionWriteRoutes classifies every non-GET route except owner
// administration (/v1/admin/…), which is probed by rule.
func restrictionWriteRoutes() map[string]writeRoute {
	routes := map[string]writeRoute{
		"DELETE /v1/facet-queries/{id}":                                {check: facetQueryRestrictionCheck},
		"DELETE /v1/genre-queries/{id}":                                {check: genreQueryRestrictionCheck},
		"DELETE /v1/libraries/{id}/browse-selection-queries/{queryId}": {check: browseSelectionQueryRestrictionCheck("DELETE")},
		"DELETE /v1/libraries/{id}/reading-browse-queries/{queryId}":   {check: readingBrowseQueryRestrictionCheck("DELETE")},
		"DELETE /v1/libraries/{id}/reading-facet-queries/{queryId}":    {check: readingFacetQueryRestrictionCheck("DELETE")},
		"DELETE /v1/file-info-queries/{queryId}":                       {check: fileInfoQueryRestrictionCheck},
		// The selection-action family acts on catalogue content only through an
		// opaque private handle: a volatile preparation or a confirmed operation.
		// Its check mints the real handle and proves the restricted selection is
		// exact, policy drift expires it, and another session, another family or
		// another library is indistinguishable from an absent handle.
		"POST /v1/libraries/{id}/selection-actions":                                   {check: selectionActionConfirmOperationRestrictionCheck("GET")},
		"POST /v1/libraries/{id}/selection-actions/preparations":                      {check: selectionActionPreparationCreateRestrictionCheck},
		"DELETE /v1/libraries/{id}/selection-actions/preparations/{preparationId}":    {check: selectionActionPreparationQueryRestrictionCheck("DELETE")},
		"DELETE /v1/selection-actions/{operationId}":                                  {check: selectionActionConfirmOperationRestrictionCheck("DELETE")},
		"DELETE /v1/metadata/bulk/preview-queries/{queryId}":                          {check: bulkMetadataPreviewQueryRestrictionCheck("DELETE")},
		"DELETE /v1/cast/bootstrap/{id}":                                              writeExempt("retires the caller's own bootstrap continuation; no catalogue lookup or content payload"),
		"POST /v1/items/{itemId}/reading/reread":                                      perHidden("POST", "/v1/items/ID/reading/reread", `{"sourceId":"matrix-source","expectedSourceRevision":"`+strings.Repeat("a", 64)+`","expectedMetadataRevision":"`+strings.Repeat("b", 64)+`"}`, "item"),
		"PUT /v1/items/{id}/main-title":                                               perHidden("PUT", "/v1/items/ID/main-title", `{"versionId":"matrix-version","revision":"`+strings.Repeat("a", 64)+`","titleId":"matrix-title"}`, "item"),
		"POST /v1/items/{id}/disc-titles/prepare":                                     perHidden("POST", "/v1/items/ID/disc-titles/prepare", `{"versionId":"matrix-version","revision":"`+strings.Repeat("a", 64)+`","titleId":"matrix-title"}`, "item"),
		"POST /v1/libraries/{libraryId}/reading/mark-all":                             perHidden("POST", "/v1/libraries/ID/reading/mark-all", `{"operationId":"matrix-mark-all","viewerFence":"`+strings.Repeat("0", 64)+`","expectedRevision":{"catalog":0,"viewer":0}}`, "library"),
		"PUT /v1/libraries/{libraryId}/reading/groups/{groupKind}/{seriesId}/my-list": {check: readingSavedGroupRestrictionCheck},
		"DELETE /v1/reading/saved-groups/{referenceId}":                               {check: readingSavedGroupRestrictionCheck},
		"PUT /v1/items/{itemId}/reading/favorite":                                     {check: readingFavoriteRestrictionCheck},
		"PUT /v1/items/{itemId}/reading/my-list":                                      {check: readingSavedRestrictionCheck},
		"PUT /v1/items/{itemId}/reading/mark":                                         {check: readingMarkRestrictionCheck},
		"POST /v1/reading/sessions":                                                   {check: readingRestrictionCheck("POST /v1/reading/sessions")},
		"HEAD /v1/reading/sessions/{sessionId}/source":                                {check: readingRestrictionCheck("HEAD /v1/reading/sessions/{sessionId}/source")},
		"PUT /v1/reading/sessions/{sessionId}/progress":                               {check: readingRestrictionCheck("PUT /v1/reading/sessions/{sessionId}/progress")},
		"PUT /v1/reading/sessions/{sessionId}/annotations":                            {check: readingRestrictionCheck("PUT /v1/reading/sessions/{sessionId}/annotations")},
		"POST /v1/reading/sessions/{sessionId}/file-save":                             {check: readingRestrictionCheck("POST /v1/reading/sessions/{sessionId}/file-save")},
		"POST /v1/reading/offline/progress":                                           {check: readingOfflineRestrictionCheck},
		"POST /v1/reading/offline/progress/read":                                      {check: readingOfflineReadRestrictionCheck},
		"DELETE /v1/reading/sessions/{sessionId}":                                     {check: readingRestrictionCheck("DELETE /v1/reading/sessions/{sessionId}")},
		"DELETE /v1/me/removed-titles/{id}":                                           perHidden("DELETE", "/v1/me/removed-titles/ID", "", "item"),
		"POST /v1/home/destination-picker": writeProbes(func(m *restrictionEnv) []idProbe {
			var out []idProbe
			for _, kind := range []string{"playlist", "collection"} {
				for _, id := range m.hiddenItemSamples() {
					out = append(out, idProbe{hidden: id, method: "POST", path: "/v1/home/destination-picker", body: fmt.Sprintf(`{"kind":%q,"limit":40,"itemIds":[%q]}`, kind, id)})
				}
			}
			return out
		}),
		"POST /v1/home/destination-receipt": writeProbes(func(m *restrictionEnv) []idProbe {
			var out []idProbe
			for kind, destination := range map[string]string{"playlist": m.playlist, "collection": m.savedResource} {
				for _, id := range m.hiddenSamples() {
					out = append(out,
						idProbe{hidden: id, method: "POST", path: "/v1/home/destination-receipt", body: fmt.Sprintf(`{"kind":%q,"destinationId":%q,"jobIds":[%q]}`, kind, destination, id)},
						idProbe{hidden: id, method: "POST", path: "/v1/home/destination-receipt", body: fmt.Sprintf(`{"kind":%q,"destinationId":%q,"jobIds":["matrix-foreign-job"]}`, kind, id)},
					)
				}
			}
			return out
		}),
		"POST /v1/metadata/bulk/preview": writeProbes(func(m *restrictionEnv) []idProbe {
			var out []idProbe
			for _, library := range []string{"private-library", "matrix-movies"} {
				body := fmt.Sprintf(`{"targets":[{"kind":"item","contentKind":"movie","id":"ID","libraryId":%q}]}`, library)
				out = append(out, perHidden("POST", "/v1/metadata/bulk/preview", body, "item").probes(m)...)
			}
			return out
		}),
		"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/fonts/{font}": writeExempt("read-only asset headers; originalSubtitleRoute rechecks grant and subtitle visibility"),
		"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.mks": writeExempt("read-only asset headers; originalSubtitleRoute rechecks grant and subtitle visibility"),
		"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.sub": writeExempt("read-only asset headers; originalSubtitleRoute rechecks grant and subtitle visibility"),
		"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.idx": writeExempt("read-only asset headers; originalSubtitleRoute rechecks grant and subtitle visibility"),
		"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.ssa": writeExempt("read-only asset headers; originalSubtitleRoute rechecks grant and subtitle visibility"),
		"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.ass": writeExempt("read-only asset headers; originalSubtitleRoute rechecks grant and subtitle visibility"),
		"HEAD /v1/media/subtitles/{grant}/{resource}/{revision}/original.sup": writeExempt("read-only asset headers; originalSubtitleRoute rechecks grant and subtitle visibility"),
		"DELETE /v1/items/{itemId}/subtitles/{resource}":                      perHidden("DELETE", "/v1/items/ID/subtitles/matrix-resource", `{}`, "item"),
		"PUT /v1/items/{itemId}/subtitles/{resource}":                         perHidden("PUT", "/v1/items/ID/subtitles/matrix-resource", `{}`, "item"),
		"POST /v1/items/{itemId}/subtitles/remote":                            perHidden("POST", "/v1/items/ID/subtitles/remote", `{}`, "item"),
		"POST /v1/items/{itemId}/subtitles/search":                            perHidden("POST", "/v1/items/ID/subtitles/search", `{}`, "item"),
		"POST /v1/items/{itemId}/subtitles/refresh":                           perHidden("POST", "/v1/items/ID/subtitles/refresh", `{}`, "item"),
		"POST /v1/items/{itemId}/subtitles/import":                            perHidden("POST", "/v1/items/ID/subtitles/import", `{}`, "item"),
		"POST /v1/items/{itemId}/subtitles/apply":                             perHidden("POST", "/v1/items/ID/subtitles/apply", `{}`, "item"),
		"POST /v1/items/{itemId}/subtitles":                                   perHidden("POST", "/v1/items/ID/subtitles", `{}`, "item"),
		"POST /v1/libraries/{id}/metadata/attention":                          perHidden("POST", "/v1/libraries/ID/metadata/attention", `{}`, "library"),
		"POST /v1/shows/{id}/metadata/refresh":                                perHidden("POST", "/v1/shows/ID/metadata/refresh", `{}`, "show"),
		"POST /v1/libraries/paths-changed":                                    perHidden("POST", "/v1/libraries/paths-changed", `{"libraryId":"ID","paths":["film.mkv"]}`, "library"),
		// Owner-only native refresh: a member is refused before a library is
		// named, so hidden and absent libraries must be indistinguishable.
		"POST /v1/integrations/refresh":    perHidden("POST", "/v1/integrations/refresh", `{"libraryId":"ID"}`, "library"),
		"POST /v1/me/access-tokens":        writeExempt("the caller's credential creation; names scopes, no catalogue ids"),
		"DELETE /v1/me/access-tokens/{id}": writeExempt("the caller's credential revocation; names no catalogue id"),
		// The tool endpoint takes a personal access token or an API key, never a
		// session, and each tool is one of this server's own routes called as
		// that credential's profile. It is probed with the restricted profile's
		// own token in the tools subtest: hidden ≡ absent for the tools that name
		// a title, and no hidden identifier in what the listing tools say.
		"POST /v1/mcp": writeExempt("probed with the restricted profile's personal access token by the tools subtest of this test"),
		// A report names the title it is about.
		"POST /v1/feedback/reports":                        perHidden("POST", "/v1/feedback/reports", `{"operationId":"OP","kind":"library","category":"wrong-details","message":"The details on this one are wrong.","itemId":"ID","playbackSessionId":"","attachDiagnostics":false}`, "item"),
		"DELETE /v1/collections/{id}/items/{itemId}":       perHidden("DELETE", "/v1/collections/unknown/items/ID", `{}`, "item"),
		"DELETE /v1/items/{id}/lyrics/{resource}":          perHidden("DELETE", "/v1/items/ID/lyrics/unknown", `{}`, "item"),
		"DELETE /v1/playlists/{id}":                        writeExempt(reasonOwnObject),
		"DELETE /v1/playlists/{id}/entries/{entryId}":      writeExempt(reasonOwnObject),
		"DELETE /v1/playlists/{id}/shares":                 writeExempt(reasonOwnObject),
		"DELETE /v1/saved-resources/{id}":                  writeExempt(reasonOwnObject),
		"DELETE /v1/saved-resources/{id}/shares":           writeExempt(reasonOwnObject),
		"DELETE /v1/storage/mounts/{id}":                   writeExempt("owner-only storage configuration; no member catalogue mutation (mounts_test and owner checks in router.go)"),
		"DELETE /v1/storage/sources/webdav/{id}":           writeExempt("owner-only storage configuration; no member catalogue mutation (mounts_test and owner checks in router.go)"),
		"DELETE /v1/storage/sources/rclone/{id}":           {check: TestRcloneSourceRouteKeepsOwnerFirstAndNoSubsystemFacts},
		"PATCH /v1/items/{id}/lyrics/{resource}/offset":    perHidden("PATCH", "/v1/items/ID/lyrics/unknown/offset", `{}`, "item"),
		"PATCH /v1/items/{id}/metadata/manual":             writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"PATCH /v1/playlists/{id}":                         writeExempt(reasonOwnObject),
		"PATCH /v1/preferences":                            writeExempt(reasonSelfPrefs),
		"PATCH /v1/saved-resources/{id}":                   writeExempt(reasonOwnObject),
		"POST /v1/albums/{id}/metadata/musicbrainz/policy": writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/albums/{id}/metadata/musicbrainz/retry":  writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/albums/{id}/metadata/musicbrainz/search": writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/analysis/jobs":                writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/analysis/jobs/{job}/control":  writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/analysis/markers":             writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/lyrics":                       perHidden("POST", "/v1/items/ID/lyrics", `{}`, "item"),
		"POST /v1/items/{id}/lyrics/search":                perHidden("POST", "/v1/items/ID/lyrics/search", `{}`, "item"),
		"POST /v1/items/{id}/metadata/musicbrainz/policy":  writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/metadata/musicbrainz/retry":   writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/metadata/musicbrainz/search":  writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/metadata/screen/retry":        writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/metadata/screen/search":       writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/items/{id}/optimizations":                writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/metadata/{kind}/{id}":                    writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/networking/certificate/config":           writeExempt(reasonNetworking),
		"POST /v1/networking/certificate/retry":            writeExempt(reasonNetworking),
		"POST /v1/networking/claim/approve":                writeExempt(reasonNetworking),
		"POST /v1/networking/claim/cancel":                 writeExempt(reasonNetworking),
		"POST /v1/networking/claim/continue":               writeExempt(reasonNetworking),
		"POST /v1/networking/claim/prepare":                writeExempt(reasonNetworking),
		"POST /v1/networking/remote/check":                 writeExempt(reasonNetworking),
		"POST /v1/networking/remote/config":                writeExempt(reasonNetworking),
		"POST /v1/notifications/inbox/actions":             writeExempt("marks only the caller's notification objects as read (notifications_routes_test)"),
		"POST /v1/notifications/{id}/read":                 writeExempt("marks only the caller's notification objects as read (notifications_routes_test)"),
		"POST /v1/optimization-jobs/{id}/cancel":           writeExempt("owner-only prepared job/version mutation, no new catalogue selection (preparedmedia service owner checks)"),
		"POST /v1/optimization-jobs/{id}/retry":            writeExempt("owner-only prepared job/version mutation, no new catalogue selection (preparedmedia service owner checks)"),
		"POST /v1/music/mixes/{id}/playlist":               {check: musicMixForbiddenCopyCheck},
		// Both playlist writes name an item id in the body, so both can reach
		// catalogue membership. The older probe here sent itemIds with
		// expectedRevision 0, which the add action answers 400 for before it reads
		// any item: it proved nothing. These send one real item at the playlist's
		// own current revision.
		"POST /v1/playlists": writeProbes(func(m *restrictionEnv) []idProbe {
			return itemProbes(m, "POST", "/v1/playlists", `{"operationId":"OP","name":"Matrix","itemIds":["ID"]}`, playlistItemKinds)
		}),
		"POST /v1/playlists/{id}/entries": writeProbes(func(m *restrictionEnv) []idProbe {
			body := fmt.Sprintf(`{"operationId":"OP","expectedRevision":%d,"itemId":"ID"}`, m.playlistRevision)
			return itemProbes(m, "POST", "/v1/playlists/"+m.playlist+"/entries", body, playlistItemKinds)
		}),
		"POST /v1/prepared-versions/{id}/delete":             writeExempt("owner-only prepared job/version mutation, no new catalogue selection (preparedmedia service owner checks)"),
		"POST /v1/quick-connect/cancel":                      writeExempt(reasonSignIn),
		"POST /v1/quick-connect/token":                       writeExempt(reasonSignIn),
		"POST /v1/saved-resources":                           writeExempt(reasonOwnObject),
		"POST /v1/shows/{id}/metadata/screen/retry":          writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/shows/{id}/metadata/screen/search":         writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"POST /v1/storage/mounts":                            writeExempt("owner-only storage configuration; no member catalogue mutation (mounts_test and owner checks in router.go)"),
		"POST /v1/storage/mounts/{id}/actions":               writeExempt("owner-only storage configuration; no member catalogue mutation (mounts_test and owner checks in router.go)"),
		"POST /v1/storage/sources/webdav/test":               {check: TestDAVConnectionHTTPChecksOwnerAndNeverPublishesBlockedTest},
		"POST /v1/storage/sources/{id}/test":                 writeExempt("owner-only saved-source connection test; reads the saved association under exclusive test custody and mutates no saved row or catalogue (storageOwner on the route)"),
		"POST /v1/storage/sources/rclone/test":               {check: TestRcloneConnectionRouteKeepsOwnerFirstAndNoSubsystemFacts},
		"POST /v1/storage/sources/rclone":                    {check: TestRcloneSourceRouteKeepsOwnerFirstAndNoSubsystemFacts},
		"POST /v1/storage/sources/rclone/config":             writeExempt("owner-only storage configuration read from the account file; no member catalogue mutation (storageOwner on the route)"),
		"POST /v1/storage/sources/rclone/drive/oauth/start":  writeExempt("owner-only Google sign-in session; no member catalogue mutation (storageOwner on every route)"),
		"POST /v1/storage/sources/rclone/drive/oauth/paste":  writeExempt("owner-only Google sign-in session; no member catalogue mutation (storageOwner on every route)"),
		"POST /v1/storage/sources/rclone/drive/oauth/status": writeExempt("owner-only Google sign-in session; no member catalogue mutation (storageOwner on every route)"),
		"POST /v1/storage/sources/rclone/drive/oauth/cancel": writeExempt("owner-only Google sign-in session; no member catalogue mutation (storageOwner on every route)"),
		"PUT /v1/storage/sources/rclone/{id}":                {check: TestRcloneSourceRouteKeepsOwnerFirstAndNoSubsystemFacts},
		"POST /v1/storage/sources/webdav":                    writeExempt("owner-only storage configuration; no member catalogue mutation (mounts_test and owner checks in router.go)"),
		"PUT /v1/albums/{id}/metadata/musicbrainz":           writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"PUT /v1/collections/{id}/items/{itemId}":            perHidden("PUT", "/v1/collections/unknown/items/ID", `{}`, "item"),
		"PUT /v1/items/{id}/analysis/policy":                 writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"PUT /v1/items/{id}/lyrics/selection":                perHidden("PUT", "/v1/items/ID/lyrics/selection", `{}`, "item"),
		"PUT /v1/items/{id}/metadata/musicbrainz":            writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"PUT /v1/items/{id}/metadata/screen":                 writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"PUT /v1/playlists/{id}/shares":                      writeExempt(reasonOwnObject),
		"PUT /v1/saved-resources/{id}/entries": writeProbes(func(m *restrictionEnv) []idProbe {
			var revision int64
			m.db.QueryRow(`SELECT revision FROM saved_resources WHERE id=?`, m.savedResource).Scan(&revision)
			body := fmt.Sprintf(`{"operationId":"OP","expectedRevision":%d,"addItemIds":["ID"]}`, revision)
			return perHidden("PUT", "/v1/saved-resources/"+m.savedResource+"/entries", body, "item").probes(m)
		}),
		"PUT /v1/saved-resources/{id}/order":             writeExempt(reasonOwnObject),
		"PUT /v1/saved-resources/{id}/shares":            writeExempt(reasonOwnObject),
		"PUT /v1/shows/{id}/metadata/screen":             writeExempt("owner-only metadata/analysis/preparation mutation; member refused (manual_metadata_test, musicbrainz_test and analysis owner checks)"),
		"PUT /v1/storage/sources/webdav/{id}":            writeExempt("owner-only storage configuration; no member catalogue mutation (mounts_test and owner checks in router.go)"),
		"PUT /v1/storage/sources/{id}/settings/advanced": writeExempt("owner-only storage configuration; no member catalogue mutation (Owner plus ScopeAdmin, captured source association rechecked in the accepting writer)"),
		"PUT /v1/storage/sources/{id}/settings/program":  writeExempt("owner-only storage configuration; no member catalogue mutation (Owner plus ScopeAdmin, mount receipt and source association rechecked in the accepting writer)"),

		"PUT /v1/storage/sources/{id}/settings/read-profile": {check: TestSourceReadSettingsHTTPUsesCurrentOwnerCASAndDurableReceipt},

		// Catalogue content named by id.
		"PUT /v1/items/{id}/personal-state": perHidden("PUT", "/v1/items/ID/personal-state", `{"operationId":"OP","expectedRevision":0,"favorite":true}`, "item"),
		// Not interested also names a show, album or book: those are probed too.
		"PUT /v1/items/personal-state:batch": writeProbes(func(m *restrictionEnv) []idProbe {
			items := perHidden("PUT", "/v1/items/personal-state:batch", `{"operationId":"OP","items":[{"itemId":"ID","watchlisted":true}]}`, "item").probes(m)
			return append(items, perHidden("PUT", "/v1/items/personal-state:batch", `{"operationId":"OP","items":[{"itemId":"ID","notInterested":true}]}`, "item", "show", "album", "book").probes(m)...)
		}),
		"PUT /v1/containers/{kind}/{id}/personal-state": writeProbes(func(m *restrictionEnv) []idProbe {
			out := []idProbe{}
			for _, kind := range []string{"show", "season", "album", "artist", "collection", "book"} {
				out = append(out, perHidden("PUT", "/v1/containers/"+kind+"/ID/personal-state", `{"expectedRevision":0,"watched":true}`, kind).probes(m)...)
			}
			return out
		}),
		"POST /v1/containers/{kind}/{id}/personal-state/undo": writeProbes(func(m *restrictionEnv) []idProbe {
			var out []idProbe
			for _, kind := range []string{"show", "season", "album", "book"} {
				out = append(out, perHidden("POST", "/v1/containers/"+kind+"/ID/personal-state/undo", `{"expectedRevision":1,"undoToken":"matrix-undo"}`, kind).probes(m)...)
			}
			return out
		}),
		"POST /v1/items/{id}/delete":                                      perHidden("POST", "/v1/items/ID/delete", `{"operationId":"OP"}`, "item"),
		"POST /v1/items/{id}/delete/preview":                              perHidden("POST", "/v1/items/ID/delete/preview", `{}`, "item"),
		"POST /v1/items/{id}/metadata/refresh":                            perHidden("POST", "/v1/items/ID/metadata/refresh", `{}`, "item"),
		"POST /v1/items/{id}/metadata/local-audio/policy":                 perHidden("POST", "/v1/items/ID/metadata/local-audio/policy", `{}`, "item"),
		"POST /v1/metadata/{kind}/{id}/art/{role}/upload":                 perHidden("POST", "/v1/metadata/item/ID/art/poster/upload", `{}`, "item"),
		"DELETE /v1/metadata/{kind}/{id}/art/{role}/upload/{candidateId}": perHidden("DELETE", "/v1/metadata/item/ID/art/poster/upload/matrix-candidate", "", "item"),
		"POST /v1/metadata/bulk":                                          perHidden("POST", "/v1/metadata/bulk", `{"operationId":"OP","itemIds":["ID"]}`, "item"),
		"PUT /v1/shows/{id}/metadata/screen/season":                       perHidden("PUT", "/v1/shows/ID/metadata/screen/season", `{}`, "show"),
		"PUT /v1/shows/{id}/metadata/tvdb":                                perHidden("PUT", "/v1/shows/ID/metadata/tvdb", `{}`, "show"),
		"POST /v1/shows/{id}/metadata/tvdb/retry":                         perHidden("POST", "/v1/shows/ID/metadata/tvdb/retry", `{}`, "show"),
		"PATCH /v1/collections/{id}":                                      perHidden("PATCH", "/v1/collections/ID", `{"name":"x"}`, "collection"),
		"DELETE /v1/collections/{id}":                                     perHidden("DELETE", "/v1/collections/ID", "", "collection"),
		"PUT /v1/collections/{id}/order":                                  perHidden("PUT", "/v1/collections/ID/order", `{"expectedRevision":1,"itemIds":["x"]}`, "collection"),
		"POST /v1/collections/{id}/items:batch": writeProbes(func(m *restrictionEnv) []idProbe {
			out := perHidden("POST", "/v1/collections/ID/items:batch", `{"addItemIds":[]}`, "collection").probes(m)
			if visible := m.visibleEntities["collection"]; len(visible) > 0 {
				out = append(out, perHidden("POST", "/v1/collections/"+url.PathEscape(visible[0])+"/items:batch", `{"addItemIds":["ID"]}`, "item").probes(m)...)
			}
			return out
		}),
		"POST /v1/downloads/preparations":      perHidden("POST", "/v1/downloads/preparations", `{"operationId":"OP","mediaId":"ID","quality":"original"}`, "item"),
		"POST /v1/playback/sessions":           perHidden("POST", "/v1/playback/sessions", `{"itemId":"ID","startFrom":"beginning"}`, "item"),
		"POST /v1/playback/controls/{id}:stop": writeExempt("account-scoped opaque playback session stop; no catalogue ID or title response (TestPlaybackV1AccountPlaybackListHidesRestrictedTitlesAndStopsOwnSlots)"),
		"POST /v1/downloads/options": writeProbes(func(m *restrictionEnv) []idProbe {
			var out []idProbe
			for _, kind := range []string{"show", "season", "album", "book"} {
				for _, probe := range perHidden("POST", "/v1/downloads/options", "", kind).probes(m) {
					// Bind the actual device only after target substitution. A
					// legitimate base64url device token may itself contain "ID".
					probe.body = fmt.Sprintf(`{"target":{"kind":%q,"id":%q},"deviceId":%q,"policy":{"episodes":"all"}}`, kind, probe.hidden, m.device)
					out = append(out, probe)
				}
			}
			return out
		}),
		"POST /v1/queues/{id}/batch":                  perHidden("POST", "/v1/queues/matrix-unknown/batch", `{"placement":"end","source":{"items":{"ids":["ID"]}}}`, "item"),
		"POST /v1/queues":                             perHidden("POST", "/v1/queues", `{"segments":[{"source":{"items":{"ids":["ID"]}}}],"startPlayback":{"state":"playing"}}`, "item"),
		"PATCH /v1/dvr/recordings/{id}":               perHidden("PATCH", "/v1/dvr/recordings/ID", `{"expectedRevision":0}`, "item"),
		"DELETE /v1/dvr/recordings/{id}":              perHidden("DELETE", "/v1/dvr/recordings/ID", "", "item"),
		"PUT /v1/dvr/recordings/{id}/keep":            perHidden("PUT", "/v1/dvr/recordings/ID/keep", `{"keep":true}`, "item"),
		"POST /v1/dvr/recordings/{id}/cancel":         perHidden("POST", "/v1/dvr/recordings/ID/cancel", `{}`, "item"),
		"PUT /v1/me/library-navigation":               perHidden("PUT", "/v1/me/library-navigation", `{"expectedRevision":0,"pinnedLibraryIds":["ID"]}`, "library"),
		"POST /v1/libraries":                          writeExempt("owner-only library creation (library_routes tests); names no existing catalogue id"),
		"POST /v1/libraries/{id}/browse":              perHidden("POST", "/v1/libraries/ID/browse", `{"pivot":"movies","limit":20}`, "library"),
		"POST /v1/libraries/{id}/browse/counts":       perHidden("POST", "/v1/libraries/ID/browse/counts", `{"pivot":"movies","queries":[{"id":"x"}]}`, "library"),
		"POST /v1/browse":                             perHidden("POST", "/v1/browse", `{"pivot":"titles","query":{"variant":"predicate","field":"savedCollection","operator":"equals","value":"ID"},"limit":20}`, "item"),
		"POST /v1/browse/counts":                      perHidden("POST", "/v1/browse/counts", `{"pivot":"titles","queries":[{"id":"x","query":{"variant":"predicate","field":"savedCollection","operator":"equals","value":"ID"}}]}`, "item"),
		"POST /v1/libraries/{id}/collections":         perHidden("POST", "/v1/libraries/ID/collections", `{"name":"x"}`, "library"),
		"POST /v1/libraries/{id}/episode-assignments": perHidden("POST", "/v1/libraries/ID/episode-assignments", `{}`, "library"),
		"POST /v1/libraries/{id}/lyrics/fetch":        perHidden("POST", "/v1/libraries/ID/lyrics/fetch", `{}`, "library"),
		"POST /v1/libraries/{id}/network-roots":       perHidden("POST", "/v1/libraries/ID/network-roots", `{}`, "library"),
		"POST /v1/libraries/{id}/scans":               perHidden("POST", "/v1/libraries/ID/scans", `{}`, "library"),
		"PUT /v1/libraries/{id}/metadata/agent":       perHidden("PUT", "/v1/libraries/ID/metadata/agent", `{}`, "library"),
		"PUT /v1/libraries/{id}/metadata/screen":      perHidden("PUT", "/v1/libraries/ID/metadata/screen", `{}`, "library"),
		"PUT /v1/libraries/{id}/strm-analysis-policy": perHidden("PUT", "/v1/libraries/ID/strm-analysis-policy", `{}`, "library"),
		"POST /v1/jobs":                               perHidden("POST", "/v1/jobs", `{"operationId":"OP","command":"personal-state","selector":{"items":{"ids":["ID"]}},"args":{"watched":true}}`, "item"),
		"POST /v1/downloads/requests":                 writeExempt("probed with hidden shows, seasons and items by the read matrix's download-request-security subtest"),

		// The viewer's own objects, sessions and grants.
		"PUT /v1/playlists/{id}/order":                      writeExempt(reasonOwnObject),
		"PUT /v1/saved-pins/order":                          writeExempt(reasonOwnObject),
		"PUT /v1/saved-pins/{kind}/{id}":                    writeExempt(reasonOwnObject),
		"POST /v1/personal-history/actions":                 writeExempt("acts on the viewer's own history rows by action; history reads are filtered to visible titles (probed GET /v1/personal-history)"),
		"POST /v1/me/recommendations:reset":                 writeExempt("names no title: clears the viewer's own not-interested marks and learned taste, and answers with a count only"),
		"DELETE /v1/search/history":                         writeExempt(reasonOwnObject),
		"PATCH /v1/playback/sessions/{id}":                  writeExempt(reasonGrantWrite),
		"POST /v1/playback/sessions/{id}/receivers/airplay": {check: airPlayRestrictionCheck},
		"DELETE /v1/playback/sessions/{id}":                 writeExempt(reasonGrantWrite),
		"PUT /v1/playback/sessions/{id}/observation":        writeExempt(reasonGrantWrite),
		"POST /v1/playback/sessions/{id}/observer":          writeExempt(reasonGrantWrite),
		"PATCH /v1/queues/{id}":                             writeExempt(reasonGrantWrite),
		"DELETE /v1/queues/{id}":                            writeExempt(reasonGrantWrite),
		"DELETE /v1/queues/{id}/entries/{entryId}":          writeExempt(reasonGrantWrite),
		"POST /v1/queues/{id}/entries/{entryId}:move":       writeExempt(reasonGrantWrite),
		"POST /v1/queues/{id}/segments":                     writeExempt(reasonGrantWrite + "; new segments are fenced per entry like a queue create (a6fc591d)"),
		"POST /v1/queues/{id}:advance":                      writeExempt(reasonGrantWrite),
		"POST /v1/queues/{id}:commit-next":                  writeExempt(reasonGrantWrite),
		"POST /v1/queues/{id}:prepare-next":                 writeExempt(reasonGrantWrite),
		"POST /v1/queues/{id}:save-as-playlist":             writeExempt(reasonGrantWrite),
		"POST /v1/downloads/preparations/{id}/actions":      writeExempt(reasonGrantWrite),
		"POST /v1/downloads/preparations/{id}/grant":        writeExempt(reasonGrantWrite),
		"POST /v1/downloads/progress":                       writeExempt(reasonGrantWrite),
		"POST /v1/downloads/receipts":                       writeExempt(reasonGrantWrite),
		"POST /v1/downloads/receipts/revalidate":            writeExempt(reasonGrantWrite),
		"POST /v1/downloads/receipts/revoke":                writeExempt(reasonGrantWrite),
		"POST /v1/handoffs":                                 writeExempt(reasonGrantWrite),
		"POST /v1/handoffs/{id}/commit":                     writeExempt(reasonGrantWrite),
		"POST /v1/handoffs/{id}/readiness":                  writeExempt(reasonGrantWrite),
		"POST /v1/handoffs/{id}/rollback":                   writeExempt(reasonGrantWrite),
		"POST /v1/groups":                                   writeExempt(reasonOwnObject + "; a group carries no playback authority (createGroup)"),
		"POST /v1/groups/join":                              writeExempt(reasonOwnObject),
		"POST /v1/groups/{id}/end":                          writeExempt(reasonOwnObject),
		"POST /v1/groups/{id}/heartbeat":                    writeExempt(reasonOwnObject),
		"POST /v1/groups/{id}/host-transfer":                writeExempt(reasonOwnObject),
		"POST /v1/groups/{id}/invites":                      writeExempt(reasonOwnObject),
		"DELETE /v1/groups/{id}/invites/{inviteId}":         writeExempt(reasonOwnObject),
		"POST /v1/groups/{id}/leave":                        writeExempt(reasonOwnObject),
		"POST /v1/groups/{id}/queue":                        writeExempt(reasonGrantWrite + "; the group queue follows the host's admitted v1 session"),
		"POST /v1/groups/{id}/readiness":                    writeExempt(reasonOwnObject),
		"POST /v1/groups/{id}/settings":                     writeExempt(reasonOwnObject),
		"POST /v1/groups/{id}/transport":                    writeExempt(reasonOwnObject),
		"POST /v1/receivers":                                writeExempt(reasonOwnObject),
		"DELETE /v1/receivers/{id}":                         writeExempt(reasonOwnObject),
		"POST /v1/receivers/{id}/grants":                    writeExempt(reasonGrantWrite),
		"POST /v1/receivers/{id}/grants/{grantId}/decision": writeExempt(reasonGrantWrite),
		"POST /v1/receivers/{id}/heartbeat":                 writeExempt(reasonOwnObject),
		"POST /v1/cast/controllers/register":                writeExempt(reasonOwnSession + " (exact receiver controller binding, no catalogue ids)"),
		"POST /v1/cast/controllers/validate":                writeExempt(reasonOwnSession + " (original sender and current receiver family validation, no catalogue ids)"),
		"POST /v1/cast/controllers/check":                   writeExempt(reasonOwnSession + " (exact receiver authority check, no catalogue ids)"),
		"POST /v1/app-reports":                              writeExempt("current original session and verified device submit only the explicitly consented closed coded journal and receive its receipt; no catalogue identifiers or content"),
		"POST /v1/cast/bootstrap":                           writeExempt(reasonOwnSession + " (a pairing code)"),
		"POST /v1/cast/reconnect":                           writeExempt(reasonOwnSession),
		"POST /v1/cast/preview":                             writeExempt("credential-free bounded pairing offer; no catalogue ids"),
		"POST /v1/cast/redeem":                              writeExempt(reasonOwnSession),
		"DELETE /v1/cast/devices/{id}":                      writeExempt(reasonOwnObject),
		"PUT /v1/cast/configuration":                        writeExempt("owner Cast configuration; no catalogue ids"),
		"POST /v1/topshelf/token":                           writeExempt(reasonOwnSession),

		// Live TV and DVR.
		"PUT /v1/guide/reminders":               perHidden("PUT", "/v1/guide/reminders", `{"sourceId":"matrix-unknown","channelId":"matrix-unknown","generation":"matrix-unknown","programmeId":"ID","kind":"library-channel","expectedRevision":0}`, "item"),
		"DELETE /v1/guide/reminders/{id}":       perHidden("DELETE", "/v1/guide/reminders/ID", `{"expectedRevision":1}`, "item"),
		"POST /v1/dvr/recordings":               writeExempt(reasonLiveChannel),
		"POST /v1/dvr/rules/preview":            writeExempt(reasonLiveChannel),
		"PUT /v1/dvr/rules/{id}":                writeExempt(reasonLiveChannel),
		"DELETE /v1/dvr/rules/{id}":             writeExempt(reasonLiveChannel),
		"PUT /v1/dvr/storage":                   writeExempt(reasonLiveChannel),
		"POST /v1/channels/preferences":         writeExempt(reasonLiveChannel),
		"POST /v1/library-channels/preferences": writeExempt(reasonLiveChannel),

		// Preferences.
		"PUT /v1/home/layout":                     writeExempt(reasonSelfPrefs),
		"POST /v1/home/layout/reset":              writeExempt(reasonSelfPrefs),
		"PUT /v1/listening/preferences":           writeExempt(reasonSelfPrefs),
		"PUT /v1/downloads/settings":              writeExempt(reasonSelfPrefs),
		"PUT /v1/lyrics/provider":                 writeExempt("owner lyrics provider setting; no catalogue ids"),
		"PUT /v1/me/devices/current/capabilities": writeExempt(reasonSelfPrefs),
		"POST /v1/diagnostics/client-logs":        writeExempt(reasonOwnSession),

		// Sessions, devices, identity.
		"DELETE /v1/sessions/current":                       writeExempt(reasonOwnSession),
		"POST /v1/auth/refresh":                             writeExempt(reasonSignIn),
		"POST /v1/auth/register":                            writeExempt(reasonSignIn),
		"POST /v1/auth/two-factor/challenge":                writeExempt(reasonSignIn),
		"POST /v1/access/invitations/accept":                writeExempt(reasonSignIn),
		"POST /v1/access/invitations/preview":               writeExempt(reasonSignIn),
		"POST /v1/devices":                                  writeExempt(reasonOwnSession),
		"PATCH /v1/devices/{id}":                            writeExempt(reasonOwnSession),
		"DELETE /v1/devices/{id}":                           writeExempt(reasonOwnSession),
		"DELETE /v1/devices/{id}/sessions":                  writeExempt(reasonOwnSession),
		"POST /v1/devices/{id}/sessions/bind":               writeExempt(reasonOwnSession),
		"POST /v1/quick-connect":                            writeExempt(reasonSignIn),
		"POST /v1/quick-connect/decision":                   writeExempt(reasonOwnSession),
		"POST /v1/quick-connect/review":                     writeExempt(reasonOwnSession),
		"POST /v1/setup":                                    writeExempt(reasonSignIn),
		"POST /v1/setup/app":                                writeExempt(reasonSignIn),
		"POST /v1/setup/browser":                            writeExempt(reasonSignIn),
		"POST /v1/setup/start-over":                         writeExempt(reasonSignIn),
		"POST /v1/setup/remote-check":                       writeExempt(reasonSignIn),
		"POST /v1/auth/portico-return/exchange":             writeExempt(reasonSignIn),
		"POST /v1/auth/portico-return/prepare":              writeExempt(reasonSignIn),
		"POST /v1/auth/move/password":                       writeExempt("a Waiting account's old password proves only that account's move; no catalogue lookup"),
		"POST /v1/auth/move/code":                           writeExempt("one-use account move code preview; no catalogue lookup"),
		"POST /v1/auth/move/local":                          writeExempt("one-use code creates only the captured account's local sign-in; no catalogue lookup"),
		"POST /v1/auth/move/portico":                        writeExempt("signed identity links only the captured Waiting account; no catalogue lookup"),
		"POST /v1/setup/account/complete":                   writeExempt(reasonSignIn),
		"POST /v1/setup/approval":                           writeExempt(reasonSignIn),
		"POST /v1/setup/abandon":                            writeExempt(reasonSignIn),
		"POST /v1/auth/owner-reset/request":                 writeExempt(reasonSignIn),
		"POST /v1/auth/owner-reset/verify":                  writeExempt(reasonSignIn),
		"POST /v1/direct/password/reset":                    writeExempt(reasonSignIn),
		"POST /v1/direct/members/{id}/reset-code":           writeExempt("authorized local member password reset; no catalogue ids"),
		"POST /v1/setup/resume":                             writeExempt(reasonSignIn),
		"POST /v1/setup/retry":                              writeExempt(reasonSignIn),
		"POST /v1/hosted/wake":                              writeExempt(reasonNetworking),
		"POST /v1/direct/members":                           writeExempt("owner member management (admin_access_test); no catalogue ids"),
		"PATCH /v1/direct/members/{id}":                     writeExempt("owner member management (admin_access_test); no catalogue ids"),
		"DELETE /v1/direct/members/{id}":                    writeExempt("owner member management (member_deletion_test); no catalogue ids"),
		"POST /v1/direct/ownership":                         writeExempt(reasonOwnSession),
		"POST /v1/direct/ownership/custody":                 writeExempt(reasonOwnSession),
		"POST /v1/direct/password":                          writeExempt(reasonOwnSession),
		"POST /v1/direct/portico-challenge":                 writeExempt(reasonSignIn),
		"POST /v1/direct/profile-switch":                    writeExempt(reasonOwnSession),
		"POST /v1/direct/profiles":                          writeExempt(reasonOwnSession),
		"PATCH /v1/direct/profiles/{id}":                    writeExempt(reasonOwnSession),
		"DELETE /v1/direct/profiles/{id}":                   writeExempt(reasonOwnSession),
		"POST /v1/direct/profiles/{id}/avatar":              writeExempt(reasonOwnSession),
		"DELETE /v1/direct/profiles/{id}/avatar":            writeExempt(reasonOwnSession),
		"POST /v1/direct/profiles/{id}/pin-reset":           writeExempt(reasonOwnSession),
		"POST /v1/direct/profiles/{id}/select":              writeExempt(reasonOwnSession),
		"PUT /v1/direct/profiles/order":                     writeExempt(reasonOwnSession),
		"PUT /v1/direct/profiles/{id}/restrictions":         writeExempt("sets a profile's restriction document (restriction tests); names library ids the owner grants, never returns titles"),
		"PUT /v1/direct/registration-policy":                writeExempt(reasonOwnSession),
		"POST /v1/auth/remembered-accounts":                 writeExempt("reads the account switcher for an installation; changes nothing"),
		"POST /v1/direct/remembered-accounts":               writeExempt(reasonOwnSession),
		"DELETE /v1/direct/remembered-accounts/{accountId}": writeExempt(reasonOwnSession),
		"DELETE /v1/direct/sessions/{id}":                   writeExempt(reasonOwnSession),
		"POST /v1/direct/sessions/sign-out-everywhere":      writeExempt(reasonOwnSession),
		"POST /v1/direct/sign-in":                           writeExempt(reasonSignIn),
		"DELETE /v1/direct/trust":                           writeExempt(reasonOwnSession),
		"DELETE /v1/direct/two-factor":                      writeExempt(reasonOwnSession),
		"POST /v1/direct/two-factor/enrol":                  writeExempt(reasonOwnSession),
		"POST /v1/direct/two-factor/verify":                 writeExempt(reasonOwnSession),
		"POST /v1/direct/two-factor/recovery-codes":         writeExempt(reasonOwnSession),

		// Hierarchical People device retirement returns no catalogue content.
		"POST /v1/direct/members/{id}/devices/sign-out":            writeExempt("live main-profile administrator hierarchy retires only that member's device families; no catalogue ids"),
		"POST /v1/direct/members/{id}/devices/{deviceId}/sign-out": writeExempt("live main-profile administrator hierarchy retires only that member's exact device family; no catalogue ids"),

		// Registrations the lane table records in unusual forms.
		"/":                                    writeExempt("the web app shell and first-run setup page (installation.go): static; no catalogue ids"),
		"/dlna/":                               writeExempt("the owner's DLNA switch (dlna_routes.go): no profile and no write; when on it deliberately serves every library to the home network, ignoring profile restrictions"),
		"/dlna/media/":                         writeExempt("DLNA media bodies (dlna_routes.go): read-only, profile-free by the owner's choice; LAN-only and off by default"),
		"POST ":                                writeExempt("the lane scanner's reading of a concatenated registration (screen_metadata.go: \"POST \"+base+\"/search\" and \"/retry\"); owner-only screen metadata, refused to a member (probed through PUT /v1/libraries/{id}/metadata/screen)"),
		"PUT ":                                 writeExempt("the lane scanner's reading of a concatenated registration (screen_metadata.go: \"PUT \"+base); owner-only screen metadata"),
		"HEAD /v1/media/{grant}/audio":         writeExempt(reasonGrantWrite + " (HEAD of a grant-bound media URL)"),
		"HEAD /v1/downloads/artifacts/{grant}": writeExempt(reasonGrantWrite + " (HEAD of a grant-bound download)"),

		// Networking and claim.
		"POST /v1/networking/claim/await-approval": writeExempt(reasonNetworking),
		"POST /v1/networking/claim/endpoint":       writeExempt(reasonNetworking),
		"POST /v1/networking/claim/web-approval":   writeExempt(reasonNetworking),
		"POST /v1/networking/endpoint-proof":       writeExempt(reasonNetworking),
		"POST /v1/networking/identity-proof":       writeExempt(reasonNetworking),
	}
	addIntegrationRestrictionWrites(routes)
	return routes
}

// toolSecurity calls the tool endpoint with a personal access token the
// restricted profile made for itself. A token acts as its profile, so the
// tools must refuse and omit exactly what that profile's session is refused.
func (m *restrictionEnv) toolSecurity(t *testing.T) {
	status, body := m.call("POST", "/v1/me/access-tokens", `{"operationId":"matrix-token","name":"Matrix script","scopes":["read","playback","library"]}`, "restricted")
	var issued struct {
		Secret string `json:"secret"`
	}
	if status != 201 || json.Unmarshal([]byte(body), &issued) != nil || issued.Secret == "" {
		t.Fatalf("personal access token: %d %s", status, body)
	}
	tool := func(name string, arguments map[string]any) (int, string) {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
		return m.callAs(issued.Secret, "POST", "/v1/mcp", string(raw))
	}
	// A tool's answer carries the route's own answer as text: compare that,
	// with the probed id and the per-request fields taken out.
	said := func(body, id string) string {
		var answer struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
			Error any `json:"error"`
		}
		if json.Unmarshal([]byte(body), &answer) != nil {
			return normalizedAnswer(body, id)
		}
		out := fmt.Sprint(answer.Result.IsError, answer.Error != nil)
		for _, part := range answer.Result.Content {
			out += "\n" + normalizedAnswer(part.Text, id)
		}
		return out
	}
	// Controls: the token works, and reaches the catalogue as its profile.
	if status, body = tool("get_title", map[string]any{"itemId": m.visibleMovie}); status != 200 || !strings.Contains(body, m.visibleMovie) || strings.Contains(body, `"isError":true`) {
		t.Fatalf("control: the token cannot read a title its profile may see: %d %s", status, clip(body))
	}
	answered := 0
	// Listings and searches say nothing about what is hidden.
	listings := []struct {
		name      string
		arguments map[string]any
	}{{"list_libraries", map[string]any{}}, {"list_notifications", map[string]any{}}}
	for _, term := range []string{"Word001", "Volume", "Release", "Person", "movie", "episode"} {
		listings = append(listings, struct {
			name      string
			arguments map[string]any
		}{"search", map[string]any{"query": term}})
	}
	for _, library := range m.libraries {
		for _, kind := range []string{"movie", "show", "album", "book"} {
			listings = append(listings, struct {
				name      string
				arguments map[string]any
			}{"list_titles", map[string]any{"libraryId": library, "kind": kind, "limit": 100}})
		}
	}
	for _, show := range m.visibleEntities["show"] {
		listings = append(listings, struct {
			name      string
			arguments map[string]any
		}{"list_seasons", map[string]any{"showId": show}})
	}
	for _, call := range listings {
		status, body := tool(call.name, call.arguments)
		if status != 200 {
			t.Errorf("%s %v answered %d: %s", call.name, call.arguments, status, clip(body))
			continue
		}
		if !strings.Contains(body, `"isError":true`) {
			answered++
		}
		if found := m.marker.FindString(body); found != "" {
			t.Errorf("%s %v tells the restricted profile's token a hidden identifier %q: %s", call.name, call.arguments, strings.Trim(found, "\"/?&=,: "), clip(body))
		}
	}
	if answered < 8 {
		t.Errorf("only %d listing tools answered the token; the probes are wrong", answered)
	}
	// A tool that names a title, a show or a season answers a hidden one
	// exactly as it answers one that does not exist.
	named := []struct {
		name, argument string
		ids            []string
		rest           map[string]any
	}{
		{"get_title", "itemId", m.hiddenItemSamples(), nil},
		{"list_seasons", "showId", m.hiddenEntities["show"], nil},
		{"list_episodes", "seasonId", m.hiddenEntities["season"], nil},
		{"get_match", "itemId", m.hiddenItemSamples(), nil},
	}
	compared := 0
	for _, call := range named {
		for _, id := range call.ids {
			arguments := func(value string) map[string]any {
				out := map[string]any{call.argument: value}
				for k, v := range call.rest {
					out[k] = v
				}
				return out
			}
			hiddenStatus, hiddenBody := tool(call.name, arguments(id))
			absentStatus, absentBody := tool(call.name, arguments(absentTwin(id)))
			compared++
			if found := m.marker.FindString(strings.ReplaceAll(hiddenBody, id, "")); found != "" {
				t.Errorf("%s on a hidden id answers with another hidden identifier %q", call.name, found)
			}
			if hiddenStatus != absentStatus || said(hiddenBody, id) != said(absentBody, absentTwin(id)) {
				t.Errorf("%s: a hidden id is distinguishable from an absent one:\nhidden %d %s\nabsent %d %s", call.name, hiddenStatus, clip(hiddenBody), absentStatus, clip(absentBody))
			}
		}
	}
	// Marking watched takes a list: a hidden title in it is skipped as an
	// absent one is, and nothing is written for it (the no-effect check).
	for _, id := range m.hiddenItemSamples() {
		hiddenStatus, hiddenBody := tool("mark_watched", map[string]any{"itemIds": []string{id}, "watched": true})
		absentStatus, absentBody := tool("mark_watched", map[string]any{"itemIds": []string{absentTwin(id)}, "watched": true})
		compared++
		if hiddenStatus != absentStatus || said(hiddenBody, id) != said(absentBody, absentTwin(id)) {
			t.Errorf("mark_watched: a hidden id is distinguishable from an absent one:\nhidden %d %s\nabsent %d %s", hiddenStatus, said(hiddenBody, id), absentStatus, said(absentBody, absentTwin(id)))
		}
	}
	if compared < 6 {
		t.Errorf("only %d tool comparisons ran; the fixture has too few hidden things", compared)
	}
}
