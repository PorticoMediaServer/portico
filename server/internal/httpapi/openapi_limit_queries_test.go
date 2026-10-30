package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// An independent inventory of current handlers, not the historical F15 count.
func expectedLimitQueries() map[string]apikit.QueryInteger {
	out := map[string]apikit.QueryInteger{}
	add := func(q apikit.QueryInteger, ids ...string) {
		for _, id := range ids {
			out[id] = q
		}
	}
	strict := func(def, max int, empty bool, ids ...string) {
		add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: def, Maximum: max, EmptyIsDefault: empty}, ids...)
	}
	strict(50, 50, false, "get_search_genres")
	strict(25, 100, false, "get_people")
	strict(40, 100, false, "get_playlists_id_content", "get_playlists_id_share_candidates", "get_saved_resources", "get_saved_content", "get_personal_history", "get_saved_resources_id_content", "get_people_id", "get_saved_resources_id_share_candidates")
	strict(40, 40, false, "get_admin_playback_sessions", "get_admin_libraries", "get_admin_jobs")
	strict(100, 100, false, "get_playback_sessions_id_chapters")
	strict(50, 100, false, "get_admin_segments_titles")
	strict(37, 50, false, "get_guide_on_now", "get_guide_now_next")
	strict(30, 50, false, "get_guide_airings", "get_guide_reminders", "get_dvr_groups")
	strict(50, 50, false, "get_guide_channels")
	strict(50, 50, true, "get_dvr")
	strict(25, 100, true, "get_admin_access_members", "get_admin_access_invitations", "get_admin_access_devices", "list_api_keys", "list_access_tokens")
	strict(40, 200, true, "get_admin_filesystem", "get_admin_library_settings", "get_admin_live_sources_id_channel_map", "get_admin_dvr_recording_groups", "get_admin_library_channels_criteria", "get_admin_library_channels_block_presets")
	strict(12, 100, true, "get_admin_play_history_dashboard", "get_admin_play_history_people")
	strict(50, 100, true, "get_reading_items", "get_reading_continue", "get_reading_series_items", "get_reading_periodical_issues")
	strict(24, 100, true, "get_reading_periodical_groups")
	strict(20, 50, true, "get_reading_search", "get_reading_sessions_sessionId_search", "list_download_requests")
	strict(20, 100, false, "get_reading_discover")
	strict(200, 500, true, "get_storage_source_folders")
	strict(64, 128, true, "get_scan_issues")
	strict(40, 100, true, "get_items_id_file_info", "get_item_file_info_groups", "get_item_file_info_members", "get_item_file_info_tracks", "get_item_file_info_head_tracks", "get_item_file_info_sources", "get_item_file_info_disc_titles")
	strict(37, 200, true, "get_items_id_recommendations_row", "get_shows_id_recommendations_row")
	strict(100, 100, true, "get_playlist_order")
	strict(50, 200, true, "list_queue_entries", "get_admin_play_history", "get_admin_playback_history", "list_missing_titles")
	strict(50, 100, true, "list_webhook_deliveries", "query_titles", "integration_items", "integration_sessions", "integration_show_seasons", "integration_show_episodes")
	strict(100, 100, true, "integration_libraries", "integration_users")
	strict(16, 100, true, "integration_media_info")
	strict(40, 100, true, "get_notifications_inbox", "get_feedback_reports", "get_admin_feedback_reports")
	strict(40, 40, true, "get_notifications")
	add(apikit.QueryInteger{Name: "limit", Minimum: 0, Default: 40, Maximum: 40, EmptyIsDefault: true}, "get_library_inventory")
	strict(25, 50, true, "get_metadata_attention", "get_server_metadata_attention")
	strict(256, 256, true, "get_download_usage")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 40, Maximum: 100, Clamped: true, DefaultBelowMinimum: true, DefaultOnInvalid: true, EmptyIsDefault: true}, "get_libraries_id_artists", "get_libraries_id_books", "get_artists_id_albums", "get_albums_id_songs", "get_books_id_files", "get_books_id_chapters", "get_libraries_id_shows", "get_seasons_id_episodes", "get_shows_id_episodes", "get_libraries_id_episode_issues", "get_books_id_timeline", "get_libraries_id_collections", "get_libraries_id_content", "get_libraries_id_browse", "get_collections_id_items", "get_items", "get_libraries_id_listening_selection")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 12, Maximum: 60, Clamped: true, EmptyIsDefault: true}, "get_suggestions", "get_items_id_recommendations", "get_shows_id_recommendations", "get_home", "get_home_row")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 40, Maximum: 40, Clamped: true}, "search")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 200, Maximum: 200, Clamped: true}, "get_browse_facets", "get_library_facets")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 200, Maximum: 200, DefaultBelowMinimum: true, DefaultAboveMaximum: true, DefaultOnInvalid: true, EmptyIsDefault: true}, "get_items_id_credits")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 24, Maximum: 100, ClampMaximum: 24, EmptyIsDefault: true}, "get_items_id_similar")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 100, Maximum: 500, DefaultBelowMinimum: true, EmptyIsDefault: true}, "get_admin_logs")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 25, Maximum: 100, DefaultBelowMinimum: true, EmptyIsDefault: true}, "get_client_logs")
	add(apikit.QueryInteger{Name: "limit", Minimum: 0, Default: 50, Maximum: 100, EmptyIsDefault: true}, "get_remote_playback_controls")
	add(apikit.QueryInteger{Name: "limit", Minimum: 0, Default: 10, Maximum: 200, EmptyIsDefault: true}, "list_admin_sessions")
	add(apikit.QueryInteger{Name: "limit", Minimum: 0, Default: 100, Maximum: 100, EmptyIsDefault: true}, "get_download_request")
	add(apikit.QueryInteger{Name: "limit", Minimum: 0, Default: 50, Maximum: 200, EmptyIsDefault: true}, "list_live_source_groups", "list_live_source_channels")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 50, Maximum: 200, DefaultBelowMinimum: true, DefaultAboveMaximum: true, DefaultOnInvalid: true, EmptyIsDefault: true}, "list_download_preparations")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Default: 200, Maximum: 500, DefaultBelowMinimum: true, DefaultAboveMaximum: true, DefaultOnInvalid: true, EmptyIsDefault: true}, "list_download_revocations")
	add(apikit.QueryInteger{Name: "limit", Minimum: 1, Maximum: 50, Required: true}, "get_guide")
	return out
}

type limitSchemaDTO struct {
	Type        json.RawMessage `json:"type"`
	Minimum     *int            `json:"minimum"`
	Maximum     *int            `json:"maximum"`
	Default     *int            `json:"default"`
	Clamp       *int            `json:"x-clamp-maximum"`
	Below       *int            `json:"x-default-below-minimum"`
	Above       *int            `json:"x-default-above-maximum"`
	Invalid     bool            `json:"x-default-on-invalid"`
	Empty       bool            `json:"x-empty-is-default"`
	Description string          `json:"description"`
	AnyOf       []struct {
		Type  string
		Const *string
	} `json:"anyOf"`
}

func TestGeneratedLimitQueryContracts(t *testing.T) {
	r, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	components, wire, err := PublishedSchemas()
	if err != nil {
		t.Fatal(err)
	}
	generated, err := apikit.GenerateWith(r.Definitions(), apikit.Options{Errors: r.ErrorDefinitions(), Components: components, WireSchemas: wire})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			ID         string `json:"operationId"`
			Parameters []struct {
				Name     string
				In       string
				Required bool
				Schema   limitSchemaDTO
			}
		}
	}
	if err := json.Unmarshal(generated.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	// Parse the generated route DTO in memory, leaving final apigen writes to the lead.
	raw, ok := bytes.CutPrefix(generated.Routes, []byte("// Generated by apigen. Do not edit.\nexport const routes = "))
	if !ok {
		t.Fatal("missing routes export")
	}
	raw, _, ok = bytes.Cut(raw, []byte(" as const;\n"))
	if !ok {
		t.Fatal("missing routes export terminator")
	}
	var routes []apikit.Metadata
	if err := json.Unmarshal(raw, &routes); err != nil {
		t.Fatal(err)
	}
	expected, checked := expectedLimitQueries(), map[string]bool{}
	for _, route := range routes {
		if !slices.Contains(route.Query, "limit") {
			continue
		}
		if route.Protocol == apikit.ProtocolCompatibility {
			consumes := false
			for _, id := range []string{"items", "user_items", "latest", "user_latest", "seasons", "episodes"} {
				if route.ID == "compat_"+id || strings.HasPrefix(route.ID, "compat_"+id+"_") {
					consumes = true
				}
			}
			if !consumes {
				if len(route.QueryIntegers) != 0 {
					t.Errorf("ignored compatibility limit constrained: %s", route.ID)
				}
				continue
			}
			want := apikit.QueryInteger{Name: "limit", Minimum: 0, Maximum: 5000000, NoDefault: true, EmptyIsDefault: true}
			if strings.Contains(route.ID, "latest") {
				want.Default, want.NoDefault = 20, false
			}
			expected[route.ID] = want
		}
		want, ok := expected[route.ID]
		if !ok {
			t.Errorf("limit handler absent from current policy inventory: %s %s", route.ID, route.Path)
			continue
		}
		var got *apikit.QueryInteger
		for i := range route.QueryIntegers {
			if route.QueryIntegers[i].Name == "limit" {
				got = &route.QueryIntegers[i]
			}
		}
		if got == nil {
			t.Errorf("%s lacks integer limit metadata", route.ID)
			continue
		}
		copy := *got
		copy.Description = ""
		if copy != want {
			t.Errorf("%s generated metadata = %+v, want %+v", route.ID, copy, want)
		}
		op := doc.Paths[route.Path][strings.ToLower(route.Method)]
		if op.ID != route.ID {
			t.Errorf("missing operation %s", route.ID)
			continue
		}
		for _, p := range op.Parameters {
			if p.Name != "limit" && !(route.Protocol == apikit.ProtocolCompatibility && p.Name == "Limit") || p.In != "query" {
				continue
			}
			checked[route.ID] = true
			s := p.Schema
			if route.ID == "get_guide_channels" && !strings.Contains(s.Description, "explicit limit must be 1") || route.ID == "get_reading_discover" && !strings.Contains(s.Description, "maximum is 20") || strings.HasPrefix(route.ID, "list_live_source_") && !strings.Contains(s.Description, "default of 40") {
				t.Errorf("%s lost its conditional limit policy: %q", route.ID, s.Description)
			}
			bound := func(name string, got *int, present bool, value int) {
				if present && (got == nil || *got != value) || !present && got != nil {
					t.Errorf("%s %s = %v, want present=%v value=%d", route.ID, name, got, present, value)
				}
			}
			bound("default", s.Default, !want.Required && !want.NoDefault, want.Default)
			bound("minimum", s.Minimum, !want.DefaultBelowMinimum, want.Minimum)
			bound("maximum", s.Maximum, !want.Clamped && !want.DefaultAboveMaximum, want.Maximum)
			bound("below fallback", s.Below, want.DefaultBelowMinimum, want.Minimum)
			bound("above fallback", s.Above, want.DefaultAboveMaximum, want.Maximum)
			clamp := want.ClampMaximum
			if want.Clamped {
				clamp = want.Maximum
			}
			bound("clamp", s.Clamp, want.Clamped || want.ClampMaximum != 0, clamp)
			if s.Invalid != want.DefaultOnInvalid || s.Empty != want.EmptyIsDefault || p.Required != want.Required {
				t.Errorf("%s flags = %+v", route.ID, p)
			}
			if want.DefaultOnInvalid {
				var types []string
				if json.Unmarshal(s.Type, &types) != nil || !reflect.DeepEqual(types, []string{"integer", "string"}) {
					t.Errorf("%s invalid fallback type = %s", route.ID, s.Type)
				}
			} else if want.EmptyIsDefault {
				if len(s.AnyOf) != 2 || s.AnyOf[0].Type != "integer" || s.AnyOf[1].Type != "string" || s.AnyOf[1].Const == nil || *s.AnyOf[1].Const != "" {
					t.Errorf("%s empty fallback schema = %+v", route.ID, s)
				}
			} else if string(s.Type) != `"integer"` {
				t.Errorf("%s type = %s", route.ID, s.Type)
			}
		}
	}
	for id := range expected {
		if !checked[id] {
			t.Errorf("expected limit operation not generated: %s", id)
		}
	}
	t.Logf("verified %d actual limit operations", len(checked))
}

func TestLimitQueryParsersRetainPublishedBoundaries(t *testing.T) {
	saved := func(q string) (int, error) { return savedPageQuery(httptest.NewRequest("GET", "/?"+q, nil)) }
	home := func(q string) (int, error) { return homeQueryLimit(httptest.NewRequest("GET", "/?"+q, nil)) }
	integration := func(q string) (int, error) { return integrationReadLimit(httptest.NewRequest("GET", "/?"+q, nil), 50) }
	for _, tc := range []struct {
		name, input string
		parse       func(string) (int, error)
		want        int
		invalid     bool
	}{
		{"saved default", "", saved, 40, false}, {"saved maximum", "limit=100", saved, 100, false}, {"saved above maximum", "limit=101", saved, 0, true}, {"saved empty", "limit=", saved, 0, true},
		{"home clamp", "limit=61", home, 60, false}, {"home lower", "limit=0", home, 0, true}, {"home invalid", "limit=bad", home, 0, true},
		{"folder maximum", "500", folderLimit, 500, false}, {"folder above maximum", "501", folderLimit, 0, true},
		{"administration parses above maximum for service validation", "201", administrationLimit, 201, false}, {"administration lower", "0", administrationLimit, 0, true},
		{"integration maximum", "limit=100", integration, 100, false}, {"integration above maximum", "limit=101", integration, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.parse(tc.input)
			if (err != nil) != tc.invalid || err == nil && got != tc.want {
				t.Fatalf("got %d %v, want %d invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
}
