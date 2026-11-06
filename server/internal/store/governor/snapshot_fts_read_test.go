package governor

import (
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestSnapshotFTSReadLimitUsesRealCaptureAndBind(t *testing.T) {
	path, writer := snapshotFixture(t)
	if _, err := writer.ExecContext(t.Context(), "DROP TABLE source"); err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), writer); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(t.Context(), "INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(1,'keanu'),(3,'keanu')"); err != nil {
		t.Fatal(err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 128}}
	descriptor := stmt.FTSRead{Slot: stmt.SearchFragmentLexicalReadV1, Shape: stmt.FTSFirst}
	spec, _ := descriptor.Specification()
	defs := []stmt.Definition{{ID: "fts.raw.first", Owner: "governed-native-read-test", CallSites: []string{"snapshot_fts_read_test.go"}, Kind: stmt.Read, SQL: spec.SQL, Params: []stmt.Param{{Name: "query", Type: stmt.Text}, {Name: "limit", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "raw_postings", Bindings: []any{"keanu", int64(1)}}}, Plan: stmt.Plan{Hot: true, NativeRead: &descriptor}}}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(bytes)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: cfg, SHA256: schema.SHA256, Statistics: plan.Unanalyzed, Implementations: "raw native postings only; no logical source or permission proof"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	config := Config{Now: time.Now, MaxGrantsPerClass: 8}
	for i := range config.Partitions {
		config.Partitions[i] = Amounts{Reader: 1, MemoryBytes: 32 << 20, CPUMilli: 1000, IOBytes: 4 << 20, WALBytes: 4 << 20}
	}
	g, a, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := NewSnapshots(connector, cfg, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshots.Close()
	for _, limit := range []int64{0, -1, 65, 1, 64} {
		request := snapshotRequest()
		request.Need[MemoryBytes] = 12 << 20
		request.Budget.RawRows = 64
		_, err := snapshots.Read(t.Context(), request, time.Second, func(scope *ReadScope) error {
			rows, err := scope.Query("fts.raw.first", "keanu", limit)
			if limit <= 0 || limit > 64 {
				if err == nil || !strings.Contains(err.Error(), "FTS LIMIT") {
					t.Fatalf("unbounded/over-window call admitted: limit%d rows%v error%v", limit, rows, err)
				}
				return err
			}
			if err != nil {
				return err
			}
			want := 2
			if limit == 1 {
				want = 1
			}
			if len(rows) != want {
				t.Fatal("bounded native result", limit, rows)
			}
			return nil
		})
		if (limit == 1 || limit == 64) && err != nil {
			t.Fatal(err)
		}
	}
	if a.Usage() != [4]Amounts{} {
		t.Fatal("synthetic read grant leaked")
	}
}
