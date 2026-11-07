package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func n1NativeEngine(t *testing.T, w *executor) sqliteconn.EngineIdentity {
	t.Helper()
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	actual, err := sqliteconn.AttestEngine(t.Context(), conn)
	if err != nil {
		t.Fatal(err)
	}
	return actual
}

func TestN1EngineMismatchRefusesBeforeBegin(t *testing.T) {
	begins := 0
	w, path, _ := testExecutor(t, func(c driver.Conn) driver.Conn { return &pageBeginCounter{c.(writerDriverConn), &begins} })
	actual := n1NativeEngine(t, w)
	g, a, wal, request := governedFixture(t, w.now)
	if err := refreshWriterWAL(path, wal, w.now); err != nil {
		t.Fatal(err)
	}
	decision := g.Try(request)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	custody, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, decision.Grant, request)
	if err != nil {
		t.Fatal(err)
	}
	custody.inventoryEngine = actual
	custody.inventoryEngine.SourceID += "-different-source"
	before := begins
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if tx != nil {
		defer tx.rollback(nil)
	}
	var refusal *Refusal
	if tx != nil || !errors.Is(err, sqliteconn.ErrPolicy) || !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || begins != before || custody.state.Load() != 2 || a.Usage()[governor.ScanMinimum] != (governor.Amounts{}) {
		t.Fatal("engine mismatch began work or lost native custody", tx, err, begins-before, custody.state.Load())
	}
}

func TestN1EngineAttestsEveryReplacementBeforeBegin(t *testing.T) {
	// Reuse GV's ONE existing keeper and its same governor/WAL composition;
	// closing a writer connection must not delete the WAL between samples.
	cp := newCheckpointComponent(t, true, false)
	w, path := cp.w, cp.path
	expected := n1NativeEngine(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
	if err != nil {
		t.Fatal(err)
	}
	var native []*ownedPhysicalConn
	var trace []string
	w.openPool(connector, func(raw driver.Conn) driver.Conn {
		c := raw.(*ownedPhysicalConn)
		native = append(native, c)
		generation := len(native)
		original := c.writerDriverConn
		c.writerDriverConn = &searchEngineProbeConn{original, func() (string, string, error) {
			trace = append(trace, "engine")
			version, source, err := original.(nativeEngineGetter).SQLiteEngine()
			if generation == 2 {
				source += "-replacement-drift"
			}
			return version, source, err
		}}
		return &n1EngineBeginTrace{c, &trace}
	})
	w.db.SetMaxIdleConns(0) // real native close; the next acquisition is a replacement
	g, a, wal := cp.g, cp.a, cp.wal
	recipe := testRecipe() // unchanged synthetic component unit; no qualification
	request := governor.Request{Key: governor.JobKey{Domain: uint16(InventoryAccept), ID: "engine-replacement"}, Owner: 1,
		Class: governor.ScanMinimum, Writer: true, MaxHold: recipe.limit.Hold, Deadline: cp.now().Add(time.Second),
		Budget:    governor.UnitBudget{RawRows: uint32(recipe.limit.Rows), VMOps: recipe.limit.VMOps, Pages: uint32(recipe.limit.Pages)},
		Need:      governor.Amounts{governor.MemoryBytes: 65536, governor.WALBytes: int64(recipe.limit.WALBytes)},
		Footprint: []governor.Scope{{Library: 1, Domain: uint16(InventoryAccept)}}}
	for generation := 1; generation <= 3; generation++ {
		trace = nil
		if err := refreshWriterWAL(path, wal, w.now); err != nil {
			t.Fatal(err)
		}
		decision := g.Try(request)
		if decision.Grant == nil {
			t.Fatal(decision.Refusal)
		}
		custody, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, decision.Grant, request)
		if err != nil {
			t.Fatal(err)
		}
		custody.inventoryEngine = expected
		tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
		if generation == 2 {
			if tx != nil {
				defer tx.rollback(nil)
			}
			if tx != nil || !errors.Is(err, sqliteconn.ErrPolicy) || !reflect.DeepEqual(trace, []string{"engine"}) {
				t.Fatal("replacement reused previous native evidence", tx, err, trace)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if tx.engineIdentity != expected || !reflect.DeepEqual(trace, []string{"engine", "BEGIN"}) {
				t.Fatal("engine was not observed before BEGIN", tx.engineIdentity, trace)
			}
			if _, err := tx.rollback(nil); err != nil {
				t.Fatal(err)
			}
		}
		if len(native) != generation || custody.state.Load() != 2 || w.db.Stats().OpenConnections != 0 || a.Usage()[governor.ScanMinimum] != (governor.Amounts{}) {
			t.Fatal("replacement or native retirement incomplete", generation, len(native), custody.state.Load(), w.db.Stats(), a.Usage())
		}
	}
}

type n1EngineBeginTrace struct {
	writerDriverConn
	trace *[]string
}

func (c *n1EngineBeginTrace) ExecContext(ctx context.Context, sql string, args []driver.NamedValue) (driver.Result, error) {
	if sql == "BEGIN IMMEDIATE" {
		*c.trace = append(*c.trace, "BEGIN")
	}
	return c.writerDriverConn.ExecContext(ctx, sql, args)
}

func TestN1EngineProfileCannotQualifyAnotherPurposeOrSubset(t *testing.T) {
	for _, purpose := range []CommandID{EffectiveStage, InventoryAccept, InventoryCreate, InventoryClaim, BasicStage, BasicSeal, BasicAccept, SearchImageStage, ProgressAccept, SecurityAccept, ProgressReceiptReclaim, InventoryDirectoryDiscover, InventoryDirectoryEOF, InventoryDirectoryRefuse, InventoryTraversalComplete} {
		p, err := freezeCommandPlan(&executor{}, effectivePlan{inventoryEngine: sqliteconn.EngineIdentity{Version: "profile-engine", SourceID: "profile-source"}}, nil, purpose)
		if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(p, effectivePlan{}) {
			t.Fatal("engine assertion escaped complete N1 composition", purpose, p, err)
		}
	}
}
