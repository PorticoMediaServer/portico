package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	livechannels "github.com/PorticoMediaServer/portico/server/internal/livechannels"
	dvr "github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	recordingaccess "github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
)

func registerLiveHTTPContracts(registry *apikit.Registry) error {
	if err := apikit.RegisterHTTP[struct{}, struct {
		ProtocolVersion string                 `json:"protocolVersion"`
		ServerID        string                 `json:"serverId"`
		Value           livechannels.GuidePage `json:"page"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_guide_on_now", Method: "GET", Path: "/v1/guide/on-now", Summary: "Read guide on-now", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Query: []string{"kind", "sourceId", "group", "cursor", "limit"}, QueryIntegers: limitQuery(37, 50), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[struct{}, struct {
		ProtocolVersion string                 `json:"protocolVersion"`
		ServerID        string                 `json:"serverId"`
		Value           livechannels.GuidePage `json:"page"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_guide_now_next", Method: "GET", Path: "/v1/guide/now-next", Summary: "Read guide now-next", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Query: []string{"kind", "sourceId", "group", "cursor", "limit"}, QueryIntegers: limitQuery(37, 50), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[struct{}, struct {
		ProtocolVersion string                   `json:"protocolVersion"`
		ServerID        string                   `json:"serverId"`
		Value           livechannels.AiringsPage `json:"airings"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_guide_airings", Method: "GET", Path: "/v1/guide/airings", Summary: "Read guide airings", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Query: []string{"search", "seriesId", "title", "channelId", "sourceId", "kind", "start", "end", "cursor", "limit"}, QueryIntegers: limitQuery(30, 50), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[livechannels.ReminderPage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_guide_reminders", Method: "GET", Path: "/v1/guide/reminders", Summary: "Get guide reminders", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, BodyLimit: 0, Query: []string{"cursor", "limit"}, QueryIntegers: limitQuery(30, 50), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[livechannels.ReminderInput, LiveOwnerResult[livechannels.Reminder]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_guide_reminders", Method: "PUT", Path: "/v1/guide/reminders", Summary: "Put guide reminders", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, BodyLimit: 4096, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}, LiveOwnerResult[map[string]bool]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_guide_reminders_id", Method: "DELETE", Path: "/v1/guide/reminders/{id}", Summary: "Delete guide reminders", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, BodyLimit: 4096, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[dvr.Summary]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dvr_summary", Method: "GET", Path: "/v1/dvr/summary", Summary: "Read recording summary", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[dvr.GroupPage]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dvr_groups", Method: "GET", Path: "/v1/dvr/groups", Summary: "Read recording groups", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Query: []string{"sort", "cursor", "limit"}, QueryIntegers: limitQuery(30, 50), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dvr_recordings_id_image", Method: "GET", Path: "/v1/dvr/recordings/{id}/image", Summary: "Read retained recording programme image", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Media, Admission: laneMedia, Cost: apikit.Constant, Status: 200, Query: []string{"ld", "sd", "size", "w"}, ResponseContentType: "application/octet-stream", Errors: []string{"unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}

	// internal/httpapi/dvr.go:82
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[dvr.Page]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dvr", Method: "GET", Path: "/v1/dvr", Summary: "Read dvr", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "state", "sort", "group"}, QueryIntegers: emptyLimitQuery(50, 50), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:103
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[dvr.Detail]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dvr_recordings_id", Method: "GET", Path: "/v1/dvr/recordings/{id}", Summary: "Read dvr recordings", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"after"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:111
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[dvr.ChannelChoices]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dvr_channels", Method: "GET", Path: "/v1/dvr/channels", Summary: "Read dvr channels", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "search", "sourceId"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:120
	if err := apikit.RegisterHTTP[struct {
		RequestID   string         "json:\"requestId\""
		Occurrence  dvr.Occurrence "json:\"occurrence\""
		Options     *dvr.Options   "json:\"options\""
		UseDefaults bool           "json:\"useDefaults\""
	}, LiveOwnerResult[dvr.Recording]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_dvr_recordings", Method: "POST", Path: "/v1/dvr/recordings", Summary: "Run dvr recordings", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:136
	if err := apikit.RegisterHTTP[dvr.UpdateInput, LiveOwnerResult[dvr.Recording]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_dvr_recordings_id", Method: "PATCH", Path: "/v1/dvr/recordings/{id}", Summary: "Change dvr recordings", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:143
	if err := apikit.RegisterHTTP[dvr.Mutation, LiveOwnerResult[dvr.Recording]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_dvr_recordings_id_cancel", Method: "POST", Path: "/v1/dvr/recordings/{id}/cancel", Summary: "Run dvr recordings cancel", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:150
	if err := apikit.RegisterHTTP[dvr.KeepInput, LiveOwnerResult[dvr.Recording]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_dvr_recordings_id_keep", Method: "PUT", Path: "/v1/dvr/recordings/{id}/keep", Summary: "Save dvr recordings keep", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:157
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[dvr.DeletePreview]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dvr_recordings_id_delete_preview", Method: "GET", Path: "/v1/dvr/recordings/{id}/delete-preview", Summary: "Read dvr recordings delete preview", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:160
	if err := apikit.RegisterHTTP[dvr.Mutation, LiveOwnerResult[dvr.Recording]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_dvr_recordings_id", Method: "DELETE", Path: "/v1/dvr/recordings/{id}", Summary: "Remove dvr recordings", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:167
	if err := apikit.RegisterHTTP[dvr.RuleConfig, LiveOwnerResult[dvr.RulePreview]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_dvr_rules_preview", Method: "POST", Path: "/v1/dvr/rules/preview", Summary: "Run dvr rules preview", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:174
	if err := apikit.RegisterHTTP[dvr.RuleInput, LiveOwnerResult[dvr.Rule]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_dvr_rules_id", Method: "PUT", Path: "/v1/dvr/rules/{id}", Summary: "Save dvr rules", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:184
	if err := apikit.RegisterHTTP[dvr.DeleteRuleInput, LiveOwnerResult[map[string]bool]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_dvr_rules_id", Method: "DELETE", Path: "/v1/dvr/rules/{id}", Summary: "Remove dvr rules", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:192
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[dvr.StorageStatus]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_dvr_storage", Method: "GET", Path: "/v1/dvr/storage", Summary: "Read dvr storage", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/dvr.go:195
	if err := apikit.RegisterHTTP[dvr.StoragePolicyInput, LiveOwnerResult[dvr.StoragePolicy]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_dvr_storage", Method: "PUT", Path: "/v1/dvr/storage", Summary: "Save dvr storage", Access: apikit.Device, Requires: apikit.ScopeAdmin, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/guide_directory.go:156
	if err := apikit.RegisterHTTP[struct{}, struct {
		ProtocolVersion string                 `json:"protocolVersion"`
		ServerID        string                 `json:"serverId"`
		Directory       livechannels.Directory `json:"directory"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_guide_channels", Method: "GET", Path: "/v1/guide/channels", Summary: "Read guide channels", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"anchorChannelId", "anchorProvenance", "anchorSourceId", "direction", "favorites", "group", "includeHidden", "includeServerHidden", "kind", "limit", "offset", "revision", "search", "sort", "sourceId", "timezone"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 50, Maximum: 50, Description: "With direction, the default is 1 and an explicit limit must be 1."}}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/guide_directory.go:185
	if err := apikit.RegisterHTTP[struct{}, struct {
		ProtocolVersion string                       `json:"protocolVersion"`
		ServerID        string                       `json:"serverId"`
		ViewerFence     string                       `json:"viewerFence"`
		Revision        string                       `json:"revision"`
		Sources         []livechannels.SourceSummary `json:"sources"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_guide_sources", Method: "GET", Path: "/v1/guide/sources", Summary: "Read guide sources", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"includeHidden", "includeServerHidden", "timezone"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/guide_images.go:14
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_guide_programme_image", Method: "GET", Path: "/v1/guide/programme-images/{generation}/{programme}", Summary: "Read guide images", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Media, Admission: laneMedia, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"ld", "sd", "size", "w"}, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:167
	if err := apikit.RegisterHTTP[struct{}, struct {
		ProtocolVersion string                    `json:"protocolVersion"`
		ServerID        string                    `json:"serverId"`
		Channels        []librarychannels.Channel `json:"channels"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_library_channels", Method: "GET", Path: "/v1/admin/library-channels", Summary: "Read server library channels", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:171
	if err := apikit.RegisterHTTP[librarychannels.SaveInput, struct {
		ProtocolVersion string                  `json:"protocolVersion"`
		ServerID        string                  `json:"serverId"`
		Channel         librarychannels.Channel `json:"channel"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels", Method: "POST", Path: "/v1/admin/library-channels", Summary: "Run server library channels", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 1048576, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:179
	if err := apikit.RegisterHTTP[struct {
		Timezone string "json:\"timezone\""
	}, struct {
		ProtocolVersion string                 `json:"protocolVersion"`
		ServerID        string                 `json:"serverId"`
		Config          librarychannels.Config `json:"config"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels_defaults", Method: "POST", Path: "/v1/admin/library-channels/defaults", Summary: "Run server library channels defaults", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:189
	if err := apikit.RegisterHTTP[librarychannels.Config, struct {
		ProtocolVersion string                  `json:"protocolVersion"`
		ServerID        string                  `json:"serverId"`
		Preview         librarychannels.Preview `json:"preview"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels_preview", Method: "POST", Path: "/v1/admin/library-channels/preview", Summary: "Run server library channels preview", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 1048576, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:197
	if err := apikit.RegisterHTTP[librarychannels.ReorderInput, struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerID        string `json:"serverId"`
		Saved           bool   `json:"saved"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels_reorder", Method: "POST", Path: "/v1/admin/library-channels/reorder", Summary: "Run server library channels reorder", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 32768, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:205
	if err := apikit.RegisterHTTP[struct {
		RequestID        string "json:\"requestId\""
		ExpectedRevision int64  "json:\"expectedRevision\""
		Boundary         string "json:\"boundary\""
	}, struct {
		ProtocolVersion string                  `json:"protocolVersion"`
		ServerID        string                  `json:"serverId"`
		Channel         librarychannels.Channel `json:"channel"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels_id_regenerate", Method: "POST", Path: "/v1/admin/library-channels/{id}/regenerate", Summary: "Run server library channels regenerate", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:217
	if err := apikit.RegisterHTTP[librarychannels.Mutation, struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerID        string `json:"serverId"`
		Deleted         bool   `json:"deleted"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels_id_delete", Method: "POST", Path: "/v1/admin/library-channels/{id}/delete", Summary: "Run server library channels delete", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:225
	if err := apikit.RegisterHTTP[struct{}, struct {
		ProtocolVersion string                     `json:"protocolVersion"`
		ServerID        string                     `json:"serverId"`
		Templates       []librarychannels.Template `json:"templates"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_library_channels_templates", Method: "GET", Path: "/v1/admin/library-channels/templates", Summary: "Read server library channels templates", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:229
	if err := apikit.RegisterHTTP[struct {
		RequestID string "json:\"requestId\""
		Timezone  string "json:\"timezone\""
	}, struct {
		ProtocolVersion string                  `json:"protocolVersion"`
		ServerID        string                  `json:"serverId"`
		Channel         librarychannels.Channel `json:"channel"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_library_channels_templates_id_install", Method: "POST", Path: "/v1/admin/library-channels/templates/{id}/install", Summary: "Run server library channels templates install", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/library_channels.go:247
	if err := apikit.RegisterHTTP[livechannels.PreferenceInput, struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerID        string `json:"serverId"`
		Revision        int64  `json:"revision"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_library_channels_preferences", Method: "POST", Path: "/v1/library-channels/preferences", Summary: "Run library channels preferences", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/live_channels.go:224
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64  "json:\"expectedRevision\""
		Enabled          *bool  "json:\"enabled\""
		RequestID        string "json:\"requestId\""
	}, struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerID        string `json:"serverId"`
		Saved           bool   `json:"saved"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_live_sources_id_enabled", Method: "POST", Path: "/v1/admin/live-sources/{id}/enabled", Summary: "Run server live sources enabled", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/live_channels.go:243
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 "json:\"expectedRevision\""
	}, struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerID        string `json:"serverId"`
		Scheduled       bool   `json:"scheduled"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_live_sources_id_refresh", Method: "POST", Path: "/v1/admin/live-sources/{id}/refresh", Summary: "Run server live sources refresh", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/live_channels.go:256
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64  "json:\"expectedRevision\""
		RequestID        string "json:\"requestId\""
	}, struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerID        string `json:"serverId"`
		Removed         bool   `json:"removed"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_live_sources_id_remove", Method: "POST", Path: "/v1/admin/live-sources/{id}/remove", Summary: "Run server live sources remove", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/live_channels.go:270
	if err := apikit.RegisterHTTP[livechannels.PreferenceInput, struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerID        string `json:"serverId"`
		Revision        int64  `json:"revision"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_channels_preferences", Method: "POST", Path: "/v1/channels/preferences", Summary: "Run channels preferences", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/live_channels.go:281
	if err := apikit.RegisterHTTP[struct{}, struct {
		ProtocolVersion string             `json:"protocolVersion"`
		ServerID        string             `json:"serverId"`
		Guide           livechannels.Guide `json:"guide"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_guide", Method: "GET", Path: "/v1/guide", Summary: "Read guide", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"channels", "cursor", "end", "favorites", "group", "includeHidden", "includeServerHidden", "kind", "limit", "programmes", "search", "sourceId", "start", "timezone"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Maximum: 50, Required: true}}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/recording_owners.go:29
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[struct {
		Items      []recordingOwnerChoice `json:"items"`
		NextCursor string                 `json:"nextCursor"`
	}]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_dvr_recording_owners", Method: "GET", Path: "/v1/admin/dvr/recording-owners", Summary: "Read server dvr recording owners", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/recording_permissions.go:17
	if err := apikit.RegisterHTTP[struct{}, LiveOwnerResult[[]recordingaccess.Grant]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_dvr_recording_permissions", Method: "GET", Path: "/v1/admin/dvr/recording-permissions", Summary: "Read server dvr recording permissions", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/recording_permissions.go:31
	if err := apikit.RegisterHTTP[recordingaccess.Grant, LiveOwnerResult[recordingaccess.Grant]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_dvr_recording_permissions", Method: "PUT", Path: "/v1/admin/dvr/recording-permissions", Summary: "Save server dvr recording permissions", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	return nil
}
