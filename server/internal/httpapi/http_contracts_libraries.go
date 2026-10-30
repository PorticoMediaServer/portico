package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	catalog "github.com/PorticoMediaServer/portico/server/internal/catalog"
	ingestion "github.com/PorticoMediaServer/portico/server/internal/ingestion"
	mounts "github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	remotemedia "github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	remotesources "github.com/PorticoMediaServer/portico/server/internal/remotesources"
)

func registerLibrariesHTTPContracts(registry *apikit.Registry) error {
	if err := apikit.RegisterHTTP[struct{}, rcloneprogram.Status](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_rclone_bundled", Method: "GET", Path: "/v1/storage/rclone/bundled", Summary: "Read bundled rclone startup status", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}

	// internal/httpapi/admin.go:62
	if err := apikit.RegisterHTTP[struct{}, catalog.AdminLibraries](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_admin_libraries", Method: "GET", Path: "/v1/admin/libraries", Summary: "Read server library", Query: []string{"cursor", "limit"}, QueryIntegers: limitQuery(40, 40), Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin.go:62
	if err := apikit.RegisterHTTP[struct{}, catalog.AdminJobs](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_admin_jobs", Method: "GET", Path: "/v1/admin/jobs", Summary: "Read server jobs", Query: []string{"libraryId", "status", "cursor", "limit"}, QueryIntegers: limitQuery(40, 40), Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin.go:62
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope   catalog.AdminScope   `json:"scope"`
		Library catalog.AdminLibrary `json:"library"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_libraries_id", Method: "GET", Path: "/v1/admin/libraries/{id}", Summary: "Read server library", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin.go:94
	if err := apikit.RegisterHTTP[struct {
		LibraryIDs []string "json:\"libraryIds\""
	}, struct {
		LibraryIds []string `json:"libraryIds"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_libraries_order", Method: "PUT", Path: "/v1/admin/libraries/order", Summary: "Save server library order", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin.go:114
	if err := apikit.RegisterHTTP[catalog.ShowSettings, catalog.ShowSettings](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_libraries_id_shows_show_settings", Method: "PUT", Path: "/v1/admin/libraries/{id}/shows/{show}/settings", Summary: "Save server library show settings", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/admin.go:132
	if err := apikit.RegisterHTTP[struct {
		Name             string "json:\"name\""
		ExpectedRevision int64  "json:\"expectedRevision\""
	}, struct {
		Scope   catalog.AdminScope   `json:"scope"`
		Library catalog.AdminLibrary `json:"library"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_admin_libraries_id", Method: "PATCH", Path: "/v1/admin/libraries/{id}", Summary: "Change server library", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:10
	if err := apikit.RegisterHTTP[struct{}, struct {
		ServerId  string                  `json:"serverId"`
		Inventory catalog.InventoryStatus `json:"inventory"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_inventory_status", Method: "GET", Path: "/v1/libraries/{id}/inventory-status", Summary: "Read library inventory status", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:36
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope     catalog.AdminScope      `json:"scope"`
		LibraryId string                  `json:"libraryId"`
		Revision  int64                   `json:"revision"`
		Sources   []catalog.LibrarySource `json:"sources"`
		Policy    catalog.ScanPolicy      `json:"policy"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_libraries_id_inventory_config", Method: "GET", Path: "/v1/admin/libraries/{id}/inventory-config", Summary: "Read server library inventory config", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:74
	if err := apikit.RegisterHTTP[catalog.SourceSettings, catalog.LibrarySource](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_libraries_id_sources", Method: "POST", Path: "/v1/admin/libraries/{id}/sources", Summary: "Run server library sources", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"source_changed", "invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:74
	if err := apikit.RegisterHTTP[catalog.SourceSettings, catalog.LibrarySource](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_admin_libraries_id_sources_source", Method: "PATCH", Path: "/v1/admin/libraries/{id}/sources/{source}", Summary: "Change server library sources", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"source_changed", "invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:93
	if err := apikit.RegisterHTTP[struct{}, catalog.LibrarySource](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_libraries_id_sources_source_check", Method: "POST", Path: "/v1/admin/libraries/{id}/sources/{source}/check", Summary: "Run server library sources check", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:106
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 "json:\"expectedRevision\""
	}, struct{}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_admin_libraries_id_sources_source", Method: "DELETE", Path: "/v1/admin/libraries/{id}/sources/{source}", Summary: "Remove server library sources", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 65536, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:126
	if err := apikit.RegisterHTTP[struct{}, ingestion.Job](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_libraries_id_sources_source_scans", Method: "POST", Path: "/v1/admin/libraries/{id}/sources/{source}/scans", Summary: "Run server library sources scans", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 0, Status: 202, Query: []string{"mode"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:139
	if err := apikit.RegisterHTTP[struct {
		Tier             string   "json:\"tier\""
		Operations       []string "json:\"operations\""
		ExpectedRevision int64    "json:\"expectedRevision\""
	}, catalog.ScanPolicy](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_admin_libraries_id_scan_policy", Method: "PUT", Path: "/v1/admin/libraries/{id}/scan-policy", Summary: "Save server library scan policy", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:165
	if err := apikit.RegisterHTTP[struct{}, ingestion.Job](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "post_admin_ingestion_jobs_id_action", Method: "POST", Path: "/v1/admin/ingestion/jobs/{id}/{action}", SuccessStatuses: []int{202, 204}, Summary: "Run server ingestion jobs", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/inventory.go:192
	if err := apikit.RegisterHTTP[struct {
		Revision string "json:\"revision\""
		ItemID   string "json:\"itemId\""
	}, struct {
		ItemId string `json:"itemId"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_libraries_id_inventory_object_action", Method: "POST", Path: "/v1/admin/libraries/{id}/inventory/{object}/{action}", SuccessStatuses: []int{200, 204}, Summary: "Run server library inventory", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/mounts.go:52
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope          catalog.AdminScope `json:"scope"`
		Revision       int64              `json:"revision"`
		Limits         map[string]int     `json:"limits"`
		Mounts         []mounts.Mount     `json:"mounts"`
		MountSupported bool               `json:"mountSupported"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_mounts", Method: "GET", Path: "/v1/storage/mounts", Summary: "Read storage mounts", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/mounts.go:87
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope catalog.AdminScope "json:\"scope\""
		mounts.Receipt
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_operations_id", Method: "GET", Path: "/v1/storage/operations/{id}", Summary: "Read storage operations", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/mounts.go:113
	if err := apikit.RegisterHTTP[mounts.Command, struct {
		Scope catalog.AdminScope "json:\"scope\""
		mounts.Receipt
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_storage_mounts", Method: "POST", Path: "/v1/storage/mounts", Summary: "Run storage mounts", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/mounts.go:113
	if err := apikit.RegisterHTTP[mounts.Command, struct {
		Scope catalog.AdminScope "json:\"scope\""
		mounts.Receipt
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_storage_mounts_id_actions", Method: "POST", Path: "/v1/storage/mounts/{id}/actions", Summary: "Run storage mounts actions", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/mounts.go:113
	if err := apikit.RegisterHTTP[mounts.Command, struct {
		Scope catalog.AdminScope "json:\"scope\""
		mounts.Receipt
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_storage_mounts_id", Method: "DELETE", Path: "/v1/storage/mounts/{id}", Summary: "Remove storage mounts", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:50
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope          catalog.AdminScope     `json:"scope"`
		Sources        []remotesources.Source `json:"sources"`
		MountSupported bool                   `json:"mountSupported"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_sources", Method: "GET", Path: "/v1/storage/sources", Summary: "Read storage sources", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:71
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope   catalog.AdminScope    `json:"scope"`
		Receipt remotesources.Receipt `json:"receipt"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_source_operations_id", Method: "GET", Path: "/v1/storage/source-operations/{id}", Summary: "Read storage source operations", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:90
	if err := apikit.RegisterHTTP[remotesources.DAVInput, struct {
		Scope   catalog.AdminScope    `json:"scope"`
		Receipt remotesources.Receipt `json:"receipt"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_storage_sources_webdav", Method: "POST", Path: "/v1/storage/sources/webdav", Summary: "Run storage sources webdav", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:90
	if err := apikit.RegisterHTTP[remotesources.DAVInput, struct {
		Scope   catalog.AdminScope    `json:"scope"`
		Receipt remotesources.Receipt `json:"receipt"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_storage_sources_webdav_id", Method: "PUT", Path: "/v1/storage/sources/webdav/{id}", Summary: "Save storage sources webdav", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:90
	if err := apikit.RegisterHTTP[remotesources.DAVInput, struct {
		Scope   catalog.AdminScope    `json:"scope"`
		Receipt remotesources.Receipt `json:"receipt"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_storage_sources_webdav_id", Method: "DELETE", Path: "/v1/storage/sources/webdav/{id}", Summary: "Remove storage sources webdav", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:128
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope  catalog.AdminScope `json:"scope"`
		Policy map[string]any     `json:"policy"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_network_roots", Method: "GET", Path: "/v1/libraries/{id}/network-roots", Summary: "Read library network roots", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:171
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope  catalog.AdminScope           `json:"scope"`
		Policy remotesources.AnalysisPolicy `json:"policy"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_strm_analysis_policy", Method: "GET", Path: "/v1/libraries/{id}/strm-analysis-policy", Summary: "Read library strm analysis policy", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:188
	if err := apikit.RegisterHTTP[struct {
		Enabled          bool  "json:\"enabled\""
		ExpectedRevision int64 "json:\"expectedRevision\""
	}, struct {
		Scope  catalog.AdminScope           `json:"scope"`
		Policy remotesources.AnalysisPolicy `json:"policy"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_libraries_id_strm_analysis_policy", Method: "PUT", Path: "/v1/libraries/{id}/strm-analysis-policy", Summary: "Save library strm analysis policy", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/remote_sources.go:213
	if err := apikit.RegisterHTTP[struct{}, struct {
		ItemId string `json:"itemId"`
		State  string `json:"state"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_source_availability", Method: "GET", Path: "/v1/items/{id}/source-availability", Summary: "Read title source availability", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:880
	if err := apikit.RegisterHTTP[struct{}, struct {
		Items []catalog.Library `json:"items"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_libraries", Method: "GET", Path: "/v1/libraries", Summary: "List visible libraries", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:893
	if err := apikit.RegisterHTTP[libraryCreationRequest, catalog.Library](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_libraries", Method: "POST", Path: "/v1/libraries", Summary: "Create a library", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 201, Errors: []string{"source_changed", "invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:959
	if err := apikit.RegisterHTTP[struct {
		Roots            []string "json:\"roots\""
		ExpectedRevision *int64   "json:\"expectedRevision,omitempty\""
	}, struct {
		Approvals []remotemedia.Approval `json:"approvals"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_libraries_id_network_roots", Method: "POST", Path: "/v1/libraries/{id}/network-roots", Summary: "Run library network roots", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:980
	if err := apikit.RegisterHTTP[struct{}, ingestion.Job](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "post_libraries_id_scans", Method: "POST", Path: "/v1/libraries/{id}/scans", Summary: "Run library scans", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 202, Query: []string{"mode"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:993
	if err := apikit.RegisterHTTP[struct{}, ingestion.Job](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_admin_ingestion_jobs_id", Method: "GET", Path: "/v1/admin/ingestion/jobs/{id}", Summary: "Read server ingestion jobs", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:1005
	if err := apikit.RegisterHTTP[struct{}, struct{}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "delete_admin_ingestion_jobs_id", Method: "DELETE", Path: "/v1/admin/ingestion/jobs/{id}", Summary: "Remove server ingestion jobs", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	return nil
}
