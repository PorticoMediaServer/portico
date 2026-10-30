package main

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Retention work follows its inputs, not unrelated telemetry or control writes.
// Unknown write scopes still wake this subscription, and worker.Run retains its
// safety tick. The cleanup functions and their deadlines are unchanged.
func minuteCleanupWake() (*worker.Signal, func()) {
	wake := worker.NewSignal()
	stop := dbwork.WakeOnTables(wake,
		"playback_sessions", "personal_*", "playlist_receipts", "saved_resource_receipts",
		"lyric_candidates", "lyric_resources", "subtitle_resources",
		"item_extras", "dvr_catalog_provenance",
		"artwork_*", "metadata_owner_fields", "metadata_owner_history",
		"catalog_*", "books", "book_files", "inventory_*",
		"libraries", "library_*", "server_settings",
	)
	return wake, stop
}
