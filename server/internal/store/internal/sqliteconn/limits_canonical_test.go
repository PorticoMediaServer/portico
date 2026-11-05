package sqliteconn_test

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"path/filepath"
	"testing"
)

func TestReadLimitsCanonicalSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canonical.sqlite")
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), conn); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	conn.Close()
	cfg.Role, cfg.ReadLimits = sqliteconn.Reader, sqliteconn.ReadLimits{LengthBytes: 131072, Columns: 512}
	reader, settings, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if settings.ReadLimits != cfg.ReadLimits {
		t.Fatalf("canonical reader policy: %+v", settings)
	}
	rows, err := reader.QueryContext(t.Context(), "SELECT id FROM catalog_entities LIMIT 0")
	if err != nil {
		t.Fatalf("fixed limits cannot read adopted schema %s: %v", schema.SHA256, err)
	}
	rows.Close()
}
