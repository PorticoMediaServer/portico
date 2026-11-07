package lcbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestMain(m *testing.M) { os.Exit(schematest.Run(m)) }

func TestMGLCRetainedNativeScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := schematest.Clone(t)
		wc := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
		db, _, err := sqliteconn.Open(t.Context(), path, wc)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		cfg := librarychannels.Config{Version: librarychannels.ProtocolVersion, ID: "channel", Name: "Shared channel", Enabled: true, Timezone: "UTC", Seed: "seed", DefaultRuleID: "rule", ViewerAccess: "server-members", Quality: librarychannels.Quality{Mode: "automatic"}, Rules: []librarychannels.Rule{{ID: "rule", Name: "Rule", Mode: "sequential", EpisodeMode: "none", Exhaustion: "loop", MaxConsecutive: 1, Query: librarychannels.Query{LibraryIDs: []string{"movies"}, Kinds: []string{"movie"}, Order: "title"}}}}
		cfg, err = librarychannels.Normalize(cfg)
		if err != nil || librarychannels.Validate(cfg) != nil {
			t.Fatal("public configuration fixture", err)
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/media/電影')`, nil},
			{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'movies','/media/電影')`, nil},
			{`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('channel',1,?,1,0,'Shared channel','building')`, []any{string(raw)}},
			{`INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms) VALUES('generation','channel',1,?,'','seed','building','candidates','',0,10000,0,0,1000)`, []any{string(raw)}},
		} {
			if _, err := conn.ExecContext(t.Context(), q.sql, q.args...); err != nil {
				t.Fatal(err)
			}
		}
		defs := read.RBSharedChannelDefinitions()
		catalogue, err := stmt.Describe(defs)
		if err != nil {
			t.Fatal(err)
		}
		policyJSON, err := schema.ReadContract("INT-INDEX-POLICY.json")
		if err != nil {
			t.Fatal(err)
		}
		policy, err := plan.ReadIndexPolicy(policyJSON)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		capture, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: wc, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "MG retained-copy integration of pinned RB sessionless scope; minimal native fixture, no workload qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		registry, err := stmt.New(defs, capture.Receipt())
		if err != nil {
			t.Fatal(err)
		}
		gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 8}
		gc.Partitions[governor.Background] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 32 << 20}
		g, a, err := governor.New(gc)
		if err != nil {
			t.Fatal(err)
		}
		rc := readfixture.ReaderConfig()
		connector, err := sqliteconn.Connector(path, rc)
		if err != nil {
			t.Fatal(err)
		}
		pool, err := governor.NewSnapshots(connector, rc, g, a, registry)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pool.Close() })
		reader, err := read.NewSharedChannelReader(pool, a, "lc-native-fixture")
		if err != nil {
			t.Fatal(err)
		}
		outRequest := governor.Request{Key: governor.JobKey{Domain: 396, ID: "source-output"}, Owner: 1, Class: governor.Background, Need: governor.Amounts{governor.MemoryBytes: 8 << 20}, Deadline: time.Now().Add(time.Hour)}
		decision := g.Try(outRequest)
		if decision.Grant == nil {
			t.Fatal(decision.Refusal)
		}
		source := read.BrowseOutput{Authority: a, Grant: decision.Grant, Request: outRequest}
		t.Cleanup(func() { g.Cancel(source.Grant); _ = a.Retired(source.Grant) })
		request := readfixture.ReadRequest()
		request.Class = governor.Background
		scope, info, _, err := reader.OpenSharedChannelScope(t.Context(), request, time.Second, source, read.SharedChannelScopeInput{Channel: "channel", Generation: "generation", Rule: "rule", Library: "movies", ExpectedRevision: 1})
		if err != nil {
			t.Fatal(err)
		}
		driver, err := NewDriver(reader, g, a, "generation", 1)
		if err != nil {
			t.Fatal(err)
		}
		values := read.SharedChannelRetainedValues{Scope: scope, Info: &info}
		measured, err := reader.MeasureSharedChannelValues(t.Context(), source, values)
		if err != nil {
			t.Fatal(err)
		}
		before := a.Usage()
		called := false
		size, err := driver.WithValues(t.Context(), source, values, func(_ context.Context, owned read.SharedChannelRetainedValues) error {
			called = true
			if owned.Scope == scope || owned.Info == &info || !reflect.DeepEqual(*owned.Info, info) || a.Usage()[governor.Background][governor.MemoryBytes] != before[governor.Background][governor.MemoryBytes]+measured.ChargeBytes {
				t.Fatal("native copy/fence/custody mismatch")
			}
			return nil
		})
		if err != nil || !called || size != measured || a.Usage() != before || a.Check(source.Grant, source.Request) != nil {
			t.Fatal("native copied scope was released incorrectly", size, err)
		}
	})
}
