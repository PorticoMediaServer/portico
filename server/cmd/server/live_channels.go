package main

import (
	"database/sql"
	"log"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// Source URLs and credentials are plain database columns: folder permissions
// are the protection, as in Plex. A state from the file-key era migrates its
// sealed rows on the way in.
func initializeLiveChannels(db *sql.DB, state string) (*livechannels.Store, error) {
	livechannels.MigrateSealedToPlain(db, state)
	store, e := livechannels.New(db)
	if e != nil {
		log.Printf("Live source storage unavailable: %v", e)
		return nil, e
	}
	return store, nil
}
