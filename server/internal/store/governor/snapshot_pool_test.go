package governor

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestSnapshotPoolSizingIsImmutable(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 2, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 16384, Columns: 16}}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSnapshots(connector, cfg, g, a, snapshotRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg.Connections = 1 // Caller changes cannot shrink the constructed pool.
	pool := any(s.db)
	for _, resizable := range []bool{
		implements[interface{ SetMaxOpenConns(int) }](pool),
		implements[interface{ SetMaxIdleConns(int) }](pool),
		implements[interface{ SetConnMaxLifetime(time.Duration) }](pool),
		implements[interface{ SetConnMaxIdleTime(time.Duration) }](pool),
	} {
		if resizable {
			t.Fatal("snapshot pool exposes a resize-capable handle")
		}
	}
	if s.db.Stats().MaxOpenConnections != 2 || cap(s.slots) != 2 || s.config.Connections != 2 {
		t.Fatal("fixed pool/slot capacity diverged", s.db.Stats(), cap(s.slots), s.config.Connections)
	}
}
func implements[T any](value any) bool { _, ok := value.(T); return ok }
