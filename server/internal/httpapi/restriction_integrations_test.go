package httpapi

import (
	"context"
	"encoding/hex"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/integrationids"
)

// These classifications are independent of compatibilityRoutes. A new protocol
// template therefore still fails the exhaustive matrix until reviewed here.
// Alias expansion changes only the path spelling, never the request's IDs.
func integrationMatrixAliases(path string) []string {
	return []string{path, strings.ToLower(path), "/mediabrowser" + path, "/mediabrowser" + strings.ToLower(path)}
}

func integrationMatrixGUID(m *restrictionEnv, kind, id string) string {
	guid, err := (integrationids.Store{DB: m.db}).Encode(context.Background(), kind, id)
	if err != nil {
		panic(err)
	}
	return guid
}

func integrationMatrixRead(path, mode string) restrictionRoute {
	return probe(func(m *restrictionEnv) []string {
		user := integrationMatrixGUID(m, "user", m.principals["restricted"].ProfileID)
		path = strings.ReplaceAll(path, "{user}", user)
		switch mode {
		case "list":
			return []string{path + "?Limit=100&Fields=Path,ProviderIds,MediaSources"}
		case "items":
			out := []string{path + "?Limit=100&Fields=Path,ProviderIds,MediaSources"}
			for _, id := range append(m.hiddenItemSamples(), m.visibleMovie) {
				out = append(out, path+"?Ids="+integrationMatrixGUID(m, "item", id)+"&Fields=Path,ProviderIds,MediaSources")
			}
			for _, library := range m.libraries {
				out = append(out, path+"?ParentId="+integrationMatrixGUID(m, "library", library)+"&Recursive=true&Limit=100")
			}
			return out
		case "item", "show":
			ids := append(m.hiddenItemSamples(), m.visibleMovie)
			if mode == "show" {
				ids = append(append([]string{}, m.hiddenEntities["show"]...), m.visibleEntities["show"]...)
			}
			out := []string{}
			for _, id := range ids {
				out = append(out, strings.ReplaceAll(path, "{item}", integrationMatrixGUID(m, "item", id))+"?Fields=Path,ProviderIds,MediaSources")
			}
			return out
		}
		panic("unclassified integration read mode")
	})
}

func addIntegrationRestrictionReads(routes map[string]restrictionRoute) {
	routes["GET /v1/integrations/compatibility"] = static("/v1/integrations/compatibility") // owner capability read; member must be refused
	routes["GET /v1/integrations/app-passwords"] = static("/v1/integrations/app-passwords") // owner account administration, not catalogue data
	routes["GET /v1/integrations/app-passwords/{id}/secret"] = probe(func(m *restrictionEnv) []string {
		return prefixed("/v1/integrations/app-passwords/", m.hiddenItemSamples(), "/secret")
	})
	routes["GET /v1/admin/api-keys/{id}/secret"] = exempt(reasonAdmin)
	routes["GET /v1/integrations/keys"] = exempt("native credential names and recoverable keys within the caller's app grant or owner authority; no catalogue lookup; integration credential tests prove cross-grant refusal")
	routes["GET /v1/integrations/users"] = exempt("native account/profile identity projection only, not catalogue content; integration user tests prove bound-profile and administrator scope")
	routes["GET /v1/integrations/libraries"] = static("/v1/integrations/libraries?limit=100")
	routes["GET /v1/integrations/sessions"] = static("/v1/integrations/sessions?limit=100")
	routes["GET /v1/integrations/items"] = probe(func(m *restrictionEnv) []string {
		out := []string{"/v1/integrations/items?limit=100&includeFiles=true", "/v1/integrations/items?limit=2"}
		for _, kind := range []string{"movie", "show", "season", "episode"} {
			out = append(out, "/v1/integrations/items?kind="+kind+"&limit=100")
		}
		for _, id := range append(m.hiddenItemSamples(), m.visibleMovie) {
			out = append(out, "/v1/integrations/items?ids="+url.QueryEscape(id)+"&includeFiles=true")
		}
		for _, library := range m.libraries {
			out = append(out, "/v1/integrations/items?libraryId="+url.QueryEscape(library)+"&limit=100")
		}
		return out
	})
	routes["GET /v1/integrations/items/{id}/media-info"] = probe(func(m *restrictionEnv) []string {
		return prefixed("/v1/integrations/items/", append(m.hiddenItemSamples(), m.visibleMovie), "/media-info")
	})
	routes["GET /v1/integrations/shows/{id}/seasons"] = perEntity("/v1/integrations/shows/", "show", "/seasons")
	routes["GET /v1/integrations/shows/{id}/episodes"] = perEntity("/v1/integrations/shows/", "show", "/episodes?includeFiles=true")

	// Each exemption is catalogue-unreachable: identity/avatar, server settings,
	// explicit empty capability response, or scoped credentials. Content routes
	// below instead traverse the enabled adapter with real GUIDs and hidden IDs.
	noCatalogue := map[string]string{
		"/System/Configuration":            "native server configuration scalars; no catalogue titles or IDs",
		"/System/Info":                     "native server identity/version/capabilities only; no catalogue lookup",
		"/System/Info/Public":              "public native server identity/version only; no catalogue lookup",
		"/Auth/Keys":                       "native credential rows within caller authority; no catalogue query; scoped recovery tested by integration credential tests",
		"/Users":                           "authorized native account/profile identities only; no catalogue query",
		"/Users/{id}":                      "authorized native account/profile identity only; user ID is not a catalogue item",
		"/Users/{id}/Images/Primary":       "native authorized profile avatar, not a catalogue artwork route",
		"/DisplayPreferences/usersettings": "explicit empty client preference capability response; no catalogue query",
		"/Plugins":                         "explicit empty installed-plugin list; no catalogue query or plugin execution",
	}
	for path, reason := range noCatalogue {
		for _, alias := range integrationMatrixAliases(path) {
			routes["GET "+alias] = exempt(reason)
		}
	}
	content := map[string]struct{ template, mode string }{
		"/Users/{id}/Views":                 {"/Users/{user}/Views", "list"},
		"/Users/{id}/Items":                 {"/Users/{user}/Items", "items"},
		"/Users/{id}/Items/Latest":          {"/Users/{user}/Items/Latest", "list"},
		"/Users/{id}/Items/{item}":          {"/Users/{user}/Items/{item}", "item"},
		"/Library/MediaFolders":             {"/Library/MediaFolders", "list"},
		"/Library/VirtualFolders":           {"/Library/VirtualFolders", "list"},
		"/Items":                            {"/Items", "items"},
		"/Items/Latest":                     {"/Items/Latest", "list"},
		"/Items/{id}/PlaybackInfo":          {"/Items/{item}/PlaybackInfo", "item"},
		"/Items/{id}/Images/{type}":         {"/Items/{item}/Images/Primary", "item"},
		"/Items/{id}/Images/{type}/{index}": {"/Items/{item}/Images/Primary/0", "item"},
		"/Shows/{id}/Seasons":               {"/Shows/{item}/Seasons", "show"},
		"/Shows/{id}/Episodes":              {"/Shows/{item}/Episodes", "show"},
		"/Sessions":                         {"/Sessions", "list"},
	}
	for path, spec := range content {
		aliases, templates := integrationMatrixAliases(path), integrationMatrixAliases(spec.template)
		for i, alias := range aliases {
			routes["GET "+alias] = integrationMatrixRead(templates[i], spec.mode)
		}
	}
}

func addIntegrationRestrictionWrites(routes map[string]writeRoute) {
	routes["POST /v1/integrations/items/{id}/subtitles/refresh"] = perHidden("POST", "/v1/integrations/items/ID/subtitles/refresh", "{}", "item")
	routes["POST /v1/integrations/app-passwords"] = writeExempt("owner-only creation of a credential bound to a native account/profile; no catalogue lookup; native credential authority and reset-session tests cover admission")
	routes["DELETE /v1/integrations/app-passwords/{id}"] = writeExempt("owner-only retirement of an app grant and its credential/device families; the ID is a credential, not catalogue content")
	routes["POST /v1/integrations/authenticate"] = writeExempt("dedicated app-password exchange only; real passwords/PINs refused; no catalogue lookup and no caller-selected catalogue identity")
	routes["POST /v1/integrations/keys"] = writeExempt("native key mint within the existing app grant/scopes; no catalogue query or content mutation; scoped key tests cover escalation refusal")
	noCatalogue := map[string]string{
		"/Users/AuthenticateByName":        "dedicated native app-password exchange; no catalogue lookup, real account passwords/PINs refused",
		"/Auth/Keys":                       "native scoped key mint only; no catalogue query or catalogue mutation",
		"/Library/Refresh":                 "delegates to the owner-only native refresh; a restricted profile is refused before any library is named, and the answer carries no catalogue content",
		"/Sessions/{id}/Playing/{command}": "only an already-admitted native session can be paused/resumed/seeked/stopped; accepts no catalogue ID or playback start; native account/family/revision and title visibility checks retained (compatibility transport and native control authority tests)",
	}
	for path, reason := range noCatalogue {
		for _, alias := range integrationMatrixAliases(path) {
			routes["POST "+alias] = writeExempt(reason)
		}
	}
	for _, alias := range integrationMatrixAliases("/Items/{id}/Refresh") {
		path := alias
		routes["POST "+path] = writeProbes(func(m *restrictionEnv) []idProbe {
			out := []idProbe{}
			for _, id := range m.hiddenItemSamples() {
				guid := integrationMatrixGUID(m, "item", id)
				out = append(out, idProbe{hidden: guid, method: "POST", path: strings.ReplaceAll(path, "{id}", guid) + "?MetadataRefreshMode=ValidationOnly&ImageRefreshMode=None", body: "{}"})
			}
			return out
		})
	}
	for _, alias := range integrationMatrixAliases("/Library/Media/Updated") {
		routes["POST "+alias] = perHidden("POST", alias, `{"Updates":[{"Path":"/media/ID","UpdateType":"Modified"}]}`, "item", "library")
	}
	for _, alias := range integrationMatrixAliases("/Notifications/Admin") {
		routes["POST "+alias] = perHidden("POST", alias, `{"Name":"ID","Description":"ID"}`, "item")
	}
}

// Enable compatibility before probing it, and mark both native and wire GUID
// identities. Otherwise disabled-mode 404s or ID conversion could hide a leak.
func prepareIntegrationRestrictionMatrix(t *testing.T, m *restrictionEnv) {
	t.Helper()
	if _, err := m.db.Exec(`INSERT INTO server_settings(singleton,"integrations.compatibilityEnabled") VALUES(1,1) ON CONFLICT(singleton) DO UPDATE SET "integrations.compatibilityEnabled"=1`); err != nil {
		t.Fatal(err)
	}
	for _, id := range m.hiddenSamples() {
		kind := "item"
		for _, library := range m.libraries {
			if id == library {
				kind = "library"
			}
		}
		m.markers = append(m.markers, integrationMatrixGUID(m, kind, id))
	}
	quoted := make([]string, 0, len(m.markers))
	for _, id := range m.markers {
		quoted = append(quoted, regexp.QuoteMeta(id))
	}
	sort.Slice(quoted, func(i, j int) bool { return len(quoted[i]) > len(quoted[j]) })
	m.marker = regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])(?:` + strings.Join(quoted, "|") + `)(?:$|[^A-Za-z0-9_-])`)
	for _, control := range []struct{ viewer, id string }{{"restricted", m.visibleMovie}, {"open", m.hiddenItems["movie"][0]}} {
		guid := integrationMatrixGUID(m, "item", control.id)
		status, body := m.call("GET", "/Items?Ids="+guid+"&Fields=MediaSources", "", control.viewer)
		if status != 200 || !strings.Contains(body, guid) {
			t.Fatalf("enabled integration control %s: %d %s", control.viewer, status, clip(body))
		}
	}
}

func absentIntegrationGUID(id string) (string, bool) {
	if len(id) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", false
	}
	absent := strings.Repeat("0", 32)
	if id == absent {
		absent = strings.Repeat("0", 31) + "1"
	}
	return absent, true
}
