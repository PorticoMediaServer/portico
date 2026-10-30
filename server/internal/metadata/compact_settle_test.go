package metadata

import (
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// settleMetadataCatalogue runs the compact projector until it has nothing
// left, standing in for the server's supervised projector: provider
// publication writes catalogue facts that a catalogue read sees only once
// they are published.
func settleMetadataCatalogue(t *testing.T, db *sql.DB) {
	t.Helper()
	catalogtest.New(t, db).Drain()
}
