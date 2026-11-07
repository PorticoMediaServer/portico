package writer

import (
	"database/sql/driver"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestMain(m *testing.M) { os.Exit(schematest.Run(m)) }

// Only ordinary canonical fixtures use copies. Production fresh startup and
// its bootstrap refusal controls still call newEmptyExecutor directly.
func testCanonicalExecutor(t *testing.T, decorate ...func(driver.Conn) driver.Conn) (*executor, string) {
	t.Helper()
	if len(decorate) > 1 {
		t.Fatal("multiple fixture decorators")
	}
	path := qualificationCanonicalFixturePath(t)
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := &executor{config: cfg, now: time.Now}
	var observe func(driver.Conn) driver.Conn
	if len(decorate) == 1 {
		observe = decorate[0]
	}
	w.openPool(connector, observe)
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
	// Clone already checked the entire file against the template digest, whose
	// canonical native layout was verified once before publication. Repeating
	// that full layout census here would replace the removed DDL work with a
	// second expensive schema walk. Physical connection policy is still checked
	// above and again at every normal frame acquisition.
	return w, path
}
