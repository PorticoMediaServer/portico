package remotesources

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Registry/control inputs, listing/job lifecycle, normalized read facts and the
// authorities/settings that fence retained leases. Telemetry rollups are not an
// input. Unknown write shapes retain dbwork's conservative wake behavior; time
// expiry remains worker.Run's SafetyTick and the retained lease's own lifetime.
var remoteSourceWakeTables = []string{
	"managed_mounts", "mount_controls", "mount_backend_configs", "mount_operations", "mount_revision",
	"remote_sources", "remote_source_read_profiles", "remote_analysis_status", "source_strm_analysis",
	"remote_listing_sessions", "remote_inventory_checks", "remote_scan_cursors", "remote_scan_generations",
	"remote_scan_observations", "remote_scan_observation_hashes",
	"library_sources", "library_network_policy", "library_scan_policies",
	"jobs", "inventory_runs", "inventory_source_active", "inventory_operation_jobs", "inventory_analysis_queue",
	"configuration", "server_settings",
	"accounts", "profile_restrictions", "viewer_revisions",
	"authorization_session_families", "authorization_family_tokens", "authorization_family_grants", "authorization_family_renewals",
	"playback_sessions", "playback_source_pins",
}

func registerRemoteSourceWake(signal *worker.Signal) func() {
	return dbwork.WakeOnTables(signal, remoteSourceWakeTables...)
}
