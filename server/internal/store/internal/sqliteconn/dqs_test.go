package sqliteconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func TestDQSRejectsQuotedTypos(t *testing.T) {
	_, db, cfg := fixture(t)
	c := assertConn(t, db, cfg)
	defer c.Close()
	for _, q := range []string{
		`UPDATE parent SET id=2 WHERE id=1 RETURNING "misspelled_column"`,
		`CREATE TABLE bad_ddl(value TEXT CHECK(value <> "misspelled_column"))`,
	} {
		st, err := c.PrepareContext(t.Context(), q)
		if st != nil {
			st.Close()
		}
		if err == nil {
			t.Fatalf("quoted typo prepared: %s", q)
		}
	}
	var id int
	if err := c.QueryRowContext(t.Context(), `SELECT "id" FROM parent WHERE id=1`).Scan(&id); err != nil || id != 1 {
		t.Fatalf("correct identifier: %d %v", id, err)
	}
	if _, err := c.ExecContext(t.Context(), `CREATE TABLE good_ddl(value TEXT CHECK(value <> 'literal'))`); err != nil {
		t.Fatal(err)
	}
	var badTables int
	if err := c.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE name='bad_ddl'`).Scan(&badTables); err != nil || badTables != 0 {
		t.Fatalf("failed prepare had effects: %d %v", badTables, err)
	}
}

// Interpose only the optional getter while preserving contextual SQL methods.
type dqsConn struct {
	limitTestConn
	ddl, dml int
}

func (c *dqsConn) SQLiteDQS() (int, int, error) { return c.ddl, c.dml, nil }

type dqsConnector struct {
	driver.Connector
	ddl, dml int
	missing  bool
}
type missingDQSConn struct {
	driver.Conn
	driver.QueryerContext
}

func (c *dqsConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if c.missing {
		return &missingDQSConn{raw, raw.(driver.QueryerContext)}, nil
	}
	return &dqsConn{raw.(limitTestConn), c.ddl, c.dml}, nil
}

func TestDQSAssertRejectsFlagsAndMissingGetter(t *testing.T) {
	path, _, cfg := fixture(t)
	base, err := Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ddl, dml int
		missing  bool
	}{{1, 0, false}, {0, 1, false}, {1, 1, false}, {0, 0, true}} {
		db := sql.OpenDB(&dqsConnector{base, tc.ddl, tc.dml, tc.missing})
		c, err := db.Conn(t.Context())
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		s, err := Assert(t.Context(), c, cfg)
		if !errors.Is(err, ErrPolicy) || s.DQSDDL != tc.ddl || s.DQSDML != tc.dml {
			t.Fatalf("accepted drift/missing: %+v %+v %v", tc, s, err)
		}
		c.Close()
		db.Close()
	}
}

func TestDQSAllRolesReplacementAndReaderGrowth(t *testing.T) {
	path, _, cfg := fixture(t)
	for _, role := range []Role{Writer, Reader, Checkpointer} {
		cfg.Role = role
		base, err := Connector(path, cfg)
		if err != nil {
			t.Fatal(err)
		}
		counted := &countedConnector{Connector: base}
		db := sql.OpenDB(counted)
		db.SetMaxOpenConns(3)
		db.SetMaxIdleConns(3)
		c := assertConn(t, db, cfg)
		if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		c.Close()
		var held []*sql.Conn
		n := 1
		if role == Reader {
			n = 3
		}
		for range n {
			c = assertConn(t, db, cfg)
			ddl, dml, err := sqlite.DQS(c)
			if err != nil || ddl != 0 || dml != 0 {
				t.Fatalf("role%d flags%d/%d: %v", role, ddl, dml, err)
			}
			held = append(held, c)
		}
		for _, c := range held {
			c.Close()
		}
		db.Close()
		if counted.opened.Load() != int64(n+1) {
			t.Fatalf("role%d replacements/growth%d", role, counted.opened.Load())
		}
	}
}

func TestDQSActualEnabledDriverRefused(t *testing.T) {
	path, _, cfg := fixture(t)
	u, err := fileURI(path)
	if err != nil {
		t.Fatal(err)
	}
	u.RawQuery = "_dqs=1"
	base, err := sqlite.NewConnector(u.String())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(base)
	defer db.Close()
	c, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := Assert(t.Context(), c, cfg)
	if !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "DQS") || s.DQSDDL != 1 || s.DQSDML != 1 {
		t.Fatalf("native enabled accepted: %+v %v", s, err)
	}
	ddl, dml, err := sqlite.DQS(c)
	if err != nil || ddl != 1 || dml != 1 {
		t.Fatal("assert repaired flags", ddl, dml, err)
	}
}
