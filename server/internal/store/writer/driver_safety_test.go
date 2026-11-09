package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"math/rand"
	"testing"
)

type carrierProbe struct {
	at    string
	value any
	hits  int
	names []string
}

func (p *carrierProbe) hit(name string) {
	if p.at == name {
		p.hits++
		panic(p.value)
	}
}

type carrierConn struct{ p *carrierProbe }

func (c carrierConn) SQLiteDQS() (int, int, error) { c.p.hit("conn.dqs"); return 0, 0, nil }

func (c carrierConn) Prepare(string) (driver.Stmt, error) {
	c.p.hit("conn.prepare")
	return carrierStmt{c.p}, nil
}
func (c carrierConn) PrepareContext(context.Context, string) (driver.Stmt, error) {
	return c.Prepare("")
}
func (c carrierConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.p.hit("conn.exec")
	return carrierResult{c.p}, nil
}
func (c carrierConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.p.hit("conn.query")
	return carrierRows{c.p}, nil
}
func (c carrierConn) Begin() (driver.Tx, error)                                    { c.p.hit("conn.begin"); return carrierTx{c.p}, nil }
func (c carrierConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c.Begin() }
func (c carrierConn) Ping(context.Context) error                                   { c.p.hit("conn.ping"); return nil }
func (c carrierConn) ResetSession(context.Context) error                           { c.p.hit("conn.reset"); return nil }
func (c carrierConn) IsValid() bool                                                { c.p.hit("conn.valid"); return true }
func (c carrierConn) Close() error                                                 { c.p.hit("conn.close"); return nil }

type carrierStmt struct{ p *carrierProbe }

func (s carrierStmt) Close() error  { s.p.hit("stmt.close"); return nil }
func (s carrierStmt) NumInput() int { s.p.hit("stmt.inputs"); return 0 }
func (s carrierStmt) Exec([]driver.Value) (driver.Result, error) {
	s.p.hit("stmt.exec")
	return carrierResult{s.p}, nil
}
func (s carrierStmt) Query([]driver.Value) (driver.Rows, error) {
	s.p.hit("stmt.query")
	return carrierRows{s.p}, nil
}
func (s carrierStmt) ExecContext(context.Context, []driver.NamedValue) (driver.Result, error) {
	return s.Exec(nil)
}
func (s carrierStmt) QueryContext(context.Context, []driver.NamedValue) (driver.Rows, error) {
	return s.Query(nil)
}

type carrierRows struct{ p *carrierProbe }

func (r carrierRows) Columns() []string         { r.p.hit("rows.columns"); return r.p.names }
func (r carrierRows) Next([]driver.Value) error { r.p.hit("rows.next"); return nil }
func (r carrierRows) Close() error              { r.p.hit("rows.close"); return nil }

type carrierResult struct{ p *carrierProbe }

func (r carrierResult) LastInsertId() (int64, error) { r.p.hit("result.id"); return 1, nil }
func (r carrierResult) RowsAffected() (int64, error) { r.p.hit("result.affected"); return 1, nil }

type carrierTx struct{ p *carrierProbe }

func (t carrierTx) Commit() error   { t.p.hit("tx.commit"); return nil }
func (t carrierTx) Rollback() error { t.p.hit("tx.rollback"); return nil }

// Deterministic fuzz-style permutations inject at EVERY carrier entry. All
// recoveries must return normally through database/sql's driver boundary and
// retain the first value even when cleanup generates another panic.
func TestDriverCarrierPanicPermutation(t *testing.T) {
	type testCase struct {
		name, target string
		run          func(*driverSafety, *carrierProbe) error
	}
	ctx := t.Context()
	cases := []testCase{
		{"DQS", "conn.dqs", func(s *driverSafety, p *carrierProbe) error {
			_, _, e := (&safeWriterConn{carrierConn{p}, s}).SQLiteDQS()
			return e
		}},
		{"Prepare", "conn.prepare", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterConn{carrierConn{p}, s}).Prepare("")
			return e
		}},
		{"PrepareContext", "conn.prepare", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterConn{carrierConn{p}, s}).PrepareContext(ctx, "")
			return e
		}},
		{"ExecContext", "conn.exec", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterConn{carrierConn{p}, s}).ExecContext(ctx, "", nil)
			return e
		}},
		{"QueryContext", "conn.query", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterConn{carrierConn{p}, s}).QueryContext(ctx, "", nil)
			return e
		}},
		{"Begin", "conn.begin", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterConn{carrierConn{p}, s}).Begin()
			return e
		}},
		{"BeginTx", "conn.begin", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterConn{carrierConn{p}, s}).BeginTx(ctx, driver.TxOptions{})
			return e
		}},
		{"Ping", "conn.ping", func(s *driverSafety, p *carrierProbe) error { return (&safeWriterConn{carrierConn{p}, s}).Ping(ctx) }},
		{"ResetSession", "conn.reset", func(s *driverSafety, p *carrierProbe) error {
			return (&safeWriterConn{carrierConn{p}, s}).ResetSession(ctx)
		}},
		{"IsValid", "conn.valid", func(s *driverSafety, p *carrierProbe) error {
			if (&safeWriterConn{carrierConn{p}, s}).IsValid() {
				t.Fatal("panic validated connection")
			}
			return s.blocked()
		}},
		{"ConnectionClose", "conn.close", func(s *driverSafety, p *carrierProbe) error { return (&safeWriterConn{carrierConn{p}, s}).Close() }},
		{"StmtClose", "stmt.close", func(s *driverSafety, p *carrierProbe) error {
			return (&safeWriterStmt{Stmt: carrierStmt{p}, scope: s}).Close()
		}},
		{"NumInput", "stmt.inputs", func(s *driverSafety, p *carrierProbe) error {
			_ = (&safeWriterStmt{Stmt: carrierStmt{p}, scope: s}).NumInput()
			return s.blocked()
		}},
		{"StmtExec", "stmt.exec", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterStmt{Stmt: carrierStmt{p}, scope: s}).Exec(nil)
			return e
		}},
		{"StmtExecContext", "stmt.exec", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterStmt{Stmt: carrierStmt{p}, scope: s}).ExecContext(ctx, nil)
			return e
		}},
		{"StmtQuery", "stmt.query", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterStmt{Stmt: carrierStmt{p}, scope: s}).Query(nil)
			return e
		}},
		{"StmtQueryContext", "stmt.query", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterStmt{Stmt: carrierStmt{p}, scope: s}).QueryContext(ctx, nil)
			return e
		}},
		{"ColumnsAtCreation", "rows.columns", func(s *driverSafety, p *carrierProbe) error {
			_, e := newSafeWriterRows(carrierRows{p}, s, false)
			return e
		}},
		{"RowsNext", "rows.next", func(s *driverSafety, p *carrierProbe) error {
			r, e := newSafeWriterRows(carrierRows{p}, s, false)
			if e != nil {
				return e
			}
			return r.Next(make([]driver.Value, 1))
		}},
		{"RowsClose", "rows.close", func(s *driverSafety, p *carrierProbe) error {
			r, e := newSafeWriterRows(carrierRows{p}, s, false)
			if e != nil {
				return e
			}
			return r.Close()
		}},
		{"NativeRowsClose", "rows.close", func(s *driverSafety, p *carrierProbe) error { return s.own(true, carrierRows{p}.Close).Close() }},
		{"NativeStmtClose", "stmt.close", func(s *driverSafety, p *carrierProbe) error { return s.own(false, carrierStmt{p}.Close).Close() }},
		{"LastInsertID", "result.id", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterResult{carrierResult{p}, s}).LastInsertId()
			return e
		}},
		{"RowsAffected", "result.affected", func(s *driverSafety, p *carrierProbe) error {
			_, e := (&safeWriterResult{carrierResult{p}, s}).RowsAffected()
			return e
		}},
		{"Commit", "tx.commit", func(s *driverSafety, p *carrierProbe) error { return (&safeWriterTx{carrierTx{p}, s}).Commit() }},
		{"Rollback", "tx.rollback", func(s *driverSafety, p *carrierProbe) error { return (&safeWriterTx{carrierTx{p}, s}).Rollback() }},
		{"Scanner", "scanner", func(s *driverSafety, p *carrierProbe) error {
			p.hits++
			return (guardedScanner{panicScanner{p.value}, s.log}).Scan(int64(11))
		}},
	}
	for seed := int64(0); seed < 32; seed++ {
		order := rand.New(rand.NewSource(seed)).Perm(len(cases))
		for _, index := range order {
			test := cases[index]
			first, second := errors.New("first"), errors.New("later")
			scope := &driverSafety{log: &driverPanicLog{}}
			probe := &carrierProbe{at: test.target, value: first, names: []string{"value"}}
			err := test.run(scope, probe)
			if p := panicFrom(err); p == nil || p.value != first || probe.hits != 1 {
				t.Fatalf("seed%d %s lost panic: %v hits%d", seed, test.name, err, probe.hits)
			}
			later := safeVoid(scope, "cleanup after "+test.name, false, func() error { panic(second) })
			if p := panicFrom(later); p == nil || p.value != first {
				t.Fatalf("seed%d %s replaced original with cleanup: %v", seed, test.name, later)
			}
			events := scope.log.take()
			if len(events) != 2 || events[0].value != first || events[1].value != second {
				t.Fatalf("seed%d %s missing chronological diagnostics: %+v", seed, test.name, events)
			}
		}
	}
}

// A failed prepare may hand back a typed-nil statement behind a non-nil driver
// interface. Both wrappers must return nil, err and register no closer for it,
// so pool close can never dereference the nil handle.
func TestDriverFailedPrepareDoesNotOwnTypedNilStatement(t *testing.T) {
	var typedNil *carrierStmt
	failure := errors.New("prepare failed")
	conn := func() writerDriverConn {
		return typedNilPrepareConn{carrierConn{&carrierProbe{}}, typedNil, failure}
	}
	for name, open := range map[string]func(*driverSafety) (driver.Stmt, error){
		"safe_writer_conn": func(s *driverSafety) (driver.Stmt, error) {
			return (&safeWriterConn{conn(), s}).PrepareContext(t.Context(), "")
		},
		"owned_physical_conn": func(s *driverSafety) (driver.Stmt, error) {
			return (&ownedPhysicalConn{writerDriverConn: conn(), scope: s}).PrepareContext(t.Context(), "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			scope := &driverSafety{log: &driverPanicLog{}}
			st, err := open(scope)
			if !errors.Is(err, failure) || st != nil {
				t.Fatal("failed prepare returned state", st, err)
			}
			scope.mu.Lock()
			registered := len(scope.resources)
			scope.mu.Unlock()
			if registered != 0 {
				t.Fatal("nil statement closer registered", registered)
			}
			if err := scope.closeAll(); err != nil {
				t.Fatal("pool close", err)
			}
		})
	}
}

type typedNilPrepareConn struct {
	carrierConn
	stmt driver.Stmt
	err  error
}

func (c typedNilPrepareConn) Prepare(string) (driver.Stmt, error) { return c.stmt, c.err }
func (c typedNilPrepareConn) PrepareContext(context.Context, string) (driver.Stmt, error) {
	return c.stmt, c.err
}

func TestDriverColumnsStableCopy(t *testing.T) {
	scope := &driverSafety{log: &driverPanicLog{}}
	probe := &carrierProbe{names: []string{"original"}}
	rows, err := newSafeWriterRows(carrierRows{probe}, scope, false)
	if err != nil {
		t.Fatal(err)
	}
	probe.names[0] = "mutated"
	probe.at = "rows.columns"
	probe.value = errors.New("must not be called")
	for range 64 {
		if got := rows.Columns(); len(got) != 1 || got[0] != "original" {
			t.Fatalf("unstable metadata: %v", got)
		}
	}
	if probe.hits != 0 || len(scope.log.take()) != 0 {
		t.Fatal("Columns reentered driver after creation")
	}
}
func TestDriverEarliestCarrierSurvivesLogDrain(t *testing.T) {
	for _, scanner := range []bool{false, true} {
		first, second := errors.New("first"), errors.New("later")
		log := &driverPanicLog{}
		scope := &driverSafety{log: log}
		var original error
		if scanner {
			original = (guardedScanner{panicScanner{first}, log}).Scan(nil)
		} else {
			original = scope.fault("first", first)
		}
		_ = log.take()
		_ = scope.fault("later", second)
		turn := &commandTurn{transaction: &transaction{writer: &executor{driverPanics: log}}}
		var recovered any
		func() { defer func() { recovered = recover() }(); turn.panicAfter(original) }()
		if recovered != first {
			t.Fatalf("scanner%v primary replaced after log drain: %v", scanner, recovered)
		}
		if len(turn.transaction.cleanupPanics) != 1 || turn.transaction.cleanupPanics[0].value != second {
			t.Fatal("later failure not recorded")
		}
	}
}
