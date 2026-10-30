package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"slices"
)

// userMutationRoutes is the reviewed, narrow durable-user cohort.
var userMutationRoutes = map[string]bool{
	"put_admin_backup_settings":                  true,
	"change_console_settings":                    true,
	"put_admin_dvr_settings":                     true,
	"put_admin_libraries_order":                  true,
	"patch_admin_libraries_id":                   true,
	"put_admin_libraries_id_scan_policy":         true,
	"put_library_settings":                       true,
	"put_admin_libraries_id_shows_show_settings": true,
	"put_admin_libraries_id_sonic_analysis":      true,
	"set_live_source_configuration":              true,
	"set_live_settings":                          true,
	"patch_admin_logs_settings":                  true,
	"put_admin_maintenance_settings":             true,
	"put_metadata_settings":                      true,
	"patch_admin_notifications_settings":         true,
	"post_channels_preferences":                  true,
	"set_container_personal_state":               true,
	"undo_container_personal_state":              true,
	"put_download_settings":                      true,
	"put_items_personal_state_batch":             true,
	"put_items_id_personal_state":                true,
	"put_items_itemId_reading_favorite":          true,
	"put_items_itemId_reading_mark":              true,
	"put_items_itemId_reading_my_list":           true,
	"put_libraries_id_metadata_agent":            true,
	"put_libraries_id_metadata_screen":           true,
	"put_libraries_id_strm_analysis_policy":      true,
	"put_reading_group_my_list":                  true,
	"post_libraries_libraryId_reading_mark_all":  true,
	"post_library_channels_preferences":          true,
	"put_listening_preferences":                  true,
	"post_networking_remote_config":              true,
	"stop_account_playback":                      true,
	"stop_playback_session":                      true,
	"post_playlists":                             true,
	"delete_playlists_id":                        true,
	"patch_playlists_id":                         true,
	"post_playlists_id_entries":                  true,
	"delete_playlists_id_entries_entryId":        true,
	"reorder_playlist_window":                    true,
	"delete_playlists_id_shares":                 true,
	"put_playlists_id_shares":                    true,
	"patch_preferences":                          true,
	"delete_reading_saved_group_reference":       true,
	"put_saved_pins_order":                       true,
	"put_saved_pins_kind_id":                     true,
	"post_saved_resources":                       true,
	"delete_saved_resources_id":                  true,
	"patch_saved_resources_id":                   true,
	"put_saved_resources_id_entries":             true,
	"reorder_saved_collection":                   true,
	"delete_saved_resources_id_shares":           true,
	"put_saved_resources_id_shares":              true,
	"put_storage_source_advanced":                true,
	"put_storage_source_program":                 true,
	"put_storage_source_read_profile":            true,
}

func userMutationMetadata(m apikit.Metadata) apikit.Metadata {
	if !userMutationRoutes[m.ID] {
		return m
	}
	if !slices.Contains(m.Headers, "Idempotency-Key") {
		m.Headers = append(m.Headers, "Idempotency-Key")
	}
	if !slices.Contains(m.Headers, "Portico-Mutation-Root") {
		m.Headers = append(m.Headers, "Portico-Mutation-Root")
	}
	for _, code := range []string{"idempotency_conflict", "receipt_expired", "invalid_mutation_key"} {
		if !slices.Contains(m.Errors, code) {
			m.Errors = append(m.Errors, code)
		}
	}
	return m
}
