package writer

import (
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

func TestMain(m *testing.M) { os.Exit(schematest.Run(m)) }

// Only ordinary canonical fixtures use copies. Production fresh startup and
// its bootstrap refusal controls still call newEmptyExecutor directly.
func testCanonicalExecutor(t *testing.T) (*executor, string) {
	t.Helper()
	path := schematest.Clone(t)
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := &executor{config: cfg, now: time.Now}
	w.openPool(connector, nil)
	t.Cleanup(func() {
		if err := w.close(); err != nil {
			t.Error(err)
		}
	})
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		t.Fatal(err)
	}
	if err := schema.AssertExisting(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	return w, path
}
