package playback

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// settlePlaybackCatalog drains derived work so direct-SQL fixtures read as
// settled. Playback's own reads are catalogue facts (synchronous) and need no
// settling; tests of derived views keep calling this.
func settlePlaybackCatalog(t *testing.T, db *sql.DB) {
	t.Helper()
	worker := compactcatalog.NewWorker(db)
	for attempt := 0; attempt < 1000; attempt++ {
		n, err := worker.Step(context.Background(), 32)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("compact playback catalogue did not settle")
}
