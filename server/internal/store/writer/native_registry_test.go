package writer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Qualification setup only. This uses the actual owned native connection and
// complete finite definition set, before measured/fault-injected execution.
// It confers no production source, permission, admission or resource proof.
func nativeFixtureRegistry(t *testing.T, conn *sql.Conn, policy sqliteconn.Config, defs []stmt.Definition, sources ...string) *stmt.Registry {
	t.Helper()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, name := range sources {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		hash.Write([]byte(name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: policy, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "owned native fixture registration setup only; no resource or domain admission qualification"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("registration executed fixture effects", before, after, err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func nativeInventoryFixtureRegistry(t *testing.T, w *executor, defs []stmt.Definition, sources ...string) *stmt.Registry {
	t.Helper()
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Called during FULL startup setup, before replacement/cancellation wrappers.
	return nativeFixtureRegistry(t, conn, w.config, defs, sources...)
}
